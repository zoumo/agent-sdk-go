package agentcodex_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func TestDefiniteNotSentLeavesInteractionRetryable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSession(t, ctx, frames, agent.Features{})

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	canceled, stop := context.WithCancel(ctx)
	stop()
	receipt, err := session.Answer(canceled, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	assert.Equal(t, "answer_failed", errorReason(t, err))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, requested.Interaction, receipt.Interaction)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)
	var answerErr *codex.AnswerError
	require.ErrorAs(t, err, &answerErr)
	assert.Equal(t, codex.NotSent, answerErr.Dispatch)

	// A confirmed unsent answer neither closes the interaction nor inserts a
	// terminal event ahead of the caller's explicit retry.
	requireNoEvent(t, ctx, stream)
	retry, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.NoError(t, err)
	require.NotNil(t, retry.Acceptance.Sent)
	assert.True(t, *retry.Acceptance.Sent)

	require.NoError(t, session.Close(ctx))
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "session_closed", closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestOutcomeUnknownConsumesInteractionAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
		`<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval-1"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)

	var gate *gatedFrameReader
	var writer *matchingAnswerWriter
	session, stream := openControlledInteractionSessionWithStatus(
		t,
		ctx,
		frames,
		"passed",
		false,
		testStreamWrappers{
			reader: func(reader io.ReadCloser) io.ReadCloser {
				gate = newGatedFrameReader(reader, []byte("serverRequest/resolved"))
				return gate
			},
			writer: func(inner io.WriteCloser) io.WriteCloser {
				writer = newMatchingAnswerWriter(inner, []byte(`{"id":"approval-1","result":{"decision":"accept"}}`))
				return writer
			},
		},
	)
	t.Cleanup(writer.releaseOnce)
	t.Cleanup(gate.releaseOnce)

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	type answerResult struct {
		receipt agent.InteractionReceipt
		err     error
	}
	result := make(chan answerResult, 1)
	go func() {
		receipt, answerErr := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
			Outcome:  agent.PermissionSelected,
			OptionID: "allow-once",
		})
		result <- answerResult{receipt: receipt, err: answerErr}
	}()
	awaitSignal(t, writer.entered, "answer write")
	awaitSignal(t, gate.entered, "native resolution")
	gate.releaseOnce()

	var answered answerResult
	select {
	case answered = <-result:
	case <-ctx.Done():
		require.FailNow(t, "answer did not settle", ctx.Err())
	}
	require.Error(t, answered.err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(answered.err))
	assert.Equal(t, "answer_outcome_unknown", errorReason(t, answered.err))
	require.ErrorIs(t, answered.err, codex.ErrRequestResolved)
	assert.Equal(t, agent.InteractionReceipt{
		Interaction: requested.Interaction,
	}, answered.receipt)
	var answerErr *codex.AnswerError
	require.ErrorAs(t, answered.err, &answerErr)
	assert.Equal(t, codex.OutcomeUnknown, answerErr.Dispatch)

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionFailed, closed.Outcome)
	assert.Equal(t, "answer_outcome_unknown", closed.Reason)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))

	// The interaction was removed before the native write completed. No second
	// answer may acquire authority, or invent a Sent observation.
	retryReceipt, retryErr := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.Error(t, retryErr)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(retryErr))
	assert.Equal(t, "invalid_interaction_ref", errorReason(t, retryErr))
	assert.Equal(t, agent.InteractionReceipt{}, retryReceipt)

	writer.releaseOnce()
	require.NoError(t, session.Close(ctx))
	_, err := stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestAnswerInFlightWaitsForNativeLifecycleSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)

	var gate *gatedFrameReader
	var writer *blockingStreamWriter
	session, stream := openControlledInteractionSession(
		t,
		ctx,
		frames,
		testStreamWrappers{
			reader: func(reader io.ReadCloser) io.ReadCloser {
				gate = newGatedFrameReader(reader, []byte("turn/completed"))
				return gate
			},
			writer: func(inner io.WriteCloser) io.WriteCloser {
				writer = newBlockingStreamWriter(inner)
				return writer
			},
		},
	)
	t.Cleanup(writer.releaseOnce)
	t.Cleanup(gate.releaseOnce)

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	writer.arm()
	type answerResult struct {
		receipt agent.InteractionReceipt
		err     error
	}
	answer := make(chan answerResult, 1)
	go func() {
		receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
			Outcome:  agent.PermissionSelected,
			OptionID: "allow-once",
		})
		answer <- answerResult{receipt: receipt, err: err}
	}()
	awaitSignal(t, writer.entered, "answer write")
	awaitSignal(t, gate.entered, "turn completion")
	gate.releaseOnce()

	// Conversion of turn/completed waits for the admitted answer rather than
	// publishing a fallback settlement while the write is unresolved.
	requireNoEvent(t, ctx, stream)
	writer.releaseOnce()

	var answered answerResult
	select {
	case answered = <-answer:
	case <-ctx.Done():
		require.FailNow(t, "answer did not complete", ctx.Err())
	}
	require.NoError(t, answered.err)
	require.NotNil(t, answered.receipt.Acceptance.Sent)
	assert.True(t, *answered.receipt.Acceptance.Sent)

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "turn_completed", closed.Reason)
	require.Equal(t, agent.StateIdle, nextState(t, ctx, stream))
}

func TestSessionCloseReportsStableInFlightAnswerOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`>{"id":"approval-1","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)

	var writer *blockingStreamWriter
	session, stream := openControlledInteractionSession(
		t,
		ctx,
		frames,
		testStreamWrappers{
			writer: func(inner io.WriteCloser) io.WriteCloser {
				writer = newBlockingStreamWriter(inner)
				return writer
			},
		},
	)
	t.Cleanup(writer.releaseOnce)

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	writer.arm()
	answer := make(chan error, 1)
	go func() {
		_, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
			Outcome:  agent.PermissionSelected,
			OptionID: "allow-once",
		})
		answer <- err
	}()
	awaitSignal(t, writer.entered, "answer write")

	closedSession := make(chan error, 1)
	go func() { closedSession <- session.Close(ctx) }()

	var answerErr error
	select {
	case answerErr = <-answer:
	case <-ctx.Done():
		require.FailNow(t, "answer did not settle after Close", ctx.Err())
	}
	require.Error(t, answerErr)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(answerErr))
	assert.Equal(t, "answer_outcome_unknown", errorReason(t, answerErr))
	require.ErrorIs(t, answerErr, codex.ErrClosed)
	requireNoClose(t, closedSession)

	writer.releaseOnce()
	select {
	case err := <-closedSession:
		require.NoError(t, err)
	case <-ctx.Done():
		require.FailNow(t, "Close did not complete", ctx.Err())
	}

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionFailed, closed.Outcome)
	assert.Equal(t, "answer_outcome_unknown", closed.Reason)
	_, err := stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestPreAnswerLifecyclePreservesTerminalMapping(t *testing.T) {
	tests := []struct {
		name        string
		terminal    string
		syncModel   string
		wantReport  string
		wantOutcome agent.InteractionOutcome
		wantReason  string
	}{
		{
			name:        "native_resolved",
			terminal:    `<{"method":"serverRequest/resolved","params":{"threadId":"root-a","requestId":"approval-1"}}`,
			syncModel:   "synthetic-model-2",
			wantReport:  "passed",
			wantOutcome: agent.InteractionCancelled,
			wantReason:  "native_resolved",
		},
		{
			name:        "turn_completed",
			terminal:    `<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000200,"completedAt":1700000201}}}`,
			wantReport:  "failed",
			wantOutcome: agent.InteractionExpired,
			wantReason:  "turn_completed",
		},
		{
			name:        "thread_idle",
			terminal:    `<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"idle"}}}`,
			wantReport:  "failed",
			wantOutcome: agent.InteractionExpired,
			wantReason:  "thread_idle",
		},
		{
			name:        "thread_unavailable",
			terminal:    `<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"systemError"}}}`,
			wantReport:  "failed",
			wantOutcome: agent.InteractionCancelled,
			wantReason:  "thread_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := interactionSessionFrames(
				commandApprovalRequest("approval-1"),
			)
			if test.syncModel != "" {
				frames = append(frames,
					`>{"id":94,"method":"thread/settings/update","params":{"threadId":"root-a","model":"`+test.syncModel+`"}}`,
					`<{"id":94,"result":{}}`,
				)
			}
			frames = append(frames, test.terminal)
			closeID := 94
			if test.syncModel != "" {
				closeID = 95
			}
			frames = append(frames, codexCloseFrames(closeID, "root-a")...)
			session, stream := openInteractionSessionWithStatus(
				t,
				ctx,
				frames,
				agent.Features{},
				test.wantReport,
			)

			submitInteractionTurn(t, ctx, session)
			require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
			require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
			requested := nextInteractionRequested(t, ctx, stream)
			if test.syncModel != "" {
				_, err := session.SetConfig(ctx, agent.ConfigChange{
					ID:    agent.ConfigModel,
					Value: test.syncModel,
				})
				require.NoError(t, err)
			}

			closed := nextInteractionClosed(t, ctx, stream)
			assert.Equal(t, requested.Interaction, closed.Interaction)
			assert.Equal(t, test.wantOutcome, closed.Outcome)
			assert.Equal(t, test.wantReason, closed.Reason)

			receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
				Outcome:  agent.PermissionSelected,
				OptionID: "allow-once",
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			assert.Equal(t, "invalid_interaction_ref", errorReason(t, err))
			assert.Equal(t, agent.InteractionReceipt{}, receipt)
		})
	}
}

