package telemetry

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
)

// BeginSubmission adds the standard span unless the caller configured a legacy
// tracer. The legacy tracer remains responsible for the span and carrier after
// local admission.
func (t Telemetry) BeginSubmission(ctx context.Context, legacy bool) (context.Context, Observation) {
	observation := t.observe(ctx, StartOrSteer)
	if legacy {
		return ctx, observation
	}
	if parent, ok := agent.TraceContextFromContext(ctx); ok && parent.Validate() == nil {
		observation.parentCarrier = &parent
		observation.ctx = trace.ContextWithRemoteSpanContext(observation.ctx, spanContext(parent))
	}
	return observation.startSpan(ctx)
}

// SubmissionCarrier converts the SDK span context to the wire carrier. A no-op
// provider preserves the valid explicit parent, including its original format.
func (o Observation) SubmissionCarrier() *agent.TraceContext {
	span := trace.SpanContextFromContext(o.ctx)
	if !span.IsValid() {
		return o.parentCarrier
	}
	if parent := o.parentCarrier; parent != nil &&
		span.TraceID().String() == parent.Traceparent[3:35] &&
		span.SpanID().String() == parent.Traceparent[36:52] {
		return parent
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(o.ctx, carrier)
	return &agent.TraceContext{
		Traceparent: carrier.Get("traceparent"),
		Tracestate:  carrier.Get("tracestate"),
	}
}

// The agent carrier has already validated the field layout. Reading its fields
// preserves parents with flag bits or future versions accepted by that contract.
func spanContext(parent agent.TraceContext) trace.SpanContext {
	traceID, _ := trace.TraceIDFromHex(parent.Traceparent[3:35])
	spanID, _ := trace.SpanIDFromHex(parent.Traceparent[36:52])
	flags, _ := strconv.ParseUint(parent.Traceparent[53:55], 16, 8)
	state, _ := trace.ParseTraceState(parent.Tracestate)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(flags),
		TraceState: state,
		Remote:     true,
	})
}
