package agentclaude_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

type traceRecorder struct {
	starts, carriers, ends int
	ctx                    context.Context
	name                   string
	parent                 *agent.TraceContext
	endedWith              error
	panicAt                string
	nilSpan                bool
	cancel                 context.CancelFunc
	carrier                agent.TraceContext
}

func (r *traceRecorder) StartSpan(ctx context.Context, name string, parent *agent.TraceContext) agent.Span {
	r.starts++
	r.ctx, r.name, r.parent = ctx, name, parent
	if r.cancel != nil {
		r.cancel()
	}
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
	parent := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=parent"}
	for _, test := range []struct {
		name, panicAt                                                    string
		invalidParent, noParent, nilSpan, cancelSubmission, childCarrier bool
	}{
		{name: "valid parent"},
		{name: "span child takes precedence", childCarrier: true},
		{name: "invalid parent", invalidParent: true},
		{name: "active context parent", noParent: true},
		{name: "nil span", nilSpan: true},
		{name: "start panic", panicAt: "start"},
		{name: "carrier panic", panicAt: "carrier"},
		{name: "end panic", panicAt: "end"},
		{name: "business error", cancelSubmission: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, claudeSessionInitFrame(t, "native-trace", cwd))
			if !test.cancelSubmission && !test.invalidParent && !test.noParent {
				carrier := parent
				if test.childCarrier {
					carrier = agent.TraceContext{Traceparent: parent.Traceparent, Tracestate: "vendor=child"}
				}
				frames = append(frames, claudePromptTraceFrames(t, "go_1", carrier, false)...)
			}
			if !test.cancelSubmission {
				frames = append(frames, claudeStartOrSteerFrame(t, "hello"))
			}
			recorder := &traceRecorder{panicAt: test.panicAt, nilSpan: test.nilSpan}
			if test.childCarrier {
				recorder.carrier = agent.TraceContext{Traceparent: parent.Traceparent, Tracestate: "vendor=child"}
			}
			provider := newTestClaudeProviderWithConfig(t, agentclaude.Config{Tracer: recorder}, prepareClaudePeer(t, "passed", frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			require.NoError(t, err)
			assert.True(t, session.Capabilities().Supports(agent.CapabilityPromptTrace))
			t.Cleanup(func() { assert.NoError(t, session.Close(context.Background())) })
			_, err = session.StartOrSteer(ctx, agent.Input{})
			require.Error(t, err)
			assert.Zero(t, recorder.starts)
			submitCtx, cancelSubmit := context.WithCancel(ctx)
			defer cancelSubmit()
			if !test.noParent {
				carrier := parent
				if test.invalidParent {
					carrier.Traceparent = "invalid"
				}
				submitCtx = agent.WithTraceContext(submitCtx, carrier)
			}
			if test.cancelSubmission {
				recorder.cancel = cancelSubmit
			}
			receipt, err := session.StartOrSteer(submitCtx, claudeTextInput("hello"))
			if test.cancelSubmission {
				require.Error(t, err)
				assert.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err)
				assert.Equal(t, agent.InputAppliedBestEffort, receipt.Result)
				assert.NotEmpty(t, receipt.Submission)
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.True(t, *receipt.Acceptance.Sent)
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
			_, err = session.StartOrSteer(ctx, claudeTextInput("closed"))
			require.Error(t, err)
			assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
			assert.Equal(t, 1, recorder.starts)
		})
	}
}

func claudePromptTraceFrames(t *testing.T, id string, trace agent.TraceContext, rejected bool) []string {
	t.Helper()
	request := `>{"type":"control_request","request_id":` + jsonString(t, id) + `,"request":{"subtype":"apply_flag_settings","settings":{"env":{"TRACEPARENT":` + jsonString(t, trace.Traceparent) + `,"TRACESTATE":` + jsonString(t, trace.Tracestate) + `}}}}`
	response := `<{"type":"control_response","response":{"subtype":"success","request_id":` + jsonString(t, id) + `,"response":{}}}`
	if rejected {
		response = `<{"type":"control_response","response":{"subtype":"error","request_id":` + jsonString(t, id) + `,"error":"synthetic trace rejection"}}`
	}
	return []string{request, response}
}

func TestTracePromptSyntheticBestEffortAndRapidInputs(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejection"}[rejected], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			first := agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=first"}
			second := agent.TraceContext{Traceparent: first.Traceparent}
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, claudeSessionInitFrame(t, "native-rapid-trace", cwd))
			for i, trace := range []agent.TraceContext{first, second, {}} {
				frames = append(frames, claudePromptTraceFrames(t, fmt.Sprintf("go_%d", i+1), trace, rejected && i == 0)...)
				frames = append(frames, claudeStartOrSteerFrame(t, fmt.Sprintf("prompt-%d", i)))
			}
			provider := newTestClaudeProviderWithConfig(t, agentclaude.Config{}, prepareClaudePeer(t, "passed", frames...))
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			for i, trace := range []agent.TraceContext{first, second, {}} {
				submitCtx := ctx
				if i < 2 {
					submitCtx = agent.WithTraceContext(ctx, trace)
				}
				receipt, err := session.StartOrSteer(submitCtx, claudeTextInput(fmt.Sprintf("prompt-%d", i)))
				require.NoError(t, err)
				assert.Equal(t, agent.InputAppliedBestEffort, receipt.Result)
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.True(t, *receipt.Acceptance.Sent)
				require.NoError(t, session.Err())
			}
			require.NoError(t, session.Close(ctx))
		})
	}
}
