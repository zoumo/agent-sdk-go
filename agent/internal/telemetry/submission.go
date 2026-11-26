package telemetry

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace"
)

// Submission observes one StartOrSteer call from entry through return.
type Submission struct {
	observation   Observation
	parentCarrier *agent.TraceContext
	legacy        *submissiontrace.Submission
}

// BeginSubmission starts timing every call and starts the standard span unless
// a legacy tracer is configured. Defer End with the returned receipt and error.
func (t Telemetry) BeginSubmission(ctx context.Context) (context.Context, *Submission) {
	s := &Submission{observation: t.observe(ctx, StartOrSteer)}
	if t.legacyTracer != nil {
		return ctx, s
	}
	s.parentCarrier = submissiontrace.ContextCarrier(ctx)
	if s.parentCarrier != nil {
		s.observation.ctx = trace.ContextWithRemoteSpanContext(s.observation.ctx, spanContext(*s.parentCarrier))
	}
	ctx, s.observation = s.observation.startSpan(ctx)
	return ctx, s
}

// AfterAdmission starts the legacy span before native submission.
func (s *Submission) AfterAdmission(ctx context.Context) {
	if tracer := s.observation.telemetry.legacyTracer; tracer != nil {
		s.legacy = submissiontrace.Start(ctx, tracer)
	}
}

// Carrier returns the selected carrier after admission.
// A no-op provider preserves the valid explicit parent in its original format.
func (s *Submission) Carrier() *agent.TraceContext {
	if s.legacy != nil {
		return s.legacy.Carrier
	}
	span := trace.SpanContextFromContext(s.observation.ctx)
	if !span.IsValid() {
		return s.parentCarrier
	}
	if parent := s.parentCarrier; parent != nil &&
		span.TraceID().String() == parent.Traceparent[3:35] &&
		span.SpanID().String() == parent.Traceparent[36:52] {
		return parent
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(s.observation.ctx, carrier)
	return &agent.TraceContext{
		Traceparent: carrier.Get("traceparent"),
		Tracestate:  carrier.Get("tracestate"),
	}
}

func (s *Submission) End(receipt agent.InputReceipt, err error) {
	if s.legacy != nil {
		s.legacy.End(err)
	}
	s.observation.End(string(receipt.Result), err)
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
