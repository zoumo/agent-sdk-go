package agent

import (
	"context"
	"errors"
	"strings"
)

// TraceContext carries the W3C parent for one prompt submission. It is call
// metadata and is never encoded into Input or unified events.
type TraceContext struct {
	// Traceparent is the W3C traceparent value.
	Traceparent string
	// Tracestate is propagated only with a valid Traceparent.
	Tracestate string
}

// Validate checks the W3C carrier without changing it. Version 00 has exactly
// 55 bytes; future versions other than ff may append a dash-delimited suffix.
// IDs and flags use lowercase hex and both IDs must be nonzero. Tracestate
// accepts W3C list members, optional surrounding whitespace and unique keys.
func (trace TraceContext) Validate() error {
	parent := trace.Traceparent
	if len(parent) < 55 || !traceASCII(parent, false) ||
		parent[2] != '-' || parent[35] != '-' || parent[52] != '-' ||
		!traceHex(parent[:2]) || parent[:2] == "ff" ||
		!traceHex(parent[3:35]) || parent[3:35] == strings.Repeat("0", 32) ||
		!traceHex(parent[36:52]) || parent[36:52] == strings.Repeat("0", 16) ||
		!traceHex(parent[53:55]) {
		return errors.New("invalid W3C traceparent")
	}
	if len(parent) != 55 && (parent[:2] == "00" || parent[55] != '-') {
		return errors.New("invalid W3C traceparent version length or suffix")
	}
	if !validTracestate(trace.Tracestate) {
		return errors.New("invalid W3C tracestate")
	}
	return nil
}

func traceASCII(value string, whitespace bool) bool {
	for i := range len(value) {
		if whitespace && value[i] == '\t' {
			continue
		}
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func traceHex(value string) bool {
	for i := range len(value) {
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func validTracestate(state string) bool {
	if state == "" {
		return true
	}
	if !traceASCII(state, true) {
		return false
	}
	members := strings.Split(state, ",")
	if len(members) > 32 {
		return false
	}
	keys := make(map[string]bool, len(members))
	for _, member := range members {
		member = strings.Trim(member, " \t")
		// W3C permits empty list members when multiple headers are combined.
		if member == "" {
			continue
		}
		key, value, ok := strings.Cut(member, "=")
		if !ok || !traceKey(key) || keys[key] || len(value) == 0 || len(value) > 256 {
			return false
		}
		for i := range len(value) {
			if value[i] < 0x20 || value[i] > 0x7e || value[i] == '=' {
				return false
			}
		}
		keys[key] = true
	}
	return true
}

func traceKey(key string) bool {
	if tenant, system, multi := strings.Cut(key, "@"); multi {
		return traceKeyPart(tenant, 241, true) && traceKeyPart(system, 14, false)
	}
	return traceKeyPart(key, 256, false)
}

func traceKeyPart(part string, max int, digitStart bool) bool {
	if len(part) == 0 || len(part) > max {
		return false
	}
	alpha := func(c byte) bool { return c >= 'a' && c <= 'z' }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	if !alpha(part[0]) && !(digitStart && digit(part[0])) {
		return false
	}
	for i := range len(part) {
		c := part[i]
		if !alpha(c) && !digit(c) && c != '_' && c != '-' && c != '*' && c != '/' {
			return false
		}
	}
	return true
}

type traceContextKey struct{}

// WithTraceContext attaches the per-call carrier without validating it.
// Repeated calls keep the most recent value.
func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceContextFromContext returns the stored value; presence does not imply
// validity. Derived contexts inherit the value using normal context rules.
func TraceContextFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}

// Tracer creates one local submission span. Implementations must be safe for
// concurrent use and non-blocking. A non-nil parent takes precedence over any
// active span in ctx. With no explicit parent, ctx may supply an active span.
// Returning nil disables the span. SDK calls isolate telemetry panics.
type Tracer interface {
	StartSpan(ctx context.Context, name string, parent *TraceContext) Span
}

// Span covers only the StartOrSteer call, not execution or later events.
// Implementations must be non-blocking; SDK calls isolate telemetry panics.
type Span interface {
	// TraceContext returns a carrier for provider propagation. A zero or invalid
	// carrier falls back to the valid carrier from the submission context.
	TraceContext() TraceContext
	// End receives the returned business error exactly once before return.
	End(err error)
}

// SpanStartOrSteer is the name of the optional SDK-local submission span.
const SpanStartOrSteer = "agent.start_or_steer"