func TestMultipleInteractionsCloseInRegistrationOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		commandApprovalRequest("approval-2"),
		commandApprovalRequest("approval-3"),
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSessionWithStatus(
		t,
		ctx,
		frames,
		agent.Features{},
		"failed",
	)

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	var requested []agent.InteractionRequestedPayload
	for len(requested) != 3 {
		event := nextEvent(t, ctx, stream)
		switch payload := event.Payload.(type) {
		case agent.StateUpdatePayload:
			continue
		case agent.InteractionRequestedPayload:
			requested = append(requested, payload)
		default:
			require.FailNowf(
				t,
				"unexpected event before interactions registered",
				"type=%s payload=%#v",
				event.Type(),
				event.Payload,
			)
		}
	}

	require.NoError(t, session.Close(ctx))
	for _, want := range requested {
		closed := nextInteractionClosed(t, ctx, stream)
		assert.Equal(t, want.Interaction, closed.Interaction)
		assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
		assert.Equal(t, "session_closed", closed.Reason)
	}
	_, err := stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestPendingInteractionClosesForTerminalNativeThreadStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := interactionSessionFrames(
		commandApprovalRequest("approval-1"),
		`<{"method":"thread/status/changed","params":{"threadId":"root-a","status":{"type":"systemError"}}}`,
	)
	frames = append(frames, codexCloseFrames(94, "root-a")...)
	session, stream := openInteractionSessionWithStatus(
		t,
		ctx,
		frames,
		agent.Features{},
		"failed",
	)

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "thread_unavailable", closed.Reason)
	payload := nextStatePayload(t, ctx, stream)
	assert.Nil(t, payload.State)
	require.NotNil(t, payload.PreviousState)
	assert.Equal(t, agent.StateRequiresAction, *payload.PreviousState)
	assert.Equal(t, "systemError", payload.StopReason)
}

