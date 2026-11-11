package agent

import (
	"context"

	"github.com/zoumo/agent-sdk-go/internal/tracecontext"
)

// TraceContext carries the W3C parent for one prompt submission. It is call
// metadata and is never encoded into Input or unified events.
type TraceContext struct {
	// Traceparent is the W3C traceparent value.
	Traceparent string
	// Tracestate is propagated only with a valid Traceparent.
	Tracestate string
}

// Validate checks the W3C carrier without changing it.
func (trace TraceContext) Validate() error {
	return tracecontext.Validate(trace.Traceparent, trace.Tracestate)
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
