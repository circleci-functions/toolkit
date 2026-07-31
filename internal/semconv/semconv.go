// Package semconv contains the standard attributes and helpers for telemetry tooling.
package semconv

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
)

const (
	// clientName is optional and can be used differentiate two clients to the same host within a service
	clientName = "backplane.client.name"

	ErrorType   = string(semconv.ErrorTypeKey)
	ErrorDetail = "error.detail" // not part of semconv, but typically used to capture 'warning' detail
)

type Timing struct {
	Name           string
	Route          string
	Method         string
	StatusCode     int
	AttemptCounter int
}

func HTTPClientTiming(opts Timing) map[string]any {
	return map[string]any{
		"http.client_name": opts.Name,
		"http.route":       opts.Route,
		"http.method":      opts.Method,
		"http.status_code": strconv.Itoa(opts.StatusCode),
		"http.retry":       strconv.FormatBool(opts.AttemptCounter > 1),
	}
}

type RequestVals struct {
	Req        *http.Request
	Route      string
	Attempt    int
	ClientName string
	ClientIP   string
}

// HTTPClientRequest returns fields that match the semantic conventions for HTTP clients
// https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-client
func HTTPClientRequest(v RequestVals) map[string]any {
	u := cleanURL(v.Req)
	as := attributes{
		clientName:                        v.ClientName,
		semconv.URLTemplateKey:            v.Route,
		semconv.HTTPRequestMethodKey:      v.Req.Method,
		semconv.URLFullKey:                u.String(),
		semconv.HTTPRequestResendCountKey: v.Attempt,
		semconv.URLSchemeKey:              u.Scheme,
		semconv.UserAgentOriginalKey:      v.Req.UserAgent(),
		semconv.HTTPRequestBodySizeKey:    v.Req.ContentLength,
	}
	as.merge(serverHostPort(v.Req.Host))
	return as.convert()
}

// HTTPClientResponse returns fields that match the semantic conventions for HTTP clients
// https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-client
func HTTPClientResponse(res *http.Response) map[string]any {
	as := attributes{
		semconv.HTTPResponseStatusCodeKey: res.StatusCode,
	}
	if res.StatusCode >= http.StatusBadRequest {
		as[semconv.ErrorTypeKey] = strconv.Itoa(res.StatusCode)
	}

	as.setString(semconv.HTTPResponseBodySizeKey, res.Header.Get("Content-Length"))
	as.setString("http.response.header.content-type", res.Header.Get("Content-Type"))
	as.setString("http.response.header.content-encoding", res.Header.Get("Content-Encoding"))

	// Note these attributes are net yet specified by semconv but are under IETF draft
	// discussion https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/
	as.setString("http.response.header.x-ratelimit-limit", res.Header.Get("X-Ratelimit-Limit"))
	as.setString("http.response.header.x-ratelimit-remaining", res.Header.Get("X-Ratelimit-Remaining"))
	as.setString("http.response.header.x-ratelimit-reset", res.Header.Get("X-Ratelimit-Reset"))
	as.setString("http.response.header.x-ratelimit-used", res.Header.Get("X-Ratelimit-Used"))

	return as.convert()
}

// HTTPServerRequest returns the fields
// https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-server
func HTTPServerRequest(v RequestVals) map[string]any {
	rURL := redactQueryString(*v.Req.URL)

	as := attributes{
		semconv.HTTPRequestMethodKey: v.Req.Method,
		semconv.URLPathKey:           rURL.Path,
		semconv.URLSchemeKey:         rURL.Scheme,
		semconv.HTTPRouteKey:         v.Route,
	}
	as.merge(serverHostPort(v.Req.Host))

	as.setString(semconv.URLQueryKey, rURL.RawQuery)
	as.setString(semconv.ClientAddressKey, v.ClientIP)
	as.setString(semconv.UserAgentOriginalKey, v.Req.Header.Get("User-Agent"))
	as.setString("http.request.header.referer", v.Req.Header.Get("Referer"))

	return as.convert()
}

func HTTPServerResponse(status int) map[string]any {
	return attributes{
		semconv.HTTPResponseStatusCodeKey: status,
	}.convert()
}

type RabbitVals struct {
	RoutingKey    string
	Destination   string
	Operation     string
	ID            string
	CorrelationID string
	Len           int
	Attempt       int64
}

const rabbitDeliveryAttempt = "messaging.rabbitmq.message.delivery_attempt"

