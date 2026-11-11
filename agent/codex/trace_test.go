package agentcodex_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

type traceRecorder struct {
	starts, carriers, ends int
	ctx                    context.Context
	name                   string
	parent                 *agent.TraceContext
	endedWith              error
	panicAt                string
	nilSpan                bool
	carrier                agent.TraceContext
}

func (r *traceRecorder) StartSpan(ctx context.Context, name string, parent *agent.TraceContext) agent.Span {
	r.starts++
	r.ctx, r.name, r.parent = ctx, name, parent
	if r.panicAt == "start" {
		panic("tracer failed")
	}
	if r.nilSpan {
		return nil
	}
	return r
}

func (r *traceRecorder) TraceContext() agent.TraceContext {
	r.carriers++
	if r.panicAt == "carrier" {
		panic("carrier failed")
	}
	return r.carrier
}

func (r *traceRecorder) End(err error) {
	r.ends++
	r.endedWith = err
	if r.panicAt == "end" {
		panic("exporter failed")
	}
}

func TestTraceStartOrSteerSynthetic(t *testing.T) {
	child := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01", Tracestate: "vendor=child"}
	parent := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=parent"}
	for _, test := range []struct {
		childSpan, invalidSpan                                              bool
		name, panicAt                                                       string
		invalidParent, noParent, nilSpan, missingIdentity, nativeNotApplied bool
	}{
		{name: "valid parent"},
		{name: "span carrier wins", childSpan: true},
		{name: "span without parent", childSpan: true, noParent: true},
		{name: "invalid span falls back", invalidSpan: true},
		{name: "invalid span and parent", invalidSpan: true, invalidParent: true},
		{name: "invalid parent", invalidParent: true},
		{name: "active context parent", noParent: true},
		{name: "nil span", nilSpan: true},
		{name: "start panic", panicAt: "start"},
		{name: "carrier panic", panicAt: "carrier"},
		{name: "end panic", panicAt: "end"},
		{name: "business error", missingIdentity: true},
		{name: "native rejection after admission", nativeNotApplied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := append([]string(nil), mockCodexInit...)
			response := `<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`
			if test.missingIdentity {
				response = `<{"id":93,"result":{"turn":{"id":"","items":[],"status":"inProgress"}}}`
			}
			if test.nativeNotApplied {
				response = `<{"id":93,"error":{"code":-32603,"message":"no active turn to steer"}}`
			}
			request := `>{"id":93,"method":"turn/start","params":{"input":[{"text":"hello","type":"text"}],"threadId":"root-a"}`
			if test.childSpan {
				request += `,"trace":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01","tracestate":"vendor=child"}`
			} else if !test.invalidParent && !test.noParent {
				request += `,"trace":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","tracestate":"vendor=parent"}`
			}
			request += `}`
			frames = append(frames,
				`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
				`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
				request,
				response,
				`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
				`<{"id":94,"result":{"status":"unsubscribed"}}`,
			)
			recorder := &traceRecorder{panicAt: test.panicAt, nilSpan: test.nilSpan}
			if test.childSpan {
				recorder.carrier = child
			}
			if test.invalidSpan {
				recorder.carrier = agent.TraceContext{Traceparent: "invalid"}
			}
			provider := newTestProviderWithWrappers(t, prepareCodexPeer(t, frames...), codextypes.InitializeParams{
				ClientInfo:   codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
				Capabilities: codextypes.Some(codextypes.InitializeCapabilities{ExperimentalAPI: codextypes.Some(true)}),
			}, testStreamWrappers{tracer: recorder})
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			assert.True(t, session.Capabilities().Supports(agent.CapabilityPromptTrace))
			require.NoError(t, session.Capabilities().Validate())
			// Invalid input is rejected before telemetry and native dispatch.
			_, err = session.StartOrSteer(ctx, agent.Input{})
			require.Error(t, err)
			assert.Zero(t, recorder.starts)
			submitCtx := ctx
			if !test.noParent {
				carrier := parent
				if test.invalidParent {
					carrier.Traceparent = "invalid"
				}
				submitCtx = agent.WithTraceContext(ctx, carrier)
			}
			receipt, err := session.StartOrSteer(submitCtx, textInput("hello"))
			if test.missingIdentity {
				require.Error(t, err)
				assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
			} else {
				require.NoError(t, err)
				if test.nativeNotApplied {
					assert.Equal(t, agent.InputNotApplied, receipt.Result)
				} else {
					assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
					assert.Equal(t, "turn-a", receipt.Submission)
				}
			}
			assert.Equal(t, 1, recorder.starts)
			assert.Same(t, submitCtx, recorder.ctx)
			assert.Equal(t, agent.SpanStartOrSteer, recorder.name)
			if test.noParent || test.invalidParent {
				assert.Nil(t, recorder.parent)
			} else {
				require.NotNil(t, recorder.parent)
				assert.Equal(t, parent, *recorder.parent)
			}
			if test.nilSpan || test.panicAt == "start" {
				assert.Zero(t, recorder.carriers)
				assert.Zero(t, recorder.ends)
			} else {
				assert.Equal(t, 1, recorder.carriers)
				assert.Equal(t, 1, recorder.ends, "span must end before return")
				assert.Equal(t, err, recorder.endedWith)
			}
			require.NoError(t, session.Close(ctx))
			_, err = session.StartOrSteer(ctx, textInput("closed"))
			require.Error(t, err)
			assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
			assert.Equal(t, 1, recorder.starts)
		})
	}
}

// Reusing a native turn ID proves only the adapter's per-call carrier mapping,
// not provider-side active-turn trace ownership.
func TestPromptTraceConsecutiveCodexSynthetic(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	parent := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=first"}
	other := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01"}
	calls := []struct {
		ctx   context.Context
		trace string
	}{
		{agent.WithTraceContext(ctx, parent), `,"trace":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","tracestate":"vendor=first"}`},
		{ctx, ""},
		{agent.WithTraceContext(ctx, other), `,"trace":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01"}`},
		{agent.WithTraceContext(ctx, agent.TraceContext{Traceparent: "invalid"}), ""},
		{agent.WithTraceContext(ctx, agent.TraceContext{Tracestate: "vendor=only"}), ""},
		{agent.WithTraceContext(ctx, agent.TraceContext{Traceparent: parent.Traceparent, Tracestate: "vendor=one,vendor=two"}), ""},
		{ctx, ""},
	}
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
	)
	for i, call := range calls {
		id := 93 + i
		frames = append(frames,
			fmt.Sprintf(`>{"id":%d,"method":"turn/start","params":{"input":[{"text":"hello","type":"text"}],"threadId":"root-a"}%s}`, id, call.trace),
			fmt.Sprintf(`<{"id":%d,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`, id),
		)
	}
	id := 93 + len(calls)
	frames = append(frames,
		fmt.Sprintf(`>{"id":%d,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`, id),
		fmt.Sprintf(`<{"id":%d,"result":{"status":"unsubscribed"}}`, id),
	)
	session := openStartOrSteerSession(t, ctx, frames...)
	for i, call := range calls {
		receipt, err := session.StartOrSteer(call.ctx, textInput("hello"))
		require.NoError(t, err)
		if i == 0 {
			assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
		} else {
			assert.Equal(t, agent.InputAppliedCurrentWork, receipt.Result)
		}
		assert.Equal(t, "turn-a", receipt.Submission)
	}
}