// matchingAnswerWriter holds the successful Write return for one canonical
// answer frame after delivering its bytes. Other writes cannot take its gate.
// The test waits for the resolved answer result and closure before releasing it,
// proving native resolution preceded the successful transport write return.
type matchingAnswerWriter struct {
	io.WriteCloser
	target  []byte
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newMatchingAnswerWriter(writer io.WriteCloser, target []byte) *matchingAnswerWriter {
	return &matchingAnswerWriter{
		WriteCloser: writer,
		target:      target,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (w *matchingAnswerWriter) releaseOnce() {
	w.once.Do(func() { close(w.release) })
}

func (w *matchingAnswerWriter) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	if bytes.Equal(bytes.TrimSpace(data), w.target) && err == nil && n == len(data) {
		close(w.entered)
		<-w.release
	}
	return n, err
}

type blockingStreamWriter struct {
	io.WriteCloser

	mu          sync.Mutex
	armed       bool
	entered     chan struct{}
	release     chan struct{}
	releaseDone sync.Once
}

func newBlockingStreamWriter(writer io.WriteCloser) *blockingStreamWriter {
	return &blockingStreamWriter{
		WriteCloser: writer,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (w *blockingStreamWriter) arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.armed = true
}

func (w *blockingStreamWriter) releaseOnce() {
	w.releaseDone.Do(func() { close(w.release) })
}

func (w *blockingStreamWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	blocked := w.armed
	if blocked {
		w.armed = false
		close(w.entered)
	}
	w.mu.Unlock()
	if blocked {
		<-w.release
	}
	return w.WriteCloser.Write(data)
}

type gatedFrameChunk struct {
	data []byte
	err  error
}

type gatedFrameReader struct {
	io.ReadCloser

	target      []byte
	entered     chan struct{}
	release     chan struct{}
	closed      chan struct{}
	chunks      chan gatedFrameChunk
	closeOnce   sync.Once
	releaseDone sync.Once
	pending     []byte
	pendingErr  error
	gated       bool
}

func newGatedFrameReader(reader io.ReadCloser, target []byte) *gatedFrameReader {
	gated := &gatedFrameReader{
		ReadCloser: reader,
		target:     append([]byte(nil), target...),
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
		closed:     make(chan struct{}),
		chunks:     make(chan gatedFrameChunk, 1),
	}
	go gated.readChunks()
	return gated
}

func (r *gatedFrameReader) readChunks() {
	defer close(r.chunks)
	reader := bufio.NewReader(r.ReadCloser)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) != 0 {
			select {
			case r.chunks <- gatedFrameChunk{data: line, err: err}:
			case <-r.closed:
				return
			}
		}
		if err != nil {
			if len(line) == 0 {
				select {
				case r.chunks <- gatedFrameChunk{err: err}:
				case <-r.closed:
				}
			}
			return
		}
	}
}

func (r *gatedFrameReader) releaseOnce() {
	r.releaseDone.Do(func() { close(r.release) })
}

func (r *gatedFrameReader) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.pendingErr != nil {
			err := r.pendingErr
			r.pendingErr = nil
			return 0, err
		}
		select {
		case chunk, ok := <-r.chunks:
			if !ok {
				return 0, io.EOF
			}
			r.pending = chunk.data
			r.pendingErr = chunk.err
		case <-r.closed:
			return 0, io.EOF
		}
		if !r.gated && bytes.Contains(r.pending, r.target) {
			r.gated = true
			close(r.entered)
			select {
			case <-r.release:
			case <-r.closed:
				return 0, io.EOF
			}
		}
	}
	n := copy(data, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *gatedFrameReader) Close() error {
	var err error
	r.closeOnce.Do(func() {
		close(r.closed)
		err = r.ReadCloser.Close()
	})
	return err
}