func RabbitMessage(msg RabbitVals) map[string]any {
	as := attributes{
		semconv.MessagingSystemKey:                        "rabbitmq",
		semconv.MessagingRabbitMQDestinationRoutingKeyKey: msg.RoutingKey,
		semconv.MessagingDestinationNameKey:               msg.Destination,
		semconv.MessagingMessageConversationIDKey:         msg.CorrelationID,
		semconv.MessagingMessageIDKey:                     msg.ID,
		// semconv.MessagingOperationNameKey:                 msg.Operation,
		semconv.MessagingOperationTypeKey:   msg.Operation,
		semconv.MessagingMessageBodySizeKey: msg.Len,
	}.convert()
	if msg.Attempt > 0 {
		as[rabbitDeliveryAttempt] = msg.Attempt
	}
	return as
}

func RabbitMetricFields() []string {
	return []string{
		"name", // The span name includes the queue which is quite useful to see in the metrics.
		rabbitDeliveryAttempt,
		string(semconv.MessagingDestinationNameKey),
		string(semconv.MessagingRabbitMQDestinationRoutingKeyKey),
		string(semconv.MessagingSystemKey),
	}
}

type PostgresVals struct {
	Database  string
	Operation string
	QueryName string
}

func Postgres(pgv PostgresVals) map[string]any {
	as := attributes{
		semconv.DBSystemNameKey:    "postgres",
		semconv.DBNamespaceKey:     pgv.Database,
		semconv.DBQuerySummaryKey:  pgv.QueryName,
		semconv.DBOperationNameKey: pgv.Operation,
	}.convert()
	return as
}

// PostgresLegacy are here to keep compatibility with the legacy attributes
// TODO - remove once we are sure we are not using the legacy attributes.
func PostgresLegacy(pgv PostgresVals) map[string]any {
	return map[string]any{
		"command":        pgv.Operation,
		"database-name":  pgv.Database,
		"query-function": pgv.QueryName,
	}
}

func PostgresTimingKeys() []string {
	return []string{
		string(semconv.DBSystemNameKey),
		string(semconv.DBNamespaceKey),
		string(semconv.DBQuerySummaryKey),
		string(semconv.DBOperationNameKey),
		// Legacy fields - TODO remove once we are sure we are not using the legacy attributes.
		"command",
		"database-name",
		"query-function",
	}
}

type TemporalVals struct {
	Destination string // workflow type or activity name
	Operation   string // start-wrokflow, run-workflow,
	ID          string // workflow id
}

func TemporalMessage(msg TemporalVals) map[string]any {
	return attributes{
		semconv.MessagingSystemKey:          "temporal",
		semconv.MessagingDestinationNameKey: msg.Destination,
		semconv.MessagingMessageIDKey:       msg.ID,
		semconv.MessagingOperationTypeKey:   msg.Operation,
	}.convert()
}

// Error returns fields that are populated from the passed in error. If detail is not requested we can assume the
// error detail has been relayed some other way (span description for instance)
func Error(err error, detail bool) map[string]any {
	if err == nil {
		return nil
	}
	fs := map[string]any{}
	if detail {
		// "error.detail" is not part of the semconv spec, but there is no obvious way to capture high cardinality
		// error detail when the span status is not error. For example where there was some error in the context of
		// a span, but it is an error that is not uncommon, so not worthy of further action, but carries useful
		// information. This is essentially the equivalent of the previous 'warning' field.
		desc := ErrorDescription(err, false)
		if desc != "" {
			fs[ErrorDetail] = desc
		}
	}
	errType, explicit := errorType(err)
	if errType == "" {
		// If we explicitly got an empty error type then don't set the field, the error knew better.
		if explicit {
			return fs
		}
		// If we did not detect a type - use the actual type of the error. Effort should be made to map errors to
		// known types.
		errType = typeFromError(err)
		// don't set the field for an empty type
		if errType == "" {
			return fs
		}
	}

	fs[ErrorType] = errType
	return fs
}

// Type returns a low cardinality type for the error, and whether that type was explicit. A type is explicit when an
// error in the chain declared it (via a Type method) or when it is a recognised default (see DefaultErrorType).
// It is not explicit when it had to be derived from the Go type of the error.
// In the case where an error explicitly returns an empty string for the type, the type is extracted from the error
// type. (This is different to the mechanism for setting the error.type field in the span which honors the explicitly
// returned empty string.)
func Type(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	errType, explicit := errorType(err)
	if errType != "" {
		return errType, explicit
	}
	// If we did not detect a type - use the actual type of the error, which is never explicit.
	return typeFromError(err), false
}

