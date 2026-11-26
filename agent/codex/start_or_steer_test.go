package agentcodex_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
)

func TestStartOrSteerStartsNewWorkWhenIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	receipt, err := session.StartOrSteer(ctx, textInput("first"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	assert.Equal(t, "turn-a", receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)
}

func TestInjectAndStartOrSteerSendStructuredInputInOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/inject_items","params":{"threadId":"root-a","items":[{"type":"message","role":"user","content":[`+
			`{"type":"input_text","text":"before"},`+
			`{"type":"input_text","text":"https://example.test/guides%2Fsetup?q=one#start"},`+
			`{"type":"input_text","text":"file:///notes"},`+
			`{"type":"input_image","image_url":"data:image/png;base64,aGk=","detail":"high"},`+
			`{"type":"input_audio","audio_url":"data:audio/wav;base64,aGk="},`+
			`{"type":"input_image","image_url":"https://example.test/image.png","detail":"high"},`+
			`{"type":"input_text","text":"http://example.test/docs"},`+
			`{"type":"input_text","text":"after"}]}]}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"turn/start","params":{"threadId":"root-a","input":[`+
			`{"type":"text","text":"before"},`+
			`{"type":"text","text":"https://example.test/guides%2Fsetup?q=one#start"},`+
			`{"type":"text","text":"file:///notes"},`+
			`{"type":"image","url":"data:image/png;base64,aGk="},`+
			`{"type":"audio","url":"data:audio/wav;base64,aGk="},`+
			`{"type":"image","url":"https://example.test/image.png"},`+
			`{"type":"text","text":"http://example.test/docs"},`+
			`{"type":"text","text":"after"}]}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)
	injector := session.(agent.Injector)
	assert.True(t, session.Capabilities().Supports(agent.CapabilityImageInput))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityAudioInput))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityResourceLinkInput))
	assert.False(t, session.Capabilities().Supports(agent.CapabilityResourceInput))

	imageMIME, pdfMIME, htmlMIME := "image/png", "application/pdf", "text/html; charset=utf-8"
	sourceURI := "file:///attachment.png"
	for _, link := range []*agent.ContentBlockResourceLink{
		{URI: sourceURI, Name: "local image", MIMEType: &imageMIME},
		{URI: "https://example.test/report.pdf", Name: "report", MIMEType: &pdfMIME},
	} {
		blocked := agent.Input{Content: []agent.ContentBlock{
			{Text: &agent.ContentBlockText{Text: "must not be sent"}},
			{ResourceLink: link},
		}}
		_, err := session.StartOrSteer(ctx, blocked)
		require.Error(t, err, "URI %s", link.URI)
		assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
		rejected, err := injector.Inject(ctx, blocked)
		require.Error(t, err, "URI %s", link.URI)
		assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
		assert.Equal(t, agent.Acceptance{Sent: new(false)}, rejected.Acceptance)
	}

	prompt := agent.Input{Content: []agent.ContentBlock{
		{Text: &agent.ContentBlockText{Text: "before"}},
		{ResourceLink: &agent.ContentBlockResourceLink{
			URI: "https://example.test/guides%2Fsetup?q=one#start", Name: "setup",
		}},
		{ResourceLink: &agent.ContentBlockResourceLink{URI: "file:///notes", Name: "notes"}},
		{Image: &agent.ContentBlockImage{Data: "aG\r\nk=", MIMEType: imageMIME, URI: &sourceURI}},
		{Audio: &agent.ContentBlockAudio{Data: "aGk=", MIMEType: "audio/wav"}},
		{ResourceLink: &agent.ContentBlockResourceLink{
			URI: "https://example.test/image.png", Name: "image", MIMEType: &imageMIME,
		}},
		{ResourceLink: &agent.ContentBlockResourceLink{
			URI: "http://example.test/docs", Name: "docs", MIMEType: &htmlMIME,
		}},
		{Text: &agent.ContentBlockText{Text: "after"}},
	}}
	injected, err := injector.Inject(ctx, prompt)
	require.NoError(t, err)
	assert.Equal(t, agent.Acceptance{Sent: new(true), NativeAccepted: new(true)}, injected.Acceptance)
	interrupted, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, interrupted.Result)
	receipt, err := session.StartOrSteer(ctx, prompt)
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
	assert.Equal(t, "turn-a", receipt.Submission)
}

func TestStartOrSteerJoinsActiveRegularTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"adjust","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	first, err := session.StartOrSteer(ctx, textInput("first"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, first.Result)

	second, err := session.StartOrSteer(ctx, textInput("adjust"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, second.Result)
	assert.Equal(t, first.Submission, second.Submission)
	require.NotNil(t, second.Acceptance.Sent)
	require.NotNil(t, second.Acceptance.NativeAccepted)
	assert.True(t, *second.Acceptance.Sent)
	assert.True(t, *second.Acceptance.NativeAccepted)
}

func TestStartOrSteerReportsNotAppliedForNonSteerableTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"review","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-review","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"blocked","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"error":{"code":-32603,"message":"failed to submit turn input: ActiveTurnNotSteerable { turn_kind: Review }","data":{"message":"cannot steer a review turn","codexErrorInfo":{"activeTurnNotSteerable":{"turnKind":"review"}}}}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	first, err := session.StartOrSteer(ctx, textInput("review"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, first.Result)

	receipt, err := session.StartOrSteer(ctx, textInput("blocked"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, receipt.Result)
	assert.Empty(t, receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)
}

func TestStartOrSteerReportsNewWorkWhenResponseTurnDiffers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"later","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-b","items":[],"status":"inProgress"}}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	first, err := session.StartOrSteer(ctx, textInput("first"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, first.Result)
	require.Equal(t, "turn-a", first.Submission)

	later, err := session.StartOrSteer(ctx, textInput("later"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedNewWork, later.Result)
	assert.Equal(t, "turn-b", later.Submission)
	assert.NotEqual(t, first.Submission, later.Submission)
	require.NotNil(t, later.Acceptance.Sent)
	require.NotNil(t, later.Acceptance.NativeAccepted)
	assert.True(t, *later.Acceptance.Sent)
	assert.True(t, *later.Acceptance.NativeAccepted)
	assert.Nil(t, later.Acceptance.NativeRecorded)
}

func TestStartOrSteerUnknownWriteBlocksLaterInputWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"maybe","type":"text"}],"threadId":"root-a"}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"early","delta":"working"}}`,
		codexTurnStartedFrame,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"resume","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-a")...)
	var delta, started *gatedFrameReader
	session, stream := openControlledInteractionSession(t, ctx, frames, testStreamWrappers{
		reader: func(reader io.ReadCloser) io.ReadCloser {
			delta = newGatedFrameReader(reader, []byte("item/agentMessage/delta"))
			started = newGatedFrameReader(delta, []byte("turn/started"))
			return started
		},
	})
	t.Cleanup(started.releaseOnce)
	t.Cleanup(delta.releaseOnce)

	unknownCtx, stopUnknown := context.WithCancel(ctx)
	defer stopUnknown()
	done := make(chan error, 1)
	go func() {
		_, err := session.StartOrSteer(unknownCtx, textInput("maybe"))
		done <- err
	}()
	awaitSignal(t, delta.entered, "delta after native turn/start write")
	stopUnknown()
	require.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(<-done))
	delta.releaseOnce()
	event := nextEvent(t, ctx, stream)
	require.Equal(t, agent.EventAgentMessageChunk, event.Type())
	assert.Equal(t, agent.MessageChunk{
		MessageID: "early",
		Content:   []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "working"}}},
	}, event.Payload)

	receipt, err := session.StartOrSteer(ctx, textInput("must not be retried"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputReceipt{
		Result: agent.InputNotApplied, Acceptance: agent.Acceptance{Sent: new(false)},
	}, receipt)
	injected, err := session.(agent.Injector).Inject(ctx, textInput("must not be injected"))
	require.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, injected.Acceptance)

	started.releaseOnce()
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	resumed, err := session.StartOrSteer(ctx, textInput("resume"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, resumed.Result)
	assert.Equal(t, "turn-a", resumed.Submission)
}

