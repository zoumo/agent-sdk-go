package agentclaude_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestClaudeNotSentAnswerRollsBackAndRetriesSameReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-not-sent", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-not-sent", "running", "2026-11-11T09:00:00Z"),
		claudePermissionRequestFrame(
			"permission-not-sent",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-not-sent","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
		claudeStartOrSteerFrame(t, "keep-open"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	answer := agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	}
	notSentContext, cancelNotSent := context.WithCancel(ctx)
	cancelNotSent()
	receipt, err := session.Answer(notSentContext, requested.Interaction, answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	requireAgentErrorReason(t, err, "answer_failed")
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)

	retried, err := session.Answer(ctx, requested.Interaction, answer)
	require.NoError(t, err)
	require.NotNil(t, retried.Acceptance.Sent)
	assert.True(t, *retried.Acceptance.Sent)

	keepOpen, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "keep-open"}}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, keepOpen.Submission)
	require.NotNil(t, keepOpen.Acceptance.Sent)
	assert.True(t, *keepOpen.Acceptance.Sent)
	require.NoError(t, session.Close(ctx))
	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "session_closed", closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

// The answer context remains live. Its first Err check occurs in native
// reserveAnswer, after the public adapter has claimed the interaction slot.
// Holding that check lets native cancellation happen before write authorization.
type claudeAnswerAdmissionGate struct {
	context.Context
	entered     chan struct{}
	releaseCh   chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (g *claudeAnswerAdmissionGate) Err() error {
	g.once.Do(func() {
		close(g.entered)
		<-g.releaseCh
	})
	return g.Context.Err()
}

func (g *claudeAnswerAdmissionGate) release() {
	g.releaseOnce.Do(func() { close(g.releaseCh) })
}

func TestClaudeNotSentWithCanceledNativeRequestConsumesAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	readGate := newClaudeTestReadGate(`"request_id":"permission-native-cancel"`)
	defer readGate.release()
	frames := []string{
		claudeSessionInitFrame(t, "native-not-sent-cancel", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-not-sent-cancel", "running", "2026-11-11T09:05:00Z"),
		claudePermissionRequestFrame("permission-native-cancel", "Read", "read-tool", `{"file_path":"a.txt"}`),
		`<{"type":"control_cancel_request","request_id":"permission-native-cancel"}`,
		claudeStartOrSteerFrame(t, "keep-open"),
	}
	session, stream := openClaudeInteractionSessionWithHook(t, ctx, cwd, "passed", agent.Features{},
		func(transport claude.StreamTransport) claude.StreamTransport {
			readGate.attach(transport.Reader)
			transport.Reader = readGate
			return transport
		}, frames...)
	_, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}}})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)
	nativeContext, snapshot := agentclaude.ObserveInteractionForTest(session, requested.Interaction)
	require.NoError(t, nativeContext.Err())
	answer := agent.PermissionAnswer{Outcome: agent.PermissionSelected, OptionID: "allow-once"}
	gate := &claudeAnswerAdmissionGate{Context: ctx, entered: make(chan struct{}), releaseCh: make(chan struct{})}
	defer gate.release()
	type answerResult struct {
		receipt agent.InteractionReceipt
		err     error
	}
	result := make(chan answerResult, 1)
	go func() {
		receipt, answerErr := session.Answer(gate, requested.Interaction, answer)
		result <- answerResult{receipt: receipt, err: answerErr}
	}()
	waitClaudeTestSignal(t, ctx, gate.entered, "admitted native reply")
	state, live := snapshot()
	require.Equal(t, "answering", state)
	require.True(t, live)

	readGate.release()
	waitClaudeTestSignal(t, ctx, nativeContext.Done(), "native request cancellation")
	require.ErrorIs(t, context.Cause(nativeContext), claude.ErrPermissionCancelled)
	require.NoError(t, gate.Context.Err(), "the Answer caller context must remain live")
	// Cancellation cannot publish closure until the admitted reply has settled.
	wait, cancelWait := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = stream.Next(wait)
	cancelWait()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	state, _ = snapshot()
	require.Equal(t, "answering", state)

	gate.release()
	var answered answerResult
	select {
	case answered = <-result:
	case <-ctx.Done():
		require.FailNow(t, "admitted answer did not settle", ctx.Err())
	}
	require.Error(t, answered.err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(answered.err))
	requireAgentErrorReason(t, answered.err, "native_request_ended")
	require.ErrorIs(t, answered.err, claude.ErrPermissionCancelled)
	var dispatch *claude.DispatchError
	require.ErrorAs(t, answered.err, &dispatch)
	assert.Equal(t, claude.NotSent, dispatch.State)
	require.Equal(t, requested.Interaction, answered.receipt.Interaction)
	require.NotNil(t, answered.receipt.Acceptance.Sent)
	assert.False(t, *answered.receipt.Acceptance.Sent)
	assert.Nil(t, answered.receipt.Acceptance.NativeAccepted)
	assert.Nil(t, answered.receipt.Acceptance.NativeRecorded)
	state, live = snapshot()
	assert.Equal(t, "settled", state)
	assert.False(t, live)

	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "native_request_ended", closed.Reason)
	retry, err := session.Answer(ctx, requested.Interaction, answer)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	requireAgentErrorReason(t, err, "invalid_interaction_ref")
	assert.Equal(t, agent.InteractionReceipt{}, retry)

	_, err = session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "keep-open"}}}})
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeOutcomeUnknownSettlesFailedAndRemovesAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	readGate := newClaudeTestReadGate(`"request_id":"permission-unknown"`)
	writeGate := newClaudeTestWriteGate(
		`"request_id":"permission-unknown"`,
		false,
		errors.New("synthetic permission write failure"),
	)
	defer readGate.release()
	defer writeGate.release()
	frames := []string{
		claudeSessionInitFrame(t, "native-unknown", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-unknown", "running", "2026-11-11T09:10:00Z"),
		claudePermissionRequestFrame(
			"permission-unknown",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-unknown","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	}
	session, stream := openClaudeInteractionSessionWithHook(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		func(transport claude.StreamTransport) claude.StreamTransport {
			readGate.attach(transport.Reader)
			writeGate.attach(transport.Writer)
			transport.Reader = readGate
			transport.Writer = writeGate
			return transport
		},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	requireAgentErrorReason(t, err, "answer_outcome_unknown")
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)

	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionFailed, closed.Outcome)
	assert.Equal(t, "answer_outcome_unknown", closed.Reason)

	_, err = session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Contains(t,
		[]agent.ErrorCode{agent.ErrorInvalidArgument, agent.ErrorClosed},
		agent.CodeOf(err),
	)
}

