// Package httpcl provides an HTTP client instrumented with the observability package, it
// includes resiliency behaviour such as configurable timeouts, retries, authentication
// and connection pooling, with support for backing off when a 429 response code is seen.
package httpcl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v7"

	"github.com/circleci-functions/toolkit/correlation"
	"github.com/circleci-functions/toolkit/internal/semconv"
	o11y "github.com/circleci-functions/toolkit/observability"
)

// JSON is the content-type header value for JSON
const JSON = "application/json; charset=utf-8"

var (
	// ErrServerBackoff is a special error that represents an HTTP response with a backoff status
	ErrServerBackoff = errors.New("server requested explicit backoff")
)

type tracer interface {
	Wrap(name string, r http.RoundTripper) http.RoundTripper
	WithTracer(ctx context.Context, route string) context.Context
	Run(ctx context.Context) error
}

// RateLimitBackoff is an enum representing the different choices for 429 handling in the client
type RateLimitBackoff int

const (
	// RateLimitBackoffDefault is the default handling, which is to reject all client requests anywhere for the next 10 seconds.
	RateLimitBackoffDefault RateLimitBackoff = iota
	// RateLimitBackoffRetry will retry requests internal to the Client.Call, respecting the 'Retry-After' header, if given.
	RateLimitBackoffRetry
	// RateLimitBackoffDisabled will immediately return the 429 error to the caller, suitable for proxying back to original callers.
	RateLimitBackoffDisabled
)

// Config provides the client configuration
type Config struct {
	// Reporter is the observability reporter that the worker will use - TODO: Get from context instead
	Reporter o11y.Reporter

	// Name is used to identify the client in spans
	Name string
	// BaseURL is the URL and optional path prefix to the server that this is a client of.
	BaseURL string
	// AuthHeader the name of the header that the AuthToken will be set on. If empty then
	// the AuthToken will be used in a bearer token authorization header
	AuthHeader string
	// AuthToken is the token to use for authentication.
	AuthToken string
	// AcceptType if set will be used to set the Accept header.
	AcceptType string
	// Timeout is the maximum time any call can take including any retries.
	// Note that a zero Timeout is not defaulted, but means the client will retry indefinitely.
	// The actual overall duration may be slightly more or sometimes less than this, depending on
	// the interplay between the timings of retried calls and the exponential backoff. It is worth noting that
	// this timeout is not responsible for any context cancellation, rather it is used to end the retry loop.
	// (Call cancellation will only happen when the context passed into the call is cancelled, or the per request
	// timeout is hit.)
	Timeout time.Duration
	// PerRequestTimeout sets the default timeout for individual HTTP requests
	// when not specified per-request. If zero, defaults to 5 seconds.
	PerRequestTimeout time.Duration
	// MaxConnectionsPerHost sets the connection pool size
	MaxConnectionsPerHost int
	// UserAgent that will be used for every request
	UserAgent string
	// Transport allows overriding the default HTTP transport the client will use.
	Transport http.RoundTripper
	// TransportModifier can modify the transport after the client has applied other config settings
	TransportModifier func(Transport *http.Transport)
	// Tracer allows http stats tracing to be enabled.
	Tracer tracer
	// DialContext allows a dial context to be injected into the HTTP transport.
	DialContext func(ctx context.Context, network string, addr string) (net.Conn, error)
	// RateLimitBackoff disables the circuit breaker behavior when the client receives a 429
	// response. This is useful if the server rate-limits more granularly than requests-per-client
	// (e.g. if it uses specific query params or headers when bucketing requests) but should be enabled
	// with care as it can lead to thrashing the server if not used appropriately.
	RateLimitBackoff RateLimitBackoff

	// CheckRedirect specifies the policy for handling redirects.
	// If CheckRedirect is not nil, the client calls it before
	// following an HTTP redirect. The arguments req and via are
	// the upcoming request and the requests made already, oldest
	// first. If CheckRedirect returns an error, the http.Client's Get
	// method returns both the previous Response (with its Body
	// closed) and CheckRedirect's error (wrapped in a url.Error)
	// instead of issuing the Request req.
	// As a special case, if CheckRedirect returns http.ErrUseLastResponse,
	// then the most recent response is returned with its body
	// unclosed, along with a nil error.
	//
	// If CheckRedirect is nil, the http.Client uses its default policy,
	// which is to stop after 10 consecutive requests.
	CheckRedirect func(req *http.Request, via []*http.Request) error
	// Jar specifies the cookie jar.
	//
	// The Jar is used to insert relevant cookies into every
	// outbound Request and is updated with the cookie values
	// of every inbound Response. The Jar is consulted for every
	// redirect that the Client follows.
	//
	// If Jar is nil, cookies are only sent if they are explicitly
	// set on the Request.
	Jar http.CookieJar

	// DisableRetries disables the default retry behaviour in the case of timeouts or 5xx.
	// (Setting the NoRetry option on each call will have no effect)
	// When retries are disabled if each call has not set a timeout less than client Timeout, then the
	// per call timeout will be set to 1 second less than the client Timeout
	DisableRetries bool

	// HTTPErrorsAsWarnings will cause all HTTPErrors to be considered warnings in o11y. This would typically only be
	// set when the service is acting as a thin gateway. Passing all HTTP errors back to the client without them
	// registering as an error in the gateway service is desirable.
	HTTPErrorsAsWarnings bool
}