// typeFromError returns the type of the error.
// Ongoing Effort should be made to map errors to known types, via DefaultErrorType for instance.
func typeFromError(err error) string {
	ts := strings.TrimPrefix(strings.ToLower(reflect.TypeOf(err).String()), "*")
	// remove the errors package prefix - as a common special case
	ts = strings.TrimPrefix(ts, "errors.")
	switch ts {
	case "errorstring":
		// error string is low value signal
		return ""
	case "joinerror":
		// see if any of the wrapped errors have meaningful types
		u, ok := err.(interface {
			Unwrap() []error
		})
		if !ok {
			return ""
		}
		for _, e := range u.Unwrap() {
			if t := typeFromError(e); t != "" {
				return t
			}
		}
		return ""
	case "fmt.wraperror":
		// see if any other errors have meaningful types
		return typeFromError(errors.Unwrap(err))
	case "observability.wrapwarningerror":
		wt := typeFromError(errors.Unwrap(err))
		if wt != "" {
			return wt
		}
		return "warning"
	}
	// convert package separator to underscore, to be consistent with error type.
	return strings.Replace(ts, ".", "_", 1)
}

// ErrorDescription returns a high cardinality error description, this allows errors to override the description.
// forStatus indicates that the returned description is being used to set the span status.
func ErrorDescription(err error, forStatus bool) string {
	ds, ok := findF(err, func(err error) (string, bool) {
		if e, ok := err.(interface{ Description(bool) string }); ok {
			return e.Description(forStatus), true
		}
		return "", false
	})
	if ok {
		return ds
	}

	return err.Error()
}

// Decide on a low cardinality semconv error type, returns true if an explicit type was found.
// Type should be snake case.
func errorType(err error) (string, bool) {
	ty, ok := findF(err, func(err error) (string, bool) {
		if e, ok := err.(interface{ Type() (string, bool) }); ok {
			t, explicit := e.Type()
			return strings.ReplaceAll(t, "-", "_"), explicit
		}
		if e, ok := err.(interface{ Type() string }); ok {
			return strings.ReplaceAll(e.Type(), "-", "_"), true
		}
		return "", false
	})
	if ok {
		return ty, true
	}
	// A recognised default type is treated as explicit, it is a curated low cardinality type rather than one
	// derived from the Go type of the error.
	if d := DefaultErrorType(err); d != "" {
		return d, true
	}
	return "", false
}

func findF(err error, f func(error) (string, bool)) (string, bool) {
	if err == nil {
		return "", false
	}
	if r, ok := f(err); ok {
		return r, true
	}
	switch x := err.(type) { //nolint:errorlint // This code does its own custom unwrapping.
	case interface{ Unwrap() error }:
		if r, ok := findF(x.Unwrap(), f); ok {
			return r, true
		}
	case interface{ Unwrap() []error }:
		for _, err = range x.Unwrap() {
			if r, ok := findF(err, f); ok {
				return r, true
			}
		}
		return "", false
	default:
		return "", false
	}
	return "", false
}

// DefaultErrorType will return the low cardinality error type for any recognised err.
func DefaultErrorType(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, io.EOF):
		return "eof"
	default:
		return ""
	}
}

// serverHostPort returns the semconv host and port fields, with values extracted from host, which may include the port
// separated by a colon.
func serverHostPort(host string) attributes {
	h, p := hostPort(host)
	return attributes{
		semconv.ServerAddressKey: h,
		semconv.ServerPortKey:    p,
	}
}

func hostPort(in string) (host, port string) {
	host, port, err := net.SplitHostPort(in)
	if err != nil {
		ae := &net.AddrError{}
		if errors.As(err, &ae) {
			if ae.Err == "missing port in address" {
				return in, ""
			}
		}
		return "unknown", ""
	}
	return host, port
}

// cleanup the url and redact any circle-token in the query string
func cleanURL(req *http.Request) url.URL {
	u := *req.URL
	if u.Scheme == "" {
		u.Scheme = "http"
		if req.TLS != nil {
			u.Scheme = "https"
		}
	}
	return redactQueryString(u)
}

// Redact the token from the query string to prevent from leaking it in the url string.
func redactQueryString(u url.URL) url.URL {
	q := u.Query()
	if q.Has("circle-token") {
		q.Set("circle-token", "REDACTED")
	}
	u.RawQuery = q.Encode()
	return u
}

type attributes map[attribute.Key]any

func (a attributes) setString(k attribute.Key, v string) {
	if v == "" {
		return
	}
	a[k] = v
}
func (a attributes) merge(in attributes) {
	for k, v := range in {
		a[k] = v
	}
}

func (a attributes) convert() map[string]any {
	fs := map[string]any{}
	for k, v := range a {
		fs[string(k)] = v
	}
	return fs
}