func TestClaudeCloseWaitsForInFlightAnswerSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	writeGate := newClaudeTestWriteGate(
		`"request_id":"permission-inflight-close"`,
		true,
		nil,
	)
	defer writeGate.release()
	frames := []string{
		claudeSessionInitFrame(t, "native-inflight-close", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-inflight-close", "running", "2026-11-11T09:20:00Z"),
		claudePermissionRequestFrame(
			"permission-inflight-close",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-inflight-close","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	}
	session, stream := openClaudeInteractionSessionWithHook(
		t,
		ctx,
		cwd,
		"failed",
		agent.Features{},
		func(transport claude.StreamTransport) claude.StreamTransport {
			writeGate.attach(transport.Writer)
			transport.Writer = writeGate
			return transport
		},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	type answerResult struct {
		receipt agent.InteractionReceipt
		err     error
	}
	answerDone := make(chan answerResult, 1)
	go func() {
		receipt, answerErr := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
			Outcome:  agent.PermissionSelected,
			OptionID: "allow-once",
		})
		answerDone <- answerResult{receipt: receipt, err: answerErr}
	}()
	select {
	case answer := <-answerDone:
		require.FailNow(t, "answer completed before the write gate was observed", "%v", answer.err)
	case <-time.After(100 * time.Millisecond):
	}
	waitClaudeTestSignal(t, ctx, writeGate.activated, "permission answer write")

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- session.Close(ctx)
	}()
	select {
	case closeErr := <-closeDone:
		require.FailNow(t, "session Close returned before the in-flight answer settled", "%v", closeErr)
	case <-time.After(100 * time.Millisecond):
	}

	writeGate.release()
	select {
	case closeErr := <-closeDone:
		require.Error(t, closeErr)
		assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(closeErr))
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	answer := <-answerDone
	require.Error(t, answer.err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(answer.err))
	requireAgentErrorReason(t, answer.err, "answer_outcome_unknown")
	require.NotNil(t, answer.receipt.Acceptance.Sent)
	assert.False(t, *answer.receipt.Acceptance.Sent)

	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionFailed, closed.Outcome)
	assert.Equal(t, "answer_outcome_unknown", closed.Reason)
	_, err = stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	requireAgentErrorReason(t, err, "session_terminated")
}

