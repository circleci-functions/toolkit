package httpcl

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/circleci-functions/toolkit/observability"
)

// Metrics collected from http tracing
type Metrics struct {
	rep  observability.Reporter
	rt   http.RoundTripper
	name string

	mu            sync.Mutex
	poolAvailable atomic.Int64 // an estimate based on reference counting
	inFlight      int64
	inFlightMax   int64

	runOnce sync.Once // to guard calling Run more than once

	// used in private tests to speed up testing the gauge producing Run loop
	forTestingRunDelay time.Duration
}

// NewMetrics creates a new Metrics for capturing metrics from http trace.
// the context is needed to extract the metrics provider.
func NewMetrics(ctx context.Context) *Metrics {
	return &Metrics{
		rep: observability.FromContext(ctx),
	}
}

// Wrap wraps r with the Metrics reference counted round tripper.
func (m *Metrics) Wrap(name string, r http.RoundTripper) http.RoundTripper {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.name = name
	m.rt = r
	return m
}

// RoundTrip makes Metrics a http.RoundTripper. It adds reference counting to
// round trips to be able to count inflight requests.
func (m *Metrics) RoundTrip(req *http.Request) (*http.Response, error) {
	func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		m.inFlight++
		if m.inFlight > m.inFlightMax {
			m.inFlightMax = m.inFlight
		}
	}()

	defer func() {
		m.mu.Lock()
		m.inFlight--
		m.mu.Unlock()
	}()

	// *important* that this is done outside the lock
	return m.rt.RoundTrip(req)
}

func appendTags(a, b observability.Fields) observability.Fields {
	tags := make(observability.Fields, len(a)+len(b))
	for k, v := range a {
		tags[k] = v
	}
	for k, v := range b {
		tags[k] = v
	}
	return tags
}

// Run sends gauge metrics about the client
func (m *Metrics) Run(ctx context.Context) error {
	runDelay := 10 * time.Second
	if m.forTestingRunDelay > 0 {
		runDelay = m.forTestingRunDelay
	}

	m.runOnce.Do(func() {
		ticker := time.NewTicker(runDelay)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sendGauges(ctx)
			}
		}
	})
	return nil
}

const gaugePrefix = "httpclient.gauge."

func (m *Metrics) sendGauges(ctx context.Context) {
	for _, g := range m.gauges() {
		m.rep.Gauge(ctx, gaugePrefix+g.name, g.value, g.tags)
	}
}

type gauge struct {
	name  string
	value float64
	tags  observability.Fields
}

func (m *Metrics) gauges() []gauge {
	m.mu.Lock()
	defer m.mu.Unlock()

	tags := observability.Fields{
		"http.client_name": m.name,
	}

	poolAvail := m.poolAvailable.Load()
	if poolAvail < 0 {
		poolAvail = 0
	}

	return []gauge{
		{name: "in_flight", value: float64(m.inFlight), tags: appendTags(tags, observability.Fields{"type": "instant"})},
		{name: "in_flight", value: float64(m.inFlightMax), tags: appendTags(tags, observability.Fields{"type": "max"})},
		{name: "pool_avail_estimate", value: float64(poolAvail), tags: tags},
	}
}

// WithTracer adds the tracer onto the context for this request.
//
//nolint:funlen // This contains a lot of anonymous functions that are annoying to break out.
func (m *Metrics) WithTracer(ctx context.Context, route string) context.Context {
	r := &request{
		m:   m,
		ctx: ctx,
		commonTags: observability.Fields{
			"http.client_name": m.name,
			"http.route":       route,
		},
	}

	// All the functions in here may be called concurrently within the scope of this context
	// But it is not expected that happens a lot or at a high rater per context, so the lock contention
	// is going to tend to be low.
	trace := &httptrace.ClientTrace{
		GetConn: func(hostPort string) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con = &con{
				host:  hostPort,
				getAt: now,
			}
		},
		GotConn: r.gotCon,
		PutIdleConn: func(err error) {
			r.mu.Lock()
			defer r.mu.Unlock()

			if err != nil {
				return
			}
			r.m.poolAvailable.Add(1)
		},
		GotFirstResponseByte: func() {
			// This can race with WroteRequest (on internal retires I think)
			r.mu.RLock()
			requestDoneAt := r.requestDoneAt
			r.mu.RUnlock()

			if requestDoneAt.IsZero() {
				return
			}
			duration := time.Since(requestDoneAt)

			m.rep.Timing(ctx, "httpclient.req.first_byte",
				duration, r.commonTags)
			m.rep.SetSpanAttribute(ctx, "req.first_byte", duration)
		},
		DNSStart: func(info httptrace.DNSStartInfo) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con.dns = &dns{
				host:    info.Host,
				startAt: now,
			}
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()
			if r.con.dns == nil {
				return
			}
			r.con.dns.doneAt = now

			r.con.dns.addrCount = len(info.Addrs)
			r.con.dns.coalesced = info.Coalesced
			r.con.dns.error = info.Err != nil
		},
		ConnectStart: func(_, _ string) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con.dialStartAt = now
		},
		ConnectDone: func(_, _ string, _ error) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con.dialDoneAt = now
		},
		TLSHandshakeStart: func() {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con.tlsStartAt = now
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			now := time.Now()

			r.mu.Lock()
			defer r.mu.Unlock()

			r.con.tlsDoneAt = now
		},
		WroteRequest: func(_ httptrace.WroteRequestInfo) {
			now := time.Now()

			// This can race with GotFirstResponseByte (on internal retires I think)
			r.mu.Lock()
			r.requestDoneAt = now
			duration := r.requestDoneAt.Sub(r.conDoneAt)
			r.mu.Unlock()

			m.rep.SetSpanAttribute(r.ctx, "req.wrote_request", duration)
			m.rep.Timing(ctx, "httpclient.req.wrote", duration, r.commonTags)
		},
	}
	return httptrace.WithClientTrace(ctx, trace)
}

