package observability

type spanConfig struct {
	kind    SpanKind
	newRoot bool
}

// SpanOpt is used to set options on a span
type SpanOpt func(spanConfig) spanConfig

// WithSpanKind sets the SpanKind of a Span.
// This is generally expected to be used by other backplane-go packages, rather than application code
func WithSpanKind(kind SpanKind) SpanOpt {
	return func(cfg spanConfig) spanConfig {
		cfg.kind = kind
		return cfg
	}
}

// SpanKind is the role a Span plays in a Trace.
type SpanKind int

// These are direct copies of the otel values - see them for documentation
const (
	SpanKindInternal SpanKind = 1
	SpanKindServer   SpanKind = 2
	SpanKindClient   SpanKind = 3
	SpanKindProducer SpanKind = 4
	SpanKindConsumer SpanKind = 5
)

func applyOpts(opts ...SpanOpt) spanConfig {
	cfg := spanConfig{}
	for _, opt := range opts {
		cfg = opt(cfg)
	}
	if cfg.kind == 0 {
		cfg.kind = SpanKindInternal
	}
	return cfg
}

// WithNewRoot specifies that the Span should be treated as a root Span. Any
// existing parent span context will be ignored. The Golden trace will be uninterrupted.
func WithNewRoot() SpanOpt {
	return func(cfg spanConfig) spanConfig {
		cfg.newRoot = true
		return cfg
	}
}
