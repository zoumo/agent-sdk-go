package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInjectPreservesIdleAndActiveInputRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"context"},{"type":"input_text","text":"second block"}]}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":95,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"more"}]}],"threadId":"root-a"}}`,
		`<{"id":95,"result":{}}`,
		`>{"id":96,"method":"turn/start","params":{"input":[{"text":"second","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":96,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":97,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":97,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)
	injector, ok := session.(agent.Injector)
	require.True(t, ok)
	input := textInput("context")
	input.Content = append(input.Content, agent.ContentBlock{Text: &agent.ContentBlockText{Text: "second block"}})
	receipt, err := injector.Inject(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, agent.Acceptance{Sent: new(true), NativeAccepted: new(true)}, receipt.Acceptance)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, interrupted.Result)

	first, err := session.StartOrSteer(ctx, textInput("first"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedNewWork, first.Result)
	_, err = injector.Inject(ctx, textInput("more"))
	require.NoError(t, err)
	second, err := session.StartOrSteer(ctx, textInput("second"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, second.Result)
	assert.Equal(t, first.Submission, second.Submission)
}

func TestInjectRejectsUnsupportedMethodWithoutPromptFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, test := range []struct {
		name string
		code string
		want agent.ErrorCode
	}{
		{"method not found", "-32601", agent.ErrorUnsupported},
		{"native rejection", "-32602", agent.ErrorNativeRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
				`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
				`>{"id":93,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"context"}]}],"threadId":"root-a"}}`,
				`<{"id":93,"error":{"code":`+test.code+`,"message":"synthetic rejection"}}`,
				`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
				`<{"id":94,"result":{"status":"unsubscribed"}}`,
			)
			session := openStartOrSteerSession(t, ctx, frames...)
			receipt, err := session.(agent.Injector).Inject(ctx, textInput("context"))
			require.Error(t, err)
			assert.Equal(t, test.want, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(true), NativeAccepted: new(false)}, receipt.Acceptance)
		})
	}
}

func TestInjectRejectsLocalInvalidCanceledAndClosedInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)
	injector := session.(agent.Injector)
	receipt, err := injector.Inject(ctx, agent.Input{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	annotated := textInput("annotated")
	annotated.Content[0].Text.Annotations = &agent.Annotations{}
	receipt, err = injector.Inject(ctx, annotated)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	canceled, stop := context.WithCancel(ctx)
	stop()
	receipt, err = injector.Inject(canceled, textInput("canceled"))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	require.NoError(t, session.Close(ctx))
	receipt, err = injector.Inject(ctx, textInput("closed"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
}

func TestInjectUnknownOutcomeDoesNotRetryOrCreateWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"context"}]}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peer := prepareCodexPeerWithFrameDelays(t, "passed", map[int]time.Duration{6: 800 * time.Millisecond}, frames...)
	session := openStartOrSteerSessionWithPeer(t, ctx, peer)
	unknown, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	_, err := session.(agent.Injector).Inject(unknown, textInput("context"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, interrupted.Result)
}

func TestInjectDoesNotAttributeUnrelatedTurnDuringNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"query","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed"}}}`,
		`>{"id":94,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"context"}]}],"threadId":"root-a"}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"unrelated","itemId":"unrelated-item","delta":"unrelated"}}`,
	)
	responseIndex := len(frames)
	frames = append(frames,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peer := prepareCodexPeerWithFrameDelays(t, "passed",
		map[int]time.Duration{responseIndex: 800 * time.Millisecond}, frames...)
	session := openStartOrSteerSessionWithPeer(t, ctx, peer)
	stream, err := session.Events()
	require.NoError(t, err)
	_, err = session.StartOrSteer(ctx, textInput("query"))
	require.NoError(t, err)
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))

	injected := make(chan error, 1)
	go func() {
		_, injectErr := session.(agent.Injector).Inject(ctx, textInput("context"))
		injected <- injectErr
	}()
	event := nextEvent(t, ctx, stream)
	assert.Equal(t, agent.EventSessionError, event.Type())
	assert.Equal(t, "stale_native_turn", sessionErrorReason(t, event))
	require.NoError(t, <-injected)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, interrupted.Result)
}