type request struct {
	m *Metrics
	// this is the per-request context
	ctx        context.Context
	commonTags observability.Fields

	con       *con
	conDoneAt time.Time

	mu            sync.RWMutex
	requestDoneAt time.Time
}

type dns struct {
	startAt   time.Time
	doneAt    time.Time
	host      string
	addrCount int
	coalesced bool
	error     bool
}

type con struct {
	host  string
	getAt time.Time

	dialStartAt time.Time
	dialDoneAt  time.Time
	tlsStartAt  time.Time
	tlsDoneAt   time.Time

	dns *dns
}

func (r *request) commonTagsWith(fields observability.Fields) observability.Fields {
	tags := make(observability.Fields, len(r.commonTags)+len(fields))
	for k, v := range r.commonTags {
		tags[k] = v
	}
	for k, v := range fields {
		tags[k] = v
	}
	return tags
}

func (r *request) gotCon(info httptrace.GotConnInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.con

	if c == nil {
		r.m.rep.SetSpanAttribute(r.ctx, "req.got_conn.no_con", true)
		return // This should be pretty rare - just don't capture the metric
	}
	r.conDoneAt = time.Now()

	commonTags := r.commonTagsWith(observability.Fields{"hostport": c.host})

	tags := observability.Fields{
		"reused":  "false",
		"starved": "false",
		"idle":    "false",
		"delayed": "false",
	}

	// internalConnDelay is the delay getting a connection, because of internal client reasons
	// such as enforced by various internal Max limits. We explicitly remove actual dial delays
	// from the dialing and tls handshaking.
	internalConnDelay := time.Since(c.getAt)

	if info.Reused {
		// Update the approximation to pool available depth
		r.m.poolAvailable.Add(-1)

		const trueTagVal = "true"
		tags["reused"] = trueTagVal

		// If it is reused then there will have been no time taken to form a new connection
		// any delay is due to limiting - very noteworthy
		if internalConnDelay > time.Millisecond*10 {
			tags["delayed"] = trueTagVal
		}

		if info.WasIdle {
			tags["idle"] = trueTagVal
			// this is the time this connection was idle - if this is low the pool is filling
			r.m.rep.Timing(r.ctx, "httpclient.con.idle",
				info.IdleTime, r.commonTags)
			r.m.rep.SetSpanAttribute(r.ctx, "req.con_idle", info.IdleTime)
		} else {
			// Reused connections might not have been made idle, they can be used immediately.

			// this means the connection was never returned to the pool so a request was
			// waiting for the connection, which effectively means the pool was exhausted.
			tags["starved"] = trueTagVal // let's see if this is congruent with delayed
		}
	} else {
		// Getting here means we must have established a new connection.
		// (We never see WasIdle here: a new connection can never have been idle.)

		// Isolate the actual connection forming...
		makeConnectionDuration := c.dialDoneAt.Sub(c.dialStartAt)
		// ...from the internal queueing delays to acquired a connection.
		internalConnDelay -= makeConnectionDuration

		// This is the stand-alone metric that tells us how long it took to physically form a
		// connection including DNS, and TLS handshake, but excluding any internal delays
		// (due to enforced maximum connections etc.)
		r.m.rep.Timing(r.ctx, "httpclient.con.new",
			makeConnectionDuration, commonTags)
		r.m.rep.SetSpanAttribute(r.ctx, "req.con_new", makeConnectionDuration)

		// this separate metric for dns resolution should always be less than makeConnectionDuration
		if c.dns != nil {
			dur := c.dns.doneAt.Sub(c.dns.startAt)

			dnsTags := r.commonTagsWith(observability.Fields{
				"addresses": strconv.Itoa(c.dns.addrCount),
				"coalesced": strconv.FormatBool(c.dns.coalesced),
				"error":     strconv.FormatBool(c.dns.error),
			})
			r.m.rep.Timing(r.ctx, "httpclient.con.dns", dur, dnsTags)
			r.m.rep.SetSpanAttribute(r.ctx, "req.con_dns", dur)
		}
		if !c.tlsDoneAt.IsZero() {
			dur := c.tlsDoneAt.Sub(c.tlsStartAt)
			r.m.rep.Timing(r.ctx, "httpclient.con.tls", dur, commonTags)
			r.m.rep.SetSpanAttribute(r.ctx, "req.con_tls", dur)
		}
	}

	tagList := r.commonTagsWith(tags)
	r.m.rep.SetSpanAttribute(r.ctx, "req.con_wait", internalConnDelay)
	// this metric measures delays in fully internal logic, and is a strong measure of client contention.
	r.m.rep.Timing(r.ctx, "httpclient.con.wait", internalConnDelay, tagList)
}