func TestClaudeNativeStateChangeWaitsForInFlightAnswerSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	readGate := newClaudeTestReadGate(`"request_id":"permission-inflight-state"`)
	writeGate := newClaudeTestWriteGate(
		`"request_id":"permission-inflight-state"`,
		true,
		nil,
	)
	defer readGate.release()
	defer writeGate.release()
	frames := []string{
		claudeSessionInitFrame(t, "native-inflight-state", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-inflight-state", "running", "2026-11-11T09:30:00Z"),
		claudePermissionRequestFrame(
			"permission-inflight-state",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		claudeStateFrame("native-inflight-state", "idle", "2026-11-11T09:30:03Z"),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-inflight-state","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
		claudeStartOrSteerFrame(t, "keep-open"),
	}
	session, stream := openClaudeInteractionSessionWithHook(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		func(transport claude.StreamTransport) claude.StreamTransport {
			readGate.attach(transport.Reader)
			writeGate.attach(transport.Writer)
			transport.Reader = readGate
			transport.Writer = writeGate
			return transport
		},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	requested := nextClaudeInteractionRequested(t, ctx, stream)

	type answerResult struct {
		receipt agent.InteractionReceipt
		err     error
	}
	answerDone := make(chan answerResult, 1)
	go func() {
		receipt, answerErr := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
			Outcome:  agent.PermissionSelected,
			OptionID: "allow-once",
		})
		answerDone <- answerResult{receipt: receipt, err: answerErr}
	}()
	waitClaudeTestSignal(t, ctx, writeGate.activated, "permission answer write")

	readGate.release()
	waitClaudeTestSignal(t, ctx, readGate.activated, "native state frame read")
	blockedContext, cancelBlocked := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelBlocked()
	_, err = stream.Next(blockedContext)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	writeGate.release()
	select {
	case answer := <-answerDone:
		require.NoError(t, answer.err)
		require.NotNil(t, answer.receipt.Acceptance.Sent)
		assert.True(t, *answer.receipt.Acceptance.Sent)
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}

	closed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "native_state_advanced", closed.Reason)
	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))

	_, err = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "keep-open"}}},
	})
	require.NoError(t, err)
	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestClaudeMultipleInteractionsCloseInRegistrationOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-close-order", cwd),
		claudeStartOrSteerFrame(t, "run"),
		claudeStateFrame("native-close-order", "running", "2026-11-11T09:40:00Z"),
		claudePermissionRequestFrame(
			"permission-first",
			"Read",
			"read-first",
			`{"file_path":"first.txt"}`,
		),
		claudePermissionRequestFrame(
			"permission-second",
			"Write",
			"write-second",
			`{"file_path":"second.txt"}`,
		),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"failed",
		agent.Features{},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "run"}}},
	})
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	first := nextClaudeInteractionRequested(t, ctx, stream)
	second := nextClaudeInteractionRequested(t, ctx, stream)
	require.NotEqual(t, first.Interaction, second.Interaction)

	err = session.Close(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	firstClosed := nextClaudeInteractionClosed(t, ctx, stream)
	secondClosed := nextClaudeInteractionClosed(t, ctx, stream)
	assert.Equal(t, first.Interaction, firstClosed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, firstClosed.Outcome)
	assert.Equal(t, "session_closed", firstClosed.Reason)
	assert.Equal(t, second.Interaction, secondClosed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, secondClosed.Outcome)
	assert.Equal(t, "session_closed", secondClosed.Reason)
	_, err = stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	requireAgentErrorReason(t, err, "session_terminated")
}

