package otel

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/internal/semconv"
	bpattr "github.com/circleci-functions/toolkit/observability/attr"
)

type spanShim struct {
	name   string
	span   trace.Span
	golden *spanShim

	mu           sync.RWMutex
	timingName   string
	timingFields []string

	// For copying over to golden span, and for timing metric from span
	kind        report.SpanKind
	attrs       map[string]any
	status      codes.Code
	description string
}

const nameAttrKey = "name"

func (s *spanShim) SetAttrs(attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make sure the special 'name' attribute updates the span name.
	for k, v := range attrs {
		if k == nameAttrKey {
			if v, ok := v.(string); ok {
				s.span.SetName(v)
			}
		}
	}
	delete(attrs, nameAttrKey)

	s.span.SetAttributes(mapToAttributes(attrs)...)

	// TODO - remove this once we're not using the legacy attrs
	extra := toAndFromLegacyAttrs(attrs)
	s.span.SetAttributes(mapToAttributes(extra)...)

	if s.attrs == nil {
		s.attrs = make(map[string]any)
	}

	for k, v := range attrs {
		// Append to the internal info special field.
		if k == bpattr.InternalInfo {
			xv, ok := s.attrs[k]
			if !ok {
				xv = stringerMap{}
			}
			v = mergeInfoField(xv, v)
		}
		s.attrs[k] = v
	}
}

func asMap(a any) (map[string]any, bool) {
	m, ok := a.(interface{ AsMap() map[string]any })
	if ok {
		return m.AsMap(), ok
	}
	if v, ok := a.(map[string]any); ok {
		return v, ok
	}
	return nil, false
}

func (s *spanShim) SetStatusOk() {
	s.span.SetStatus(codes.Ok, "")

	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = codes.Ok
}

func (s *spanShim) SetStatusError(desc string) {
	s.span.SetStatus(codes.Error, desc)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = codes.Error
	s.description = desc
}

func (s *spanShim) End() {
	// Use the stored up merged internal info field to set this attribute on the real span.
	s.mu.Lock()
	if v, ok := s.attrs[bpattr.InternalInfo]; ok {
		s.span.SetAttributes(attr(bpattr.InternalInfo, v))
	}
	s.mu.Unlock()

	s.span.End()
	if s.golden != nil {
		s.golden.copy(s)
		s.golden.End()
	}
}

func (s *spanShim) SetTimingAttrs(name string, attrs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name != "" {
		// reject a second call to SetTimingAttrs within the same span
		if s.timingName != "" {
			return
		}
		s.timingName = name
	}
	s.timingFields = append(s.timingFields, attrs...)
}

func (s *spanShim) TimingAttrs() (string, map[string]any) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.timingName == "" {
		return "", nil
	}
	m := make(map[string]any, len(s.timingFields))
	for _, k := range s.timingFields {
		if k == "name" { // The special attribute that means use the span name
			m["span.name"] = s.name // need to avoid conflict with the generic name key
			continue
		}
		m[k] = s.attrs[k]
	}
	// Note - we can't use detail or message - since they are high cardinality attributes
	result := "success"
	if et, ok := s.attrs[semconv.ErrorType]; ok {
		result = "warning"
		m[semconv.ErrorType] = et
	}
	if _, ok := s.attrs[semconv.ErrorDetail]; ok {
		result = "warning"
	}
	if s.status == codes.Error {
		result = "error"
	}
	m["result"] = result

	return s.timingName, m
}

func (s *spanShim) copy(sp *spanShim) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	s.span.SetAttributes(mapToAttributes(sp.attrs)...)
	s.span.SetStatus(sp.status, sp.description)
}

func mapToAttributes(m map[string]any) []attribute.KeyValue {
	attributes := make([]attribute.KeyValue, 0, len(m))
	for k, v := range m {
		attributes = append(attributes, attr(k, v))
	}
	return attributes
}

type spanCtxKey struct{}

func spanFromContext(ctx context.Context) *spanShim {
	ss, ok := ctx.Value(spanCtxKey{}).(*spanShim)
	if !ok {
		return nil
	}
	return ss
}

// golden is the struct holding the golden root span and the context that holds the matching otel trace context
type golden struct {
	ctx  context.Context
	span *spanShim
}

const metaGolden = "meta.golden"

type goldenCtxKey struct{}

// getGolden returns the active golden context holder in the given context.
// It will return nil if there is no span available.
func goldenFromContext(ctx context.Context) *golden {
	if s, ok := ctx.Value(goldenCtxKey{}).(*golden); ok {
		return s
	}
	return nil
}

// stringerMap exists to marshal the info fields in the same way that o11.Fields does, and to implement
// AsMap so we can merge in with o11y.Fields.
// This is part of the transition to a stricter set of approved fields.
type stringerMap map[string]any

func (f stringerMap) String() string {
	if f == nil {
		return ""
	}
	b, _ := json.Marshal(f)
	return string(b)
}

func (f stringerMap) AsMap() map[string]any {
	return f
}

func toAndFromLegacyAttrs(attrs map[string]any) (all map[string]any) {
	all = make(map[string]any, len(attrs))
	for k, v := range attrs {
		// first add in legacy keys
		legacyKey := strings.TrimPrefix(k, bpattr.Prefix)
		// Only mutate keys that do have the prefix
		if legacyKey != k {
			legacyKey = strings.Replace(legacyKey, ".id", "_id", 1)
			legacyKey = strings.Replace(legacyKey, ".name", "_name", 1)
			// Only add legacy keys that we are mapping.
			if _, ok := toMap[legacyKey]; ok {
				all[legacyKey] = v
			}
		}
		// Add in the new keys from old, if they are ones we want to map.
		if newKey, ok := toMap[k]; ok {
			all[newKey] = v
		}
	}
	return all
}

func mergeInfoField(x, v any) any {
	xm, ok := asMap(x)
	if !ok {
		return v
	}
	vm, ok := asMap(v)
	if !ok {
		return x
	}
	for k, v := range xm {
		vm[k] = v
	}
	return stringerMap(vm)
}

var toMap = map[string]string{
	"user_id":      bpattr.UserID,
	"project_id":   bpattr.ProjectID,
	"project_name": bpattr.ProjectName,
	"org_id":       bpattr.OrgID,
	"org_name":     bpattr.OrgName,
	"pipeline_id":  bpattr.PipelineID,
	"workflow_id":  bpattr.WorkflowID,
	"job_id":       bpattr.JobID,

	// These are keys that work in pairs, and are a new experiment. It is unlikely that they will be used in the same
	// way currently.
	// bpattr.ExternalID:     true,
	// bpattr.ExternalIDType: true,
	// bpattr.Key:            true,
	// bpattr.KeyType:        true,
}