// ProxyConfig returns a config that will set reasonable defaults when the client is acting more like a slim proxy.
// This func might be more useful as a guide as to what to set in those cases.
func ProxyConfig() Config {
	return Config{
		// This is set to ha high number since we would expect most callers to have given up before this time.
		// Any timeout be rippled back to the caller.
		Timeout: 31 * time.Second,

		// This is to cap to the concurrency hitting outbound services to 50x the number of services running.
		// It is limited by hostname, not IP (if multiple are resolved)
		// N.B - be sure to increase this in high throughput pathways that are hitting slow backends!
		MaxConnectionsPerHost: 50,

		// This client should pass any rate limit responses back out to the caller, to decide how to behave, and
		// should not attempt circuit-breaking.
		RateLimitBackoff: RateLimitBackoffDisabled,

		// We should pass any retryable responses back to the caller, not retry in this client.
		// (Setting the NoRetry option on each call will have no effect)
		DisableRetries: true,

		// Since we should be passing back bad status codes to the caller, we should regard them as warnings internally.
		// This is kind of true for 5XX, but this setting still treats 5XX as error, since there is no harm in us being
		// loudly aware of our backend systems 5XX-ing.
		HTTPErrorsAsWarnings: true,
	}
}

// Client is the instrumented http client.
type Client struct {
	reporter o11y.Reporter // Get rid of this when possible

	name                  string
	baseURL               string
	httpClient            *http.Client
	backOffMaxElapsedTime time.Duration
	perRequestTimeout     time.Duration
	authToken             string
	authHeader            string
	acceptType            string
	additionalHeaders     map[string]string
	tracer                tracer
	rateLimitBackoff      RateLimitBackoff
	disableRetries        bool
	httpErrorsAsWarnings  bool

	mu      sync.RWMutex
	last429 time.Time

	now func() time.Time // purely a test hook
}

// New creates a client configured with the config param
func New(ctx context.Context, cfg Config) *Client {
	if cfg.Reporter == nil {
		cfg.Reporter = o11y.FromContext(ctx)
	}

	if cfg.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.MaxConnectionsPerHost == 0 {
			cfg.MaxConnectionsPerHost = 10
		}
		transport.MaxConnsPerHost = cfg.MaxConnectionsPerHost
		transport.MaxIdleConnsPerHost = cfg.MaxConnectionsPerHost
		if cfg.DialContext != nil {
			transport.DialContext = cfg.DialContext
		}
		cfg.Transport = transport
	}
	if cfg.TransportModifier != nil {
		if transport, ok := cfg.Transport.(*http.Transport); ok {
			cfg.TransportModifier(transport)
		}
	}

	additionalHeaders := make(map[string]string)
	if cfg.UserAgent != "" {
		additionalHeaders["User-Agent"] = cfg.UserAgent
	}

	// Default to standard metrics tracer if non specified
	if cfg.Tracer == nil {
		cfg.Tracer = NewMetrics(ctx)
	}

	var roundTripper = cfg.Transport
	if cfg.Tracer != nil {
		roundTripper = cfg.Tracer.Wrap(cfg.Name, roundTripper)
	}
	return &Client{
		name:                  cfg.Name,
		baseURL:               cfg.BaseURL,
		backOffMaxElapsedTime: cfg.Timeout,
		perRequestTimeout:     cfg.PerRequestTimeout,
		authHeader:            cfg.AuthHeader,
		additionalHeaders:     additionalHeaders,
		authToken:             cfg.AuthToken,
		acceptType:            cfg.AcceptType,
		httpClient: &http.Client{
			Transport:     roundTripper,
			Jar:           cfg.Jar,
			CheckRedirect: cfg.CheckRedirect,
		},
		tracer:               cfg.Tracer,
		now:                  time.Now,
		rateLimitBackoff:     cfg.RateLimitBackoff,
		reporter:             cfg.Reporter,
		httpErrorsAsWarnings: cfg.HTTPErrorsAsWarnings,
		disableRetries:       cfg.DisableRetries,
	}
}

