//nolint:revive // Dont use this
package otel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/secret"
)

func init() { //nolint:gochecknoinits // no decent alternative at the moment
	// Register the trace context propagator so data is propagated across services/processes.
	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	otel.SetTextMapPropagator(propagator)
}

type TracerProvider struct {
	cfg TracerProviderConfig
	tp  *sdktrace.TracerProvider
}

var _ report.TraceEmitter = (*TracerProvider)(nil)

// TracerProviderConfig configures a TracerProvider
type TracerProviderConfig struct {
	Resource ResourceConfig
	Batchers []SpanExporterConfig
	Syncers  []SpanExporterConfig

	ExecutionTempSpanProcessor sdktrace.SpanProcessor // Only in use by Execution
}

type ResourceConfig struct {
	UseEnvironments       bool
	ServiceName           string
	Revision              string
	DeployEnvironment     string
	K8sNodeName           string
	K8sPodName            string
	K8sPodNamespace       string
	K8sReplicaSet         string
	K8sServiceAccountName string

	// Temporary resource options whilst migrating execution to environments
	ExecutionServiceMode string
	ExecutionDataSet     string
}

// SpanExporterConfig models a configuration object that can be turned
// into a sdktrace.SpanExporter
type SpanExporterConfig interface {
	Build(context.Context) (sdktrace.SpanExporter, error)
}

// CollectorExportConfig configures spans to be exported to an OpenTelemetry collector
type CollectorExportConfig struct {
	Protocol string

	Secure bool
	Host   string
	Port   int

	HTTPPath        string
	HTTPToken       secret.String
	HTTPServiceName string
	HTTPRevision    string
}

func (cfg CollectorExportConfig) Build(ctx context.Context) (sdktrace.SpanExporter, error) {
	host := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	var client otlptrace.Client
	switch cfg.Protocol {
	case "", "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(host),
			otlptracegrpc.WithServiceConfig(serviceConfig()),
		}
		if !cfg.Secure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		client = otlptracegrpc.NewClient(opts...)
	case "http":
		headers := map[string]string{
			"User-Agent": fmt.Sprintf("backplane-go %s/%s", cfg.HTTPServiceName, cfg.HTTPRevision),
		}
		if cfg.HTTPToken.Value() != "" {
			headers["Authorization"] = "Bearer " + cfg.HTTPToken.Value()
		}
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(host),
			otlptracehttp.WithURLPath(cfg.HTTPPath),
			otlptracehttp.WithHeaders(headers),
		}
		if !cfg.Secure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		client = otlptracehttp.NewClient(opts...)
	default:
		return nil, fmt.Errorf("unsupported OTEL protocol: %q", cfg.Protocol)
	}
	return otlptrace.New(ctx, client)
}

// SpanEmitterExportConfig configures spans to be exported to a SpanEmitter-based
type SpanEmitterExportConfig struct {
	SpanEmitter report.SpanEmitter
}

func (cfg SpanEmitterExportConfig) Build(_ context.Context) (sdktrace.SpanExporter, error) {
	return &exporter{emitter: cfg.SpanEmitter}, nil
}

func NewTracerProvider(ctx context.Context, cfg TracerProviderConfig) (*TracerProvider, error) {
	// Pick fairly standard telemetry processor limits
	limits := sdktrace.NewSpanLimits()
	limits.AttributeCountLimit = 2000
	limits.AttributeValueLengthLimit = 64 * 1024

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(newResource(cfg.Resource)),
		sdktrace.WithRawSpanLimits(limits),
	}

	if cfg.ExecutionTempSpanProcessor != nil {
		opts = append(opts, sdktrace.WithSpanProcessor(cfg.ExecutionTempSpanProcessor))
	}

	for _, b := range cfg.Batchers {
		batcher, err := b.Build(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(batcher))
	}
	for _, s := range cfg.Syncers {
		syncer, err := s.Build(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithSyncer(syncer))
	}

	return &TracerProvider{
		cfg: cfg,
		tp:  sdktrace.NewTracerProvider(opts...),
	}, nil
}

func (t *TracerProvider) TracerProvider() trace.TracerProvider {
	return t.tp
}

func (t *TracerProvider) StartSpan(ctx context.Context, name string, o report.StartOpts) (context.Context, report.WSpan) {
	return t.startSpanShim(ctx, name, o)
}

//nolint:spancheck // this is closed later by our wrapper
func (t *TracerProvider) startSpanShim(ctx context.Context, name string, o report.StartOpts) (context.Context, *spanShim) {
	tracer := t.tp.Tracer("")
	opts := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKind(o.Kind))}
	if o.NewRoot {
		opts = append(opts, trace.WithNewRoot())
	}
	ctx, span := tracer.Start(ctx, name, opts...)
	ss := &spanShim{
		name: name,
		span: span,
		kind: o.Kind,
	}
	return context.WithValue(ctx, spanCtxKey{}, ss), ss
}