func requireAgentErrorReason(t *testing.T, err error, reason string) {
	t.Helper()
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, reason, classified.Reason)
}

func waitClaudeTestSignal(
	t *testing.T,
	ctx context.Context,
	signal <-chan struct{},
	description string,
) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "waiting for %s", description)
	}
}

type claudeTestReadGate struct {
	io.ReadCloser
	match        []byte
	activated    chan struct{}
	releaseCh    chan struct{}
	activateOnce sync.Once
	releaseOnce  sync.Once
	reader       *bufio.Reader
	pending      []byte
	pendingErr   error
	hold         bool
	activate     bool
}

func newClaudeTestReadGate(match string) *claudeTestReadGate {
	return &claudeTestReadGate{
		match:     []byte(match),
		activated: make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (g *claudeTestReadGate) attach(source io.ReadCloser) {
	g.ReadCloser = source
	g.reader = bufio.NewReader(source)
}

func (g *claudeTestReadGate) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if g.hold {
			<-g.releaseCh
			g.hold = false
		}
		if len(g.pending) == 0 {
			if g.pendingErr != nil {
				err := g.pendingErr
				g.pendingErr = nil
				return 0, err
			}
			line, err := g.reader.ReadBytes('\n')
			if len(line) == 0 && err != nil {
				return 0, err
			}
			g.pending = line
			g.pendingErr = err
			g.activate = bytes.Contains(line, g.match)
			continue
		}
		n := copy(p, g.pending)
		g.pending = g.pending[n:]
		if len(g.pending) == 0 && g.activate {
			g.activate = false
			g.hold = true
			g.activateOnce.Do(func() { close(g.activated) })
		}
		return n, nil
	}
}

func (g *claudeTestReadGate) release() {
	g.releaseOnce.Do(func() {
		close(g.releaseCh)
	})
}

func (g *claudeTestReadGate) Close() error {
	g.release()
	if g.ReadCloser == nil {
		return nil
	}
	return g.ReadCloser.Close()
}

type claudeTestWriteGate struct {
	io.WriteCloser
	match        []byte
	block        bool
	afterWrite   error
	activated    chan struct{}
	releaseCh    chan struct{}
	activateOnce sync.Once
	releaseOnce  sync.Once
}

func newClaudeTestWriteGate(
	match string,
	block bool,
	afterWrite error,
) *claudeTestWriteGate {
	return &claudeTestWriteGate{
		match:      []byte(match),
		block:      block,
		afterWrite: afterWrite,
		activated:  make(chan struct{}),
		releaseCh:  make(chan struct{}),
	}
}

func (g *claudeTestWriteGate) attach(source io.WriteCloser) {
	g.WriteCloser = source
}

func (g *claudeTestWriteGate) Write(p []byte) (int, error) {
	if !bytes.Contains(p, g.match) {
		return g.WriteCloser.Write(p)
	}
	g.activateOnce.Do(func() { close(g.activated) })
	if g.block {
		<-g.releaseCh
	}
	n, err := g.WriteCloser.Write(p)
	if err == nil && n == len(p) && g.afterWrite != nil {
		return n, g.afterWrite
	}
	return n, err
}

func (g *claudeTestWriteGate) release() {
	g.releaseOnce.Do(func() {
		close(g.releaseCh)
	})
}

func (g *claudeTestWriteGate) Close() error {
	if g.WriteCloser == nil {
		return nil
	}
	return g.WriteCloser.Close()
}