// Run runs any added tracer, typically to produce gauge metrics
func (c *Client) Run(ctx context.Context) error {
	return c.tracer.Run(ctx) // N.B. tracer guards against multiple runs
}

// Teardown does nothing but help us satisfy the Component interface
func (c *Client) Teardown(_ context.Context) error { return nil }

// UnixTransport returns a transport that uses the unix socket.
func UnixTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := &net.Dialer{}
			return d.DialContext(ctx, "unix", socket)
		},
	}
}

// CloseIdleConnections is only used for testing.
func (c *Client) CloseIdleConnections() {
	c.httpClient.CloseIdleConnections()
}

// DecoderFunc the type of func the decoders expect.
type DecoderFunc func(r io.Reader) error

const (
	successDecodeStatus = -1
	anyDecodeStatus     = -2
)

// Request is an individual http Request that the Client will send
// NewRequest should be used to create a new Request rather than constructing a Request directly.
type Request struct {
	method  string
	route   string
	body    any    // If set this will be sent as JSON
	rawBody []byte // If set this will be sent as is

	decoders  map[int]DecoderFunc        // If set will be used to decode the response body by http status code
	headerFns []func(header http.Header) // If set will be called with the response header
	cookies   []*http.Cookie
	headers   http.Header
	timeout   time.Duration // The individual per call timeout
	retry     bool
	query     url.Values
	rawquery  string

	propagation bool
	sampleIn    bool

	url string
}

