package submissiontrace_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace"
)

type (
	tracerFunc    func(context.Context, string, *agent.TraceContext) agent.Span
	activeSpanKey struct{}
)

func (f tracerFunc) StartSpan(ctx context.Context, name string, parent *agent.TraceContext) agent.Span {
	return f(ctx, name, parent)
}

type testSpan struct {
	carrier func() agent.TraceContext
	end     func(error)
}

func (s testSpan) TraceContext() agent.TraceContext { return s.carrier() }
func (s testSpan) End(err error)                    { s.end(err) }

func TestSubmissionTraceCarrierSelectionAndPanicIsolation(t *testing.T) {
	parent := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=parent"}
	child := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01", Tracestate: "vendor=child"}
	invalid := agent.TraceContext{Traceparent: "invalid"}
	for _, test := range []struct {
		name              string
		contextCarrier    *agent.TraceContext
		spanCarrier       agent.TraceContext
		panicAt           string
		noTracer, nilSpan bool
		want              *agent.TraceContext
	}{
		{name: "span wins", contextCarrier: &parent, spanCarrier: child, want: &child},
		{name: "no tracer", contextCarrier: &parent, noTracer: true, want: &parent},
		{name: "no carrier", noTracer: true},
		{name: "nil span", contextCarrier: &parent, nilSpan: true, want: &parent},
		{name: "zero span", contextCarrier: &parent, want: &parent},
		{name: "invalid span", contextCarrier: &parent, spanCarrier: invalid, want: &parent},
		{name: "invalid parent", contextCarrier: &invalid, spanCarrier: child, want: &child},
		{name: "no parent", spanCarrier: child, want: &child},
		{name: "invalid both", contextCarrier: &invalid, spanCarrier: invalid},
		{name: "start panic", contextCarrier: &parent, panicAt: "start", want: &parent},
		{name: "carrier panic", contextCarrier: &parent, panicAt: "carrier", want: &parent},
		{name: "end panic", contextCarrier: &parent, spanCarrier: child, panicAt: "end", want: &child},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), activeSpanKey{}, "active-span")
			if test.contextCarrier != nil {
				ctx = agent.WithTraceContext(ctx, *test.contextCarrier)
			}
			starts, ends := 0, 0
			businessErr := errors.New("submission failed")
			var endedWith error
			var tracer agent.Tracer = tracerFunc(func(gotCtx context.Context, name string, gotParent *agent.TraceContext) agent.Span {
				starts++
				assert.Same(t, ctx, gotCtx)
				assert.Equal(t, agent.SpanStartOrSteer, name)
				if test.contextCarrier != nil && test.contextCarrier.Validate() == nil {
					require.NotNil(t, gotParent)
					assert.Equal(t, *test.contextCarrier, *gotParent)
					*gotParent = invalid // Must not corrupt the fallback snapshot.
				} else {
					assert.Nil(t, gotParent)
				}
				if test.panicAt == "start" {
					panic("start")
				}
				if test.nilSpan {
					return nil
				}
				return testSpan{
					carrier: func() agent.TraceContext {
						if test.panicAt == "carrier" {
							panic("carrier")
						}
						return test.spanCarrier
					},
					end: func(err error) {
						ends++
						endedWith = err
						if test.panicAt == "end" {
							panic("end")
						}
					},
				}
			})
			if test.noTracer {
				tracer = nil
			}
			submission := submissiontrace.Start(ctx, tracer)
			assert.Equal(t, test.want, submission.Carrier)
			submission.End(businessErr)
			submission.End(nil)
			if test.noTracer {
				assert.Zero(t, starts)
			} else {
				assert.Equal(t, 1, starts)
			}
			if test.noTracer || test.nilSpan || test.panicAt == "start" {
				assert.Zero(t, ends)
			} else {
				assert.Equal(t, 1, ends)
				assert.Same(t, businessErr, endedWith)
			}
		})
	}
}