func openControlledInteractionSession(
	t *testing.T,
	ctx context.Context,
	frames []string,
	wrappers testStreamWrappers,
) (testSession, agent.EventStream) {
	t.Helper()
	return openControlledInteractionSessionWithStatus(
		t,
		ctx,
		frames,
		"passed",
		false,
		wrappers,
	)
}

func openControlledInteractionSessionWithStatus(
	t *testing.T,
	ctx context.Context,
	frames []string,
	wantReportStatus string,
	wantCloseError bool,
	wrappers testStreamWrappers,
) (testSession, agent.EventStream) {
	t.Helper()
	peerSession := prepareCodexPeerWithStatus(t, wantReportStatus, frames...)
	provider := newTestProviderWithWrappers(
		t,
		peerSession,
		codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
			Capabilities: codextypes.Some(codextypes.InitializeCapabilities{
				ExperimentalAPI: codextypes.Some(true),
			}),
		},
		wrappers,
	)
	t.Cleanup(func() {
		err := provider.Stop(context.Background())
		if wantReportStatus == "passed" {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err, "a failed replay must remain visible through the provider")
		}
	})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		err := session.Close(context.Background())
		if wantCloseError {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	})
	stream, err := session.Events()
	require.NoError(t, err)
	concrete, ok := session.(testSession)
	require.True(t, ok)
	return concrete, stream
}

func awaitSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		require.FailNow(t, label+" did not reach its synchronization point")
	}
}

func requireNoEvent(t *testing.T, ctx context.Context, stream agent.EventStream) {
	t.Helper()
	wait, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := stream.Next(wait)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func requireNoClose(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		require.FailNowf(t, "Close returned before the admitted answer settled", "err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func errorReason(t *testing.T, err error) string {
	t.Helper()
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	return classified.Reason
}
