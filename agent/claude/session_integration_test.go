package agentclaude_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestStartOrSteerMapsMinimalEventsAndPreservesNativeEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-loop","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		claudeStartOrSteerFrame(t, "hello"),
		claudeStartOrSteerFrame(t, "follow-up"),
		`<{"type":"assistant","session_id":"native-loop","uuid":"assistant-event","timestamp":"2026-11-07T12:34:56.789Z","message":{"id":"message-1","role":"assistant","model":"synthetic","content":[{"type":"text","text":"hello from Claude","apiBlockIndex":0}],"future_message":null},"future":{"precise":9007199254740993},"future_message":null}`,
		`<{"type":"system","subtype":"session_state_changed","session_id":"native-loop","state":"running","timestamp":"2026-11-07T12:34:57Z"}`,
		`<{"type":"result","subtype":"success","session_id":"native-loop","is_error":false,"result":"done","timestamp":"2026-11-07T12:34:58Z"}`,
		`<{"type":"system","subtype":"session_state_changed","session_id":"native-loop","state":"idle","timestamp":"2026-11-07T12:34:59Z"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	assert.Equal(t, "native-loop", session.ID())

	receipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedBestEffort, receipt.Result)
	assert.NotEmpty(t, receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)

	second, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "follow-up"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedBestEffort, second.Result)
	assert.NotEmpty(t, second.Submission)
	assert.NotEqual(t, receipt.Submission, second.Submission)
	require.NotNil(t, second.Acceptance.Sent)
	assert.True(t, *second.Acceptance.Sent)
	assert.Nil(t, second.Acceptance.NativeAccepted)

	message, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, message.Type())
	assert.Equal(t, "native-loop", message.SessionID)
	assert.Equal(t, "message-1:0", message.ItemID)
	assert.Equal(t, time.Date(2026, 11, 7, 12, 34, 56, 789_000_000, time.UTC), message.Time)
	assert.Equal(t, agent.AgentMessage{
		MessageID: "message-1:0",
		Content:   []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello from Claude"}}},
	}, message.Payload)
	require.NoError(t, message.Validate())

	claudeMeta := requireClaudeMeta(t, message)
	raw, ok := claudeMeta["raw"].(json.RawMessage)
	require.True(t, ok)
	var native map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &native))
	assert.JSONEq(t, `{"precise":9007199254740993}`, string(native["future"]))
	assert.JSONEq(t, `null`, string(native["future_message"]))

	runningEvent, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionStateUpdate, runningEvent.Type())
	running, ok := runningEvent.Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	assert.Nil(t, running.PreviousState)
	require.NotNil(t, running.State)
	assert.Equal(t, agent.StateRunning, *running.State)
	assert.Equal(t, time.Date(2026, 11, 7, 12, 34, 57, 0, time.UTC), runningEvent.Time)

	idleEvent, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionStateUpdate, idleEvent.Type())
	idle, ok := idleEvent.Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	require.NotNil(t, idle.PreviousState)
	assert.Equal(t, agent.StateRunning, *idle.PreviousState)
	require.NotNil(t, idle.State)
	assert.Equal(t, agent.StateIdle, *idle.State)
	assert.Equal(t, time.Date(2026, 11, 7, 12, 34, 59, 0, time.UTC), idleEvent.Time)

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestResultErrorBecomesSessionError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-error","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"result","subtype":"error_during_execution","session_id":"native-error","is_error":true,"result":"operation failed","errors":["first","second"],"timestamp":"2026-11-07T12:40:00Z","future":42}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionError, event.Type())
	assert.Equal(t, agent.SessionErrorPayload{
		Message: "operation failed",
		Reason:  "native_result_error",
	}, event.Payload)
	assert.Equal(t, time.Date(2026, 11, 7, 12, 40, 0, 0, time.UTC), event.Time)
	require.NoError(t, event.Validate())
}

func TestEnvelopeErrorBecomesSessionError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-envelope-error","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-envelope-error","error":"envelope failed","message":{"id":"partial-message","content":[{"type":"text","text":"partial","apiBlockIndex":0}]},"future":true}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	event, err = stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionError, event.Type())
	assert.Equal(t, agent.SessionErrorPayload{
		Message: "envelope failed",
		Reason:  "native_error",
	}, event.Payload)
}

func TestEventWaitCancellationDoesNotConsumeOrClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"assistant","session_id":"native-early","message":{"id":"early-message","content":[{"type":"text","text":"early","apiBlockIndex":0}]}}`,
		`<{"type":"system","subtype":"init","session_id":"native-early","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = stream.Next(canceled)
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-session.Done():
		require.Fail(t, "canceling Next terminated the session")
	default:
	}

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "early-message:0", event.ItemID)
}

func TestCloseDrainsAcceptedEarlyEventThenReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"assistant","session_id":"native-drain","message":{"id":"drained-message","content":[{"type":"text","text":"drained","apiBlockIndex":0}]}}`,
		`<{"type":"system","subtype":"init","session_id":"native-drain","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return agentclaude.InjectSupportedForTest(session)
	}, 3*time.Second, time.Millisecond)
	require.NoError(t, session.Close(ctx))
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "drained-message:0", event.ItemID)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, session.Err())
	_, err = session.Events()
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestUnexpectedDisconnectTerminatesDoneErrAndStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-disconnect","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`>{"type":"user","session_id":"","parent_tool_use_id":null,"message":{"role":"user","content":"disconnect"}}`,
		"!",
	)
	peerSession := prepareClaudePeer(t, "failed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)

	// Hold the scripted disconnect until Open has returned; otherwise an early
	// transport failure can legitimately win the initialization race.
	_, _ = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "disconnect"}}},
	})
	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(session.Err()))
	stream, err := session.Events()
	require.NoError(t, err)
	_, err = stream.Next(ctx)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
}

func TestNaturalTerminationReleasesProviderLeaseAndKeepsEventsDrainable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-natural","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-natural","message":{"id":"kept-message","content":[{"type":"text","text":"kept","apiBlockIndex":0}]}}`,
		`>{"type":"user","session_id":"","parent_tool_use_id":null,"message":{"role":"user","content":"disconnect"}}`,
		"!",
	)
	peerSession := prepareClaudePeer(t, "failed", frames...)
	provider := newTestClaudeProvider(t, peerSession)

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	_, _ = session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "disconnect"}}},
	})
	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}

	require.NoError(t, provider.Stop(ctx))
	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	_, err = stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	assert.Same(t, session.Err(), err)
}

func TestAnswerRejectsUnknownRefAndInterruptHasNoActiveWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-unsupported","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	_, err = session.Answer(
		ctx,
		agent.InteractionRef("missing"),
		agent.PermissionAnswer{},
	)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
	receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, receipt.Result)
	assert.Empty(t, receipt.Target)
}

func requireClaudeMeta(t *testing.T, event agent.Event) map[string]any {
	t.Helper()
	meta, ok := event.Meta["claude"].(map[string]any)
	require.True(t, ok)
	return meta
}
