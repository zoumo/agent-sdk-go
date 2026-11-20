// Package telemetry instruments the public operations of the unified adapters.
package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
)

const scope = "github.com/zoumo/agent-sdk-go/agent"

type Operation string

const (
	Start        Operation = "core.start"
	Open         Operation = "core.open"
	Resume       Operation = "core.resume"
	Stop         Operation = "core.stop"
	Close        Operation = "session.close"
	StartOrSteer Operation = "start_or_steer"
	Inject       Operation = "session.inject"
	Answer       Operation = "session.answer"
	Interrupt    Operation = "session.interrupt"
)

type Telemetry struct {
	provider attribute.KeyValue
	tracer   trace.Tracer
	duration metric.Float64Histogram
}

func New(provider string, traces trace.TracerProvider, meters metric.MeterProvider) Telemetry {
	if traces == nil {
		traces = otel.GetTracerProvider()
	}
	if meters == nil {
		meters = otel.GetMeterProvider()
	}
	duration, _ := meters.Meter(scope).Float64Histogram(
		"agent.sdk.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of an Agent SDK operation"),
	)
	return Telemetry{
		provider: attribute.String("provider", provider),
		tracer:   traces.Tracer(scope),
		duration: duration,
	}
}

type Observation struct {
	telemetry     Telemetry
	operation     attribute.KeyValue
	started       time.Time
	ctx           context.Context
	span          trace.Span
	parentCarrier *agent.TraceContext
}

func (t Telemetry) Begin(ctx context.Context, operation Operation) (context.Context, Observation) {
	return t.observe(ctx, operation).startSpan(ctx)
}

func (t Telemetry) observe(ctx context.Context, operation Operation) Observation {
	if ctx == nil {
		ctx = context.Background()
	}
	return Observation{
		telemetry: t,
		operation: attribute.String("operation", string(operation)),
		started:   time.Now(),
		ctx:       ctx,
	}
}

func (o Observation) startSpan(ctx context.Context) (context.Context, Observation) {
	o.ctx, o.span = o.telemetry.tracer.Start(o.ctx, "agent."+o.operation.Value.AsString(),
		trace.WithTimestamp(o.started),
		trace.WithAttributes(o.telemetry.provider, o.operation),
	)
	// Some entry points validate nil contexts as part of their public contract.
	if ctx != nil {
		ctx = o.ctx
	}
	return ctx, o
}

func (o Observation) EndAcceptance(acceptance agent.Acceptance, err error) {
	var outcome string
	if err == nil {
		switch {
		case isTrue(acceptance.NativeRecorded):
			outcome = "native_recorded"
		case isTrue(acceptance.NativeAccepted):
			outcome = "native_accepted"
		case acceptance.NativeAccepted != nil:
			outcome = "not_applied"
		case isTrue(acceptance.Sent):
			outcome = "sent"
		case acceptance.Sent != nil:
			outcome = "not_applied"
		default:
			outcome = "outcome_unknown"
		}
	}
	o.End(outcome, err)
}

func isTrue(value *bool) bool {
	return value != nil && *value
}

func (o Observation) EndLifecycle(reused bool, err error) {
	var outcome string
	if reused && err == nil {
		outcome = "reused"
	}
	o.End(outcome, err)
}

// End uses a supplied receipt outcome or classifies the returned error.
func (o Observation) End(outcome string, err error) {
	finished := time.Now()
	if outcome == "" {
		outcome = classify(err)
	}
	result := attribute.String("outcome", outcome)
	if o.span != nil {
		o.span.SetAttributes(result)
		if err != nil {
			o.span.SetAttributes(attribute.String("error.type", errorType(err)))
			o.span.SetStatus(codes.Error, "")
		}
		o.span.End(trace.WithTimestamp(finished))
	}
	if o.telemetry.duration != nil {
		o.telemetry.duration.Record(o.ctx, finished.Sub(o.started).Seconds(),
			metric.WithAttributes(o.telemetry.provider, o.operation, result))
	}
}

func classify(err error) string {
	switch {
	case err == nil:
		return "success"
	case agent.CodeOf(err) == agent.ErrorOutcomeUnknown:
		return "outcome_unknown"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	switch agent.CodeOf(err) {
	case agent.ErrorInvalidArgument, agent.ErrorFailedPrecondition, agent.ErrorUnsupported,
		agent.ErrorResourceExhausted, agent.ErrorNativeRejected, agent.ErrorClosed:
		return "rejected"
	default:
		return "error"
	}
}

func errorType(err error) string {
	switch code := agent.CodeOf(err); code {
	case agent.ErrorInvalidArgument, agent.ErrorFailedPrecondition, agent.ErrorUnsupported,
		agent.ErrorUnavailable, agent.ErrorResourceExhausted, agent.ErrorOutcomeUnknown,
		agent.ErrorNativeRejected, agent.ErrorClosed:
		return string(code)
	default:
		return classify(err)
	}
}