func TestStartOrSteerRejectsOverlapWithoutSecondNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000000}}}`,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeerWithFrameDelays(
		t,
		"passed",
		map[int]time.Duration{7: time.Second},
		frames...,
	)
	session := openStartOrSteerSessionWithPeer(t, ctx, peerSession)
	stream, err := session.Events()
	require.NoError(t, err)

	type firstResult struct {
		receipt agent.InputReceipt
		err     error
	}
	firstDone := make(chan firstResult, 1)
	go func() {
		receipt, err := session.StartOrSteer(ctx, textInput("first"))
		firstDone <- firstResult{receipt: receipt, err: err}
	}()

	// The peer emits turn/started before its delayed turn/start response, so
	// receiving the state event proves the first native write is still in flight.
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	overlap, overlapErr := session.StartOrSteer(ctx, textInput("overlap"))
	require.NoError(t, overlapErr)
	require.Equal(t, agent.InputNotApplied, overlap.Result)
	assert.Empty(t, overlap.Submission)
	require.NotNil(t, overlap.Acceptance.Sent)
	assert.False(t, *overlap.Acceptance.Sent)

	first := <-firstDone
	require.NoError(t, first.err)
	assert.Equal(t, agent.InputAppliedNewWork, first.receipt.Result)
	assert.Equal(t, "turn-a", first.receipt.Submission)
}

func TestStartOrSteerLateResponsePreservesObservedIdle(t *testing.T) {
	testStartOrSteerAfterTerminalEvent(t,
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`, "")
}