func (t *TracerProvider) SpanFromContext(ctx context.Context) report.WSpan {
	ss := spanFromContext(ctx)
	if ss == nil { // make sure we return the nop span
		return &spanShim{span: trace.SpanFromContext(ctx)}
	}
	return ss
}

func (t *TracerProvider) MakeSpanGolden(ctx context.Context) context.Context {
	// Get the existing normal span
	sp := spanFromContext(ctx)
	if sp == nil { // no span to make golden, then do nothing
		return ctx
	}

	// Find (or start) the current golden trace
	ctx, gold := t.findOrStartGoldenTrace(ctx)

	// Start the golden span based on the current span
	gold.ctx, gold.span = t.startSpanShim(gold.ctx, sp.name, report.StartOpts{
		Kind:    sp.kind,
		NewRoot: false,
	})
	gold.span.SetAttrs(map[string]any{metaGolden: true})
	sp.golden = gold.span

	// finish by making sure the baggage contains the new golden span
	return goldenToBaggage(ctx, gold)
}

func (t *TracerProvider) Shutdown(ctx context.Context) error {
	return t.tp.Shutdown(ctx)
}

func newResource(cfg ResourceConfig) *sdkresource.Resource {
	// The resource metadata has been intentionally left empty until we decide
	// how to load this information from a config.
	return sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(cfg.ServiceName),
		semconv.ServiceVersionKey.String(cfg.Revision),
		semconv.DeploymentEnvironmentKey.String(cfg.DeployEnvironment),
		semconv.K8SNodeNameKey.String(cfg.K8sNodeName),
		semconv.K8SPodNameKey.String(cfg.K8sPodName),
		semconv.K8SReplicaSetNameKey.String(cfg.K8sReplicaSet),
		semconv.K8SNamespaceNameKey.String(cfg.K8sPodNamespace),
		attribute.String("k8s.service-account.name", cfg.K8sServiceAccountName),
		attribute.Bool("meta.environments", cfg.UseEnvironments),

		// TODO - drop these once execution is migrated to env
		attribute.String("service.mode", cfg.ExecutionServiceMode),
		attribute.String("x-honeycomb-dataset", cfg.ExecutionDataSet),
	)
}

const goldenTracePrefix = "golden-"

func (t *TracerProvider) findOrStartGoldenTrace(ctx context.Context) (context.Context, *golden) {
	// Use any existing one from the context
	gold := goldenFromContext(ctx)
	if gold != nil {
		return ctx, gold
	}

	// If there is a golden trace in the baggage, then hydrate that into the context and use that.
	ctx, gold = goldenFromBaggage(ctx)
	if gold != nil {
		return ctx, gold
	}

	// No existing trace found, so start one with a clean context for the golden root span.
	tracer := t.tp.Tracer("")
	gCtx, _ := tracer.Start(context.Background(), "root") //nolint:spancheck // TODO - this needs to be fixed
	gold = &golden{
		ctx: gCtx,
	}
	ctx = context.WithValue(ctx, goldenCtxKey{}, gold)

	// Store it in the baggage
	return goldenToBaggage(ctx, gold), gold
}

// goldenToBaggage makes sure the baggage carries any current golden if one exists in the context.
func goldenToBaggage(ctx context.Context, gold *golden) context.Context {
	if gold == nil {
		return ctx
	}
	sm := http.Header{}

	bg := baggage.FromContext(ctx)
	propagation.TraceContext{}.Inject(gold.ctx, propagation.HeaderCarrier(sm))
	for k, v := range sm {
		if len(v) == 0 {
			continue
		}
		m, err := baggage.NewMemberRaw(goldenTracePrefix+k, v[0])
		if err != nil { // any error, just ignore value
			continue
		}
		bg, _ = bg.SetMember(m) // any error, just drop the value
	}
	return baggage.ContextWithBaggage(ctx, bg)
}

// goldenFromBaggage extracts the golden trace from baggage on the context and adds it into the context.
func goldenFromBaggage(ctx context.Context) (context.Context, *golden) {
	bg := baggage.FromContext(ctx)
	// sm will end up containing standard otel trace propagation headers.
	sm := http.Header{}
	// look for the golden prefixed standard propagation keys
	for _, m := range bg.Members() {
		k, ok := strings.CutPrefix(m.Key(), goldenTracePrefix)
		if ok {
			sm.Set(k, m.Value())
		}
	}
	if len(sm) == 0 {
		return ctx, nil
	}
	// populate this gold context with the golden propagation ids
	gCtx := propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(sm))
	gold := &golden{
		ctx: gCtx,
	}
	return context.WithValue(ctx, goldenCtxKey{}, gold), gold
}

func serviceConfig() string {
	cfg, err := json.Marshal(conf{
		LoadBalancingConfig: []loadBalancingConfig{
			{
				roundRobin: roundRobin{},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(cfg)
}

type conf struct {
	LoadBalancingConfig []loadBalancingConfig `json:"loadBalancingConfig"`
}

type loadBalancingConfig struct {
	roundRobin `json:"round_robin"`
}

type roundRobin struct{}
