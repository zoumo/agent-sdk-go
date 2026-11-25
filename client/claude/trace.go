// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"

	"github.com/zoumo/agent-sdk-go/internal/tracecontext"
)

// TraceContext carries the W3C parent for one native prompt submission.
// It is call metadata, independent of user content and process configuration.
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
