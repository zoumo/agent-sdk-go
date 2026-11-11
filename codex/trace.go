package codex

import (
	"context"

	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/tracecontext"
)

// TraceContext carries one native prompt's W3C request-envelope metadata.
// It is independent of the optional unified agent package.
type TraceContext = types.W3cTraceContext

type traceContextKey struct{}

// WithTraceContext attaches a carrier without validating it. Only Session.Submit
// consumes it; raw Client calls and thread operations remain trace-free.
func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceContextFromContext reads the current carrier without validating it.
// Derived contexts inherit the value; repeated writes keep the latest value.
func TraceContextFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}

// Snapshot only known, valid W3C fields. Native optional null/absent state and
// empty tracestate all mean no tracestate on this request.
func submissionTrace(ctx context.Context) *types.W3cTraceContext {
	trace, ok := TraceContextFromContext(ctx)
	if !ok {
		return nil
	}
	parent, ok := trace.Traceparent.Get()
	state := trace.Tracestate.ValueOr("")
	if !ok || tracecontext.Validate(parent, state) != nil {
		return nil
	}
	snapshot := types.W3cTraceContext{Traceparent: types.Some(parent)}
	if state != "" {
		snapshot.Tracestate = types.Some(state)
	}
	return &snapshot
}