// NewRequest should be used to create a new Request rather than constructing a Request directly.
// This encourages the user to specify a "route" for the tracing, and avoid high cardinality routes
// (when parts of the url may contain many varying values).
//
// Example:
//
//	req := httpclient.NewRequest("POST", "/api/person/%s",
//	  httpclient.RouteParams("person-id"),
//	  httpclient.Timeout(time.Second),
//	)
func NewRequest(method, route string, opts ...func(*Request)) Request {
	r := Request{
		method:      method,
		url:         route,
		route:       route,
		decoders:    map[int]DecoderFunc{},
		headers:     http.Header{},
		query:       url.Values{},
		propagation: true,
		retry:       true,
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// RouteParams are the values used to fill in the route
func RouteParams(routeParams ...any) func(*Request) {
	return func(r *Request) {
		r.url = fmt.Sprintf(r.route, routeParams...)
	}
}

// Decoder adds a response body decoder to some http status code
// Note this will modify the original Request. If retries are enabled, the decoder must be safe to invoke multiple times
//
// Example:
//
//	err := client.Call(ctx, httpclient.NewRequest("POST", "/bad",
//	  httpclient.Decoder(http.StatusBadRequest, httpclient.NewStringDecoder(&s)),
//	))
func Decoder(status int, decoder DecoderFunc) func(*Request) {
	return func(r *Request) {
		r.decoders[status] = decoder
	}
}

// SuccessDecoder sets the decoder for all 2xx statuses
func SuccessDecoder(decoder DecoderFunc) func(*Request) {
	return Decoder(successDecodeStatus, decoder)
}

// AnyStatusDecoder sets the decoder for all statuses
func AnyStatusDecoder(decoder DecoderFunc) func(*Request) {
	return Decoder(anyDecodeStatus, decoder)
}

// JSONDecoder is a shorthand to decode the success body as JSON
func JSONDecoder(resp any) func(*Request) {
	return SuccessDecoder(NewJSONDecoder(resp))
}

// StringDecoder is a shorthand to decode the success body as a string
func StringDecoder(resp *string) func(*Request) {
	return SuccessDecoder(NewStringDecoder(resp))
}

// BytesDecoder is a shorthand to decode the success body as raw bytes
func BytesDecoder(resp *[]byte) func(*Request) {
	return SuccessDecoder(NewBytesDecoder(resp))
}

// ResponseHeader adds a function that will be called when the response returns, passing in the response headers.
func ResponseHeader(f func(http.Header)) func(*Request) {
	return func(r *Request) {
		r.headerFns = append(r.headerFns, f)
	}
}

// Body sets the request body that will be sent as JSON
func Body(body any) func(*Request) {
	return func(r *Request) {
		r.body = body
	}
}

// RawBody sets the request body that will be sent as is
func RawBody(body []byte) func(*Request) {
	return func(r *Request) {
		r.rawBody = body
	}
}

// Cookie sets the cookie for the request
func Cookie(cookie *http.Cookie) func(*Request) {
	return func(r *Request) {
		r.cookies = append(r.cookies, cookie)
	}
}

// Header sets one header value for the request
func Header(key, val string) func(*Request) {
	return func(r *Request) {
		r.headers.Add(key, val)
	}
}

// Headers sets multiple headers in one go for the request
func Headers(headers map[string]string) func(*Request) {
	return func(r *Request) {
		for k, v := range headers {
			r.headers.Add(k, v)
		}
	}
}

// RawQuery sets the unmodified raw query
func RawQuery(rawquery string) func(*Request) {
	return func(r *Request) {
		r.rawquery = rawquery
	}
}

// QueryParam sets one query param for the request
func QueryParam(key, value string) func(*Request) {
	return func(r *Request) {
		r.query.Set(key, value)
	}
}

// QueryParamMulti sets the single query parameter key to the given values, in
// order. Any value already set for that key on this request is replaced. With
// two or more values, the key appears once per value in the outgoing query
// string. With a nil or empty slice, that key is removed from the query string.
func QueryParamMulti(key string, values []string) func(*Request) {
	return func(r *Request) {
		if len(values) == 0 {
			r.query.Del(key)
			return
		}
		r.query.Set(key, values[0])
		for _, v := range values[1:] {
			r.query.Add(key, v)
		}
	}
}

// QueryParams sets multiple query params for the request
func QueryParams(params map[string]string) func(*Request) {
	return func(r *Request) {
		for k, v := range params {
			r.query.Set(k, v)
		}
	}
}

// Timeout sets the individual request timeout,
// and does not take into account of retries.
// This is different from setting the timeout field on the http client,
// which is the total timeout across all retries.
func Timeout(timeout time.Duration) func(*Request) {
	return func(r *Request) {
		r.timeout = timeout
	}
}

// NoRetry prevents any retries from being made for this request.
func NoRetry() func(*Request) {
	return func(r *Request) {
		r.retry = false
	}
}

// Propagation sets the tracing propagation header on the request if set to true,
// the header is not set if set to false
func Propagation(propagation bool) func(*Request) {
	return func(r *Request) {
		r.propagation = propagation
	}
}

// SampleIn marks the client span to be sampled in.
func SampleIn() func(*Request) {
	return func(r *Request) {
		r.sampleIn = true
	}
}

// Call makes the Request call. It will trace out a top level span and a span for any retry attempts.
// Retries will be attempted on any 5XX responses.
// If the http call completed with a non 2XX status code then an HTTPError will be returned containing
// details of result of the call.
//
// Example:
//
//	err := client.Call(ctx, httpclient.NewRequest("POST", "/api/fruit/%s",
//	  httpclient.RouteParams("apple"),
//	  httpclient.Timeout(time.Second),
//	))
//	if err != nil {
//	  panic(err)
//	}
func (c *Client) Call(ctx context.Context, r Request) (err error) {
	// N.B. we don't include a span for Call, since in 99% of the cases it just duplicates the inner
	// HTTP call, and would be expensive. In the retry case it would be handy to have this span.
	// We may consider adding this span in, once we have stabilised o11y. For now the call will emmit
	// an appropriate error event.
	// If adding a span here - remove the error event at the end of the func.
	if err := r.validate(); err != nil {
		return err
	}
	spanName := fmt.Sprintf("%s %s", r.method, r.route)
	// most clients should use NewRequest, but if they created a Request directly
	// use the raw route
	if r.url == "" {
		r.url = r.route
	}
	u, err := url.Parse(c.baseURL + r.url)
	if err != nil {
		return err
	}
	if r.rawquery == "" {
		u.RawQuery = r.query.Encode() // returns "" if query is nil
	} else {
		u.RawQuery = r.rawquery
	}
	// If the client was configured to disable retries force the request to not be retried
	if c.disableRetries {
		r.retry = false
	}

	err = c.retryRequest(ctx, spanName, r, c.requestBuilder(r, u))

	// remove the special retry status to resume normal error/warning behaviour, in case the backoff mechanism
	// did not correctly detect the final retry
	return doneRetrying(err)
}

//nolint:gocognit // Just a list of options, not too complex
func (c *Client) requestBuilder(r Request, u *url.URL) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		//nolint:noctx // We inject a context later with tracing context and a timeout
		req, err := http.NewRequest(r.method, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if c.authToken != "" {
			if c.authHeader != "" {
				req.Header.Set(c.authHeader, c.authToken)
			} else {
				req.Header.Set("Authorization", "Bearer "+c.authToken)
			}
		}

		for k, v := range c.additionalHeaders {
			req.Header.Add(k, v)
		}
		for k, vals := range r.headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		for _, cookie := range r.cookies {
			req.AddCookie(cookie)
		}

		if c.acceptType != "" {
			req.Header.Set("Accept", c.acceptType)
		}

		if r.body != nil {
			req.Header.Set("Content-Type", JSON)
			b := &bytes.Buffer{}
			err = json.NewEncoder(b).Encode(r.body)
			if err != nil {
				return nil, fmt.Errorf("could not json encode Request: %w", err)
			}
			req.ContentLength = int64(b.Len())
			req.Body = io.NopCloser(b)
		}

		if r.rawBody != nil {
			b := bytes.NewReader(r.rawBody)
			req.ContentLength = int64(len(r.rawBody))
			req.Body = io.NopCloser(b)
		}

		return req, nil
	}
}

// retryRequest will make the Request and only call the decoder when a 2XX has been received.
// Any response body in non 2XX cases is discarded.
func (c *Client) retryRequest(ctx context.Context, name string, r Request, newReq func() (*http.Request, error)) error {
	a := attempter{
		c:              c,
		name:           name,
		r:              r,
		newReq:         newReq,
		requestTimeout: c.requestTimeout(r.timeout, r.retry),
	}

	// If retries are disabled then the only request is final.
	if !r.retry {
		return a.attempt(ctx, func(responseDetail) bool { return true })
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = time.Millisecond * 50

	return Retry(ctx, &retry{
		BackOff:        bo,
		MaxElapsedTime: c.backOffMaxElapsedTime,
		Operation:      a.attempt,
		UseDetail:      c.rateLimitBackoff == RateLimitBackoffRetry,
	})
}

// requestTimeout returns a per single http request timeout.
// This defaults for this client are for service to service calls, so it returns a reasonable default
// timeout if one is not specified. The timeout priority is for the one passed in, or the per request imeout configured
// on the client, or the default.
// If the request is configured for no retries, then set the request timeout to be 1 second less than
// the client timeout, when the client timeout is more than the default.
func (c *Client) requestTimeout(timeout time.Duration, retry bool) time.Duration {
	if timeout != 0 {
		return timeout
	}
	// use  the client timeout if set, otherwise use the default
	if c.perRequestTimeout > 0 {
		timeout = c.perRequestTimeout
	} else {
		timeout = time.Second * 5
	}
	// limit the timeout to the max elapsed time in the non retry case
	if !retry && c.backOffMaxElapsedTime > timeout {
		timeout = c.backOffMaxElapsedTime - time.Second
	}
	return timeout
}

type attempter struct {
	c              *Client
	name           string
	r              Request
	newReq         func() (*http.Request, error)
	requestTimeout time.Duration
	attempts       int
}

type responseDetail struct {
	sleep *time.Duration
}

// attempt takes a callback function that will return true if this it the final retry, so that we can
// promote any errors on the final retry to be more than warnings. This is expected to be used in conjunction
// with the Retry function.
func (a *attempter) attempt(ctx context.Context, isFinal func(detail responseDetail) bool) (err error) {
	ctx, endSpan := o11y.StartSpan(ctx, a.name, o11y.WithSpanKind(o11y.SpanKindClient))
	if a.r.sampleIn {
		o11y.SetSpanSampledIn(ctx)
	}
	var detail responseDetail
	defer func() {
		// If this is the final retry then remove the special retry status so the error no longer
		// behaves as a warning.
		if isFinal(detail) {
			err = doneRetrying(err)
		}
		endSpan(&err)
	}()

	before := time.Now()

	a.attempts++

	if a.c.shouldBackoff() {
		return backoff.Permanent(ErrServerBackoff)
	}

	req, err := a.newReq()
	if err != nil {
		return backoff.Permanent(err)
	}

	ctx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()

	if a.c.tracer != nil {
		ctx = a.c.tracer.WithTracer(ctx, a.r.route)
	}

	req = req.WithContext(ctx)
	if a.r.propagation {
		a.c.addPropagationHeader(ctx, req)
	}

	o11y.SetSpanAttributes(ctx, o11y.Fields{
		"http.client_name": a.c.name,
		"http.route":       a.r.route,
		"http.base_url":    a.c.baseURL,
	})
	o11y.SetSpanAttributes(ctx, semconv.LegacyReqToAttributes(req, a.attempts))
	o11y.SetSpanAttributes(ctx, semconv.HTTPClientRequest(semconv.RequestVals{
		Req:     req,
		Route:   a.r.route,
		Attempt: a.attempts,
	}))

	res, err := a.c.httpClient.Do(req)
	if err != nil {
		return extractDoError(err, a.c.httpErrorsAsWarnings)
	}

	defer func() {
		// drain anything left in the body and close it, to ensure we can take advantage of keep alive
		// this is best-efforts so any errors here are not important
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	o11y.Timing(ctx, "httpclient.duration",
		time.Since(before),
		semconv.HTTPClientTiming(semconv.Timing{
			Name:           a.c.name,
			Route:          a.r.route,
			Method:         a.r.method,
			StatusCode:     res.StatusCode,
			AttemptCounter: a.attempts,
		}),
	)
	ctx = o11y.ExtractPropagation(ctx, res.Header)
	o11y.SetSpanAttributes(ctx, semconv.LegacyRespToAttributes(res))
	o11y.SetSpanAttributes(ctx, semconv.HTTPClientResponse(res))

	for _, f := range a.r.headerFns {
		f(res.Header)
	}

	err = extractHTTPError(req, res, a.attempts, a.r.route, a.c.httpErrorsAsWarnings, a.c.rateLimitBackoff == RateLimitBackoffRetry)
	if err != nil {
		if HasStatusCode(err, http.StatusTooManyRequests) {
			a.c.setLast429()
			if s, ok := parseRetryAfterHeader(res.Header["Retry-After"], time.Now()); ok {
				detail.sleep = &s
			}
		}

		// attempt to decode a failure message if registered
		// keep the primary http error, but add the decode error to the span
		errDecode := a.r.decodeBody(res, false)
		if errDecode != nil {
			a.c.reporter.SetSpanAttribute(ctx, "fail_decoder_error", errDecode)
		}

		return err
	}

	return a.r.decodeBody(res, true)
}

// InjectRequestIDs extracts the trace and request IDs from the context, and
// injects them in the request header
func injectRequestIDs(ctx context.Context, req *http.Request) {
	ids := correlation.FromContext(ctx)
	if ids.RequestID != "" {
		req.Header.Set("X-Request-Id", ids.RequestID)
	}
	if ids.ClientTraceID != "" {
		req.Header.Set("X-Client-Trace-Id", ids.ClientTraceID)
	}
}

func (c *Client) addPropagationHeader(ctx context.Context, req *http.Request) {
	o11y.InjectPropagation(ctx, req.Header)
	injectRequestIDs(ctx, req)
}

// decodeBody uses success to decide which registered decoder to use
func (r Request) decodeBody(resp *http.Response, success bool) error {
	var dec DecoderFunc
	if d, ok := r.decoders[anyDecodeStatus]; ok {
		dec = d
	}
	if success {
		if d, ok := r.decoders[successDecodeStatus]; ok {
			dec = d
		}
	}
	if d, ok := r.decoders[resp.StatusCode]; ok {
		dec = d
	}
	if dec != nil {
		err := dec(resp.Body)
		if errors.Is(err, context.DeadlineExceeded) {
			// Most decoding errors are deterministic and so don't want to be retried
			// Timeouts are not - they are influenced by external factors and retries are desirable e.g slow
			// server response times or slow steaming decoders
			return err
		}
		if err != nil {
			// do not retry decoding errors
			return backoff.Permanent(fmt.Errorf("decoder: %w", err))
		}
	}
	return nil
}

// parseRetryAfterHeader parses the Retry-After header and returns the
// delay duration according to the spec: https://httpwg.org/specs/rfc7231.html#header.retry-after
// The bool returned will be true if the header was successfully parsed.
// Otherwise, the header was either not present, or was not parseable according to the spec.
//
// Retry-After headers come in two flavors: Seconds or HTTP-Date
//
// Examples:
// * Retry-After: Fri, 31 Dec 1999 23:59:59 GMT
// * Retry-After: 120
func parseRetryAfterHeader(headers []string, now time.Time) (time.Duration, bool) {
	if len(headers) == 0 || headers[0] == "" {
		return 0, false
	}
	header := headers[0]
	// Retry-After: 120
	if sleep, err := strconv.ParseInt(header, 10, 64); err == nil {
		if sleep < 0 { // a negative sleep doesn't make sense
			return 0, false
		}
		return time.Second * time.Duration(sleep), true
	}

	// Retry-After: Fri, 31 Dec 1999 23:59:59 GMT
	retryTime, err := time.Parse(time.RFC1123, header)
	if err != nil {
		return 0, false
	}
	if until := retryTime.Sub(now); until > 0 {
		return until, true
	}
	// date is in the past
	return 0, true
}

func (c *Client) shouldBackoff() bool {
	if c.rateLimitBackoff == RateLimitBackoffDisabled || c.rateLimitBackoff == RateLimitBackoffRetry {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	// If not yet 10 seconds since the last 429
	return c.now().Before(c.last429.Add(time.Second * 10))
}

func (c *Client) setLast429() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.last429 = c.now()
}

// NewJSONDecoder returns a decoder func enclosing the resp param
// the func returned takes an io reader which will be passed to a json decoder to
// decode into the resp.
func NewJSONDecoder(resp any) DecoderFunc {
	return func(r io.Reader) error {
		if err := json.NewDecoder(r).Decode(resp); err != nil {
			return fmt.Errorf("failed to unmarshal: %w", err)
		}
		return nil
	}
}

// NewBytesDecoder decodes the response body into a byte slice
func NewBytesDecoder(resp *[]byte) DecoderFunc {
	return func(r io.Reader) error {
		bs, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		*resp = bs
		return nil
	}
}

// NewStringDecoder decodes the response body into a string
func NewStringDecoder(resp *string) DecoderFunc {
	return func(r io.Reader) error {
		var bs []byte
		err := NewBytesDecoder(&bs)(r)
		if err != nil {
			return err
		}
		*resp = string(bs)
		return nil
	}
}

// NewNopDecoder can be used to ignore the response body, which can be useful when certain
// responses return surprising response bodies.
// (N.B: The client will still drain and close the response body)
func NewNopDecoder() DecoderFunc {
	return func(io.Reader) error { return nil }
}

// HTTPError represents an error in an HTTP call when the response status code is not 2XX
type HTTPError struct {
	method   string
	route    string
	code     int
	attempts int
	// doneRetrying is set true as soon as we are out of the retry loop, for instance we stopped
	// retrying 5xx calls, or we received a non 5xx call.
	doneRetrying bool
	// warning will mean this error is always treated as a warning
	warning bool
}

var _ error = (*HTTPError)(nil)

func (e *HTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("the response from %s %s was %d (%s) (%d attempts)",
		e.method, e.route, e.code, http.StatusText(e.code), e.attempts)
}

// Type returns the status code as a string if the status was 4xx or 5xx
// This is used by the observability packages.
func (e *HTTPError) Type() string {
	if e.code < http.StatusBadRequest {
		return ""
	}
	return strconv.Itoa(e.code)
}

// Description returns the span status description.
// This is used by the observability packages. The (unused) bool will be true if the description is to set the span
// status, otherwise it is being used for the error details field. In this case since the status code is descriptive
// enough we do not need to return a description in either case.
func (e *HTTPError) Description(_ bool) string {
	return ""
}

// Is checks that this error is being checked for the special o11y error that is not
// added to the trace as an error. If the error is due to relatively expected failure response codes
// return true so it will appear in the trace as a warning.
// N.B. We should not see 3XX codes normally in our client responses, so we currently consider them errors.
func (e *HTTPError) Is(target error) bool {
	switch {
	case !o11y.IsWarningNoUnwrap(target):
		return false
	// doneRetrying will be set true as soon as we are out of the retry loop, for instance if we have
	// stopped retrying 5XXs or got any non 5XX response.
	// Report true for the warning match Whilst we are retrying.
	case !e.doneRetrying:
		return true

	case e.warning:
		return true

	// Since this client package is used mainly for internal inter-service requests, where we expect
	// clients to construct good requests, we don't want all 4xx to be considered warnings.
	// We do want to report errors if one of our internal services is constructing bad requests,
	// however there are a number of 4xx response codes that we do expect our services to use to communicate
	// specific feedback, so these should be warnings.
	// We often expect to see 401, 403, 404 and occasional 429s
	default:
		switch e.code {
		case 401, 403, 404, 429:
			return true
		}
	}
	return false
}

// Code returns the status code recorded in this error.
func (e *HTTPError) Code() int {
	return e.code
}

// HasStatusCode tests err for HTTPError and returns true if any of the codes
// match the stored code.
func HasStatusCode(err error, codes ...int) bool {
	e := &HTTPError{}
	if errors.As(err, &e) {
		for _, code := range codes {
			if e.code == code {
				return true
			}
		}
	}
	return false
}

// IsRequestProblem checks the err for HTTPError and returns true if the stored status code
// is in the 4xx range
func IsRequestProblem(err error) bool {
	e := &HTTPError{}
	if errors.As(err, &e) {
		return e.code >= 400 && e.code < 500
	}
	return false
}

// extractHTTPError returns an HTTPError if the response status code is >=300, otherwise it
// returns nil.
func extractHTTPError(req *http.Request, res *http.Response, attempts int, route string, warning, retry429 bool) error {
	httpErr := &HTTPError{
		method:   req.Method,
		route:    route,
		code:     res.StatusCode,
		attempts: attempts,
		warning:  warning,
	}
	switch {
	case res.StatusCode >= 500:
		// 500 could be temporary server problems, so should retry.
		return httpErr
	case retry429 && res.StatusCode == http.StatusTooManyRequests:
		return httpErr
	case res.StatusCode >= 300:
		// All other none 2XX codes are something we did wrong so exit the retry.
		// Note - our client follows redirects, so we should not see redirect status codes if the redirect
		// response is constructed properly. If we do get a 3XX code here then something is a bit awry.
		httpErr.doneRetrying = true
		return backoff.Permanent(httpErr)
	}
	return nil
}

// extractDoError returns a DoError if the round trip failed
func extractDoError(err error, warning bool) error {
	// url errors repeat the method and url which clutters metrics and logging
	e := &url.Error{}
	if errors.As(err, &e) {
		err = e.Err
		ae := &net.AddrError{}
		if errors.As(err, &ae) {
			// address errors can not be retried
			return &DoError{
				err:     backoff.Permanent(err),
				warning: warning,
				typ:     "net_address",
			}
		}
	}
	// Wrap this in the special error, that reports warning whilst retrying
	return &DoError{err: err, warning: warning}
}

// doneRetrying sets the doneRetrying flag on the special error types
func doneRetrying(err error) error {
	e := &HTTPError{}
	if errors.As(err, &e) {
		e.doneRetrying = true
		return e
	}
	de := &DoError{}
	if errors.As(err, &de) {
		de.doneRetrying = true
		return de
	}
	return err
}

// DoError represents an error in the Do call
type DoError struct {
	err error
	// doneRetrying is set true as soon as we are out of the retry loop
	doneRetrying bool
	// warning will mean this error is always treated as a warning
	warning bool

	// typ is the type of error if detected
	typ string
}

var _ error = (*DoError)(nil)

func (e *DoError) Error() string {
	return fmt.Sprintf("httpclient do: %s", e.err)
}

func (e *DoError) Unwrap() error {
	return e.err
}

// Is checks that this error is being checked for the special o11y error that is not
// added to the trace as an error.
func (e *DoError) Is(target error) bool {
	switch {
	case !o11y.IsWarningNoUnwrap(target):
		return false
	// doneRetrying will be set true as soon as we are out of the retry loop. Whilst retrying consider the error
	// a warning.
	case !e.doneRetrying:
		return true
	case e.warning:
		return true
	// Almost all cancels are from a propagated inbound client cancel, so make these warnings.
	case errors.Is(e.err, context.Canceled):
		return true
	}
	return false
}

// Type returns the error type for the wrapped error.
// This is used by the observability packages. We attempt to return high cardinality values for known common errors.
func (e *DoError) Type() string {
	// Favour the explicit error type if set
	if e.typ != "" {
		return e.typ
	}
	et := semconv.DefaultErrorType(e)
	if et != "" {
		return et
	}
	switch {
	case strings.HasSuffix(e.err.Error(), ": connection reset by peer"):
		return "connection_reset"
	case strings.HasSuffix(e.err.Error(), ": connection refused"):
		return "connection_refused"
	case strings.Contains(e.err.Error(), "i/o timeout") && strings.Contains(e.err.Error(), "dial"):
		return "dial_timeout"
	case strings.Contains(e.err.Error(), "closed idle connection"):
		return "closed_idle"
	}
	return "unknown"
}

func (r Request) validate() error {
	// We do not allow GET requests with Body as they are unusual (and not supported by CloudFront WAF)
	if !r.validateGetWithBody() {
		return errors.New("cannot have GET request with body or raw body")
	}

	if !r.validateOnlyRawBodyOrBody() {
		return errors.New("cannot have both body and raw body be set")
	}

	return nil
}

func (r Request) validateOnlyRawBodyOrBody() bool {
	if r.rawBody != nil && r.body != nil {
		return false
	}

	return true
}

func (r Request) validateGetWithBody() bool {
	return r.method != "GET" || !r.hasBody()
}

func (r Request) hasBody() bool {
	return r.body != nil || r.rawBody != nil
}