func TestStartOrSteerLateResponsePreservesObservedCompletion(t *testing.T) {
	testStartOrSteerAfterTerminalEvent(t,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed"}}}`, "completed")
}

func testStartOrSteerAfterTerminalEvent(t *testing.T, terminalFrame, stopReason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
		terminalFrame,
		`<{"id":93,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000000}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	var gate *gatedFrameReader
	session, stream := openControlledInteractionSession(t, ctx, frames, testStreamWrappers{
		reader: func(reader io.ReadCloser) io.ReadCloser {
			gate = newGatedFrameReader(reader, []byte(`"startedAt"`))
			return gate
		},
	})
	t.Cleanup(gate.releaseOnce)

	type result struct {
		receipt agent.InputReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		receipt, err := session.StartOrSteer(ctx, textInput("first"))
		done <- result{receipt: receipt, err: err}
	}()
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	terminal := nextStatePayload(t, ctx, stream)
	require.Equal(t, new(agent.StateIdle), terminal.State)
	assert.Equal(t, stopReason, terminal.StopReason)
	awaitSignal(t, gate.entered, "turn/start response")
	gate.releaseOnce()
	started := <-done
	require.NoError(t, started.err)
	assert.Equal(t, agent.InputReceipt{
		Result: agent.InputAppliedNewWork, Submission: "turn-a",
		Acceptance: agent.Acceptance{Sent: new(true), NativeAccepted: new(true)},
	}, started.receipt)

	// The peer expects unsubscribe next. An interrupt would fail replay.
	receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, receipt)
}

func TestStartOrSteerMissingTurnIdentityIsOutcomeUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{"turn":{"id":"","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	_, err := session.StartOrSteer(ctx, textInput("first"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))

	receipt, err := session.StartOrSteer(ctx, textInput("must not be retried"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, receipt.Result)
	assert.Empty(t, receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)
}

func TestStartOrSteerMissingTurnIdentityAfterStartedAllowsNextInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"turn/start","params":{"input":[{"text":"first","type":"text"}],"threadId":"root-a"}}`,
		codexTurnStartedFrame,
		`<{"id":93,"result":{"turn":{"id":"","items":[],"status":"inProgress","startedAt":1700000001}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"next","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}}`,
	)
	frames = append(frames, codexCloseFrames(95, "root-a")...)
	var gate *gatedFrameReader
	session, stream := openControlledInteractionSession(t, ctx, frames, testStreamWrappers{
		reader: func(reader io.ReadCloser) io.ReadCloser {
			gate = newGatedFrameReader(reader, []byte("1700000001"))
			return gate
		},
	})
	t.Cleanup(gate.releaseOnce)

	done := make(chan error, 1)
	go func() {
		_, err := session.StartOrSteer(ctx, textInput("first"))
		done <- err
	}()
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	gate.releaseOnce()
	require.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(<-done))

	receipt, err := session.StartOrSteer(ctx, textInput("next"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedCurrentWork, receipt.Result)
	assert.Equal(t, "turn-a", receipt.Submission)
}

func TestStartOrSteerRejectsInvalidInputAndClosedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	session := openStartOrSteerSession(t, ctx, frames...)

	_, err := session.StartOrSteer(ctx, agent.Input{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	_, err = session.StartOrSteer(ctx, textInput("after close"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func openStartOrSteerSession(
	t *testing.T,
	ctx context.Context,
	frames ...string,
) agent.Session {
	t.Helper()
	return openStartOrSteerSessionWithPeer(t, ctx, prepareCodexPeer(t, frames...))
}

func openStartOrSteerSessionWithPeer(
	t *testing.T,
	ctx context.Context,
	peerSession *mocktest.Session,
) agent.Session {
	t.Helper()
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	return session
}

func textInput(text string) agent.Input {
	return agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: text}}},
	}
}
