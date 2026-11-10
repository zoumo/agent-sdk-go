package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
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
		`<{"id":93,"result":{"turn":{"id":"turn-maybe","items":[],"status":"inProgress"}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeerWithFrameDelays(
		t,
		"passed",
		map[int]time.Duration{6: 800 * time.Millisecond},
		frames...,
	)
	session := openStartOrSteerSessionWithPeer(t, ctx, peerSession)

	unknownCtx, stopUnknown := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stopUnknown()
	_, err := session.StartOrSteer(unknownCtx, textInput("maybe"))
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
