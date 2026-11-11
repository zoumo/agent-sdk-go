// Package submissiontrace owns the best-effort span lifecycle of one admitted
// unified submission. Provider wire mapping remains in each adapter.
package submissiontrace

import (
	"context"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
)

// Submission retains only per-call telemetry. Carrier is the valid span carrier
// when available, otherwise the valid context carrier, or nil.
type Submission struct {
	Carrier *agent.TraceContext
	span    agent.Span
	endOnce sync.Once
}

// Start snapshots the context carrier and starts at most one local span. Call
// this after local admission, before native submission, and defer End.
func Start(ctx context.Context, tracer agent.Tracer) *Submission {
	submission := &Submission{}
	if carrier, ok := agent.TraceContextFromContext(ctx); ok && carrier.Validate() == nil {
		submission.Carrier = &carrier
	}
	if tracer == nil {
		return submission
	}
	// Keep a separate parent copy: tracer implementations do not own the
	// fallback carrier snapshot and may retain or modify their parent argument.
	var parent *agent.TraceContext
	if submission.Carrier != nil {
		value := *submission.Carrier
		parent = &value
	}
	submission.span = startSpan(ctx, tracer, parent)
	if submission.span != nil {
		if carrier := spanCarrier(submission.span); carrier.Validate() == nil {
			submission.Carrier = &carrier
		}
	}
	return submission
}

func startSpan(ctx context.Context, tracer agent.Tracer, parent *agent.TraceContext) (span agent.Span) {
	defer func() { _ = recover() }()
	return tracer.StartSpan(ctx, agent.SpanStartOrSteer, parent)
}

func spanCarrier(span agent.Span) (carrier agent.TraceContext) {
	defer func() { _ = recover() }()
	return span.TraceContext()
}

// End isolates exporter panics and ends each non-nil span at most once.
func (submission *Submission) End(err error) {
	submission.endOnce.Do(func() {
		if submission.span != nil {
			defer func() { _ = recover() }()
			submission.span.End(err)
		}
	})
}
