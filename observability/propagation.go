package observability

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"

	"github.com/circleci-functions/toolkit/observability/attr"
)

type detachedTraceKey struct{}

// WithDetachedTrace returns a context that tells ExtractPropagation to extract
// only baggage and ignore trace parent context, so the next span created will
// be a root span.
func WithDetachedTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, detachedTraceKey{}, true)
}

func isDetachedTrace(ctx context.Context) bool {
	v, _ := ctx.Value(detachedTraceKey{}).(bool)
	return v
}

// Carrier is used to carry the propagation information
type Carrier interface {
	Get(key string) string
	Set(key string, value string)
}

type adapter struct {
	Carrier
}

// Keys is included to adapt Carrier to otels TextMapCarrier.
func (a adapter) Keys() []string { return nil }

// ExtractPropagation extracts propagation information from the carrier and sets it in the returned context.
// If the context has been marked with WithDetachedTrace, only baggage is extracted and trace parent
// context is ignored, so the next span will be a root span.
func ExtractPropagation(ctx context.Context, c Carrier) context.Context {
	if isDetachedTrace(ctx) {
		return baggageToWithFields(propagation.Baggage{}.Extract(ctx, &adapter{Carrier: c}))
	}
	return baggageToWithFields(otel.GetTextMapPropagator().Extract(ctx, &adapter{Carrier: c}))
}

// InjectPropagation injects propagation information from the context into the carrier.
func InjectPropagation(ctx context.Context, c Carrier) {
	otel.GetTextMapPropagator().Inject(ctx, &adapter{Carrier: c})
}

func baggageToWithFields(ctx context.Context) context.Context {
	bg := baggage.FromContext(ctx)
	attrs := Fields{}
	// look for the prefixed attributes or user info
	for _, m := range bg.Members() {
		if strings.HasPrefix(m.Key(), "user.") || strings.HasPrefix(m.Key(), attr.Prefix) {
			attrs[m.Key()] = m.Value()
		}
	}
	SetSpanAttributes(ctx, attrs)
	return WithFields(ctx, attrs)
}

// WithBaggage adds the fields to the context, current span, and to the baggage in the context for propagation to
// other processes.
func WithBaggage(ctx context.Context, attrs Fields) context.Context {
	ctx = WithFields(ctx, attrs)
	SetSpanAttributes(ctx, attrs)

	// Create new baggage from the current baggage
	bg, _ := baggage.New(baggage.FromContext(ctx).Members()...)

	// And then merge in the new fields
	for k, v := range attrs {
		s, ok := str(v)
		if !ok {
			continue
		}
		if !allowedBaggage[k] {
			continue
		}
		m, err := baggage.NewMemberRaw(k, s)
		if err != nil { // any error, just ignore value
			continue
		}
		bg, _ = bg.SetMember(m) // any error, just drop the value
	}
	return baggage.ContextWithBaggage(ctx, bg)
}

// str returns a string from v, if v is a string or implements Stringer. In both cases the returned bool is true,
// otherwise it will be false
func str(v any) (string, bool) {
	s, ok := v.(string)
	if ok {
		return s, true
	}
	vs, ok := v.(fmt.Stringer)
	if ok {
		return vs.String(), true
	}
	return "", false
}

var allowedBaggage = map[string]bool{
	"user.id":           true,
	attr.UserID:         true,
	attr.ProjectID:      true,
	attr.ProjectName:    true,
	attr.ProjectType:    true,
	attr.OrgID:          true,
	attr.OrgName:        true,
	attr.GroupID:        true,
	attr.PipelineID:     true,
	attr.WorkflowID:     true,
	attr.WorkflowStatus: true,
	attr.JobID:          true,
	attr.TaskIndex:      true,
	attr.ExternalID:     true,
	attr.ExternalIDType: true,
	attr.Key:            true,
	attr.KeyType:        true,
}
