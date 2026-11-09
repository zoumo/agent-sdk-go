package agentclaude_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestStartOrSteerWritesBestEffortWhenIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-start", cwd),
		claudeStartOrSteerFrame(t, "nudge"),
		claudeStateFrame("native-start", "running", "2026-11-09T10:00:00Z"),
		claudeStateFrame("native-start", "idle", "2026-11-09T10:00:01Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	receipt, err := session.StartOrSteer(ctx, claudeTextInput("nudge"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedBestEffort, receipt.Result)
	assert.NotEmpty(t, receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Nil(t, receipt.Acceptance.NativeRecorded)

	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
}

func TestStartOrSteerWritesBestEffortWhileBusy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-busy", cwd),
		claudeStartOrSteerFrame(t, "first"),
		claudeStateFrame("native-busy", "running", "2026-11-09T10:10:00Z"),
		claudeStartOrSteerFrame(t, "second"),
		claudeStateFrame("native-busy", "idle", "2026-11-09T10:10:01Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	first, err := session.StartOrSteer(ctx, claudeTextInput("first"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedBestEffort, first.Result)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	second, err := session.StartOrSteer(ctx, claudeTextInput("second"))
	require.NoError(t, err)
	assert.Equal(t, agent.InputAppliedBestEffort, second.Result)
	assert.NotEmpty(t, second.Submission)
	assert.NotEqual(t, first.Submission, second.Submission)
	require.NotNil(t, second.Acceptance.Sent)
	assert.True(t, *second.Acceptance.Sent)
	assert.Nil(t, second.Acceptance.NativeAccepted)

	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
}

func TestStartOrSteerInterruptTargetsSubmissionAfterRunningState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-start-interrupt", cwd),
		claudeStartOrSteerFrame(t, "nudge"),
		claudeStateFrame("native-start-interrupt", "running", "2026-11-09T10:30:00Z"),
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"interrupt"}}`,
		`<{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"go_1","response":{}}}`,
		claudeStateFrame("native-start-interrupt", "idle", "2026-11-09T10:30:01Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	submitted, err := session.StartOrSteer(ctx, claudeTextInput("nudge"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedBestEffort, submitted.Result)
	require.NotEmpty(t, submitted.Submission)
	require.Nil(t, submitted.Acceptance.NativeAccepted)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, submitted.Submission, receipt.Target)
	assert.Equal(t, agent.InterruptAcknowledged, receipt.Result)

	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
	noWork, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, noWork)

	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestStartOrSteerPreservesExistingInterruptTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-start-existing", cwd),
		claudeSubmitFrame(t, "first"),
		claudeStateFrame("native-start-existing", "running", "2026-11-09T10:40:00Z"),
		claudeStartOrSteerFrame(t, "second"),
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"interrupt"}}`,
		`<{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"go_1","response":{}}}`,
		claudeStateFrame("native-start-existing", "idle", "2026-11-09T10:40:01Z"),
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	first, err := session.Submit(ctx, claudeTextInput("first"))
	require.NoError(t, err)
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))

	second, err := session.StartOrSteer(ctx, claudeTextInput("second"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedBestEffort, second.Result)
	require.NotEqual(t, first.Submission, second.Submission)

	receipt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, first.Submission, receipt.Target)
	assert.Equal(t, agent.InterruptAcknowledged, receipt.Result)

	require.Equal(t, agent.StateIdle, nextStateValue(t, ctx, stream))
	require.NoError(t, session.Close(ctx))
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestStartOrSteerAttributesPermissionBeforeRunningState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		claudeSessionInitFrame(t, "native-start-permission", cwd),
		claudeStartOrSteerFrame(t, "inspect"),
		claudePermissionRequestFrame(
			"permission-start",
			"Read",
			"read-tool",
			`{"file_path":"a.txt"}`,
		),
		`>{"type":"control_response","response":{"subtype":"success",` +
			`"request_id":"permission-start","response":{"behavior":"allow",` +
			`"updatedInput":{"file_path":"a.txt"}}}}`,
	}
	session, stream := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	submitted, err := session.StartOrSteer(ctx, claudeTextInput("inspect"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedBestEffort, submitted.Result)
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))

	requested := nextClaudeInteractionRequested(t, ctx, stream)
	assert.Equal(t, agent.InteractionPermission, requested.Request.Kind)
	assert.Equal(t, "read-tool", requested.Request.ToolCallID)

	answered, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.NoError(t, err)
	assert.Equal(t, requested.Interaction, answered.Interaction)
	require.NotNil(t, answered.Acceptance.Sent)
	assert.True(t, *answered.Acceptance.Sent)

	require.NoError(t, session.Close(ctx))
}

func TestStartOrSteerRejectsInvalidInputAndClosedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{claudeSessionInitFrame(t, "native-invalid", cwd)}
	session, _ := openClaudeInteractionSession(
		t,
		ctx,
		cwd,
		"passed",
		agent.Features{},
		frames...,
	)

	_, err := session.StartOrSteer(ctx, agent.Input{})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	_, err = session.StartOrSteer(ctx, claudeTextInput("after close"))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestStartOrSteerUnknownWriteIsNotRetriedOrQueued(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		claudeSessionInitFrame(t, "native-unknown", cwd),
		claudeStateFrame("native-unknown", "running", "2026-11-09T10:20:00Z"),
	)
	peerSession := prepareClaudePeerWithTiming(
		t,
		"failed",
		10*time.Second,
		map[int]time.Duration{3: time.Second},
		frames...,
	)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	unknownCtx, stopUnknown := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stopUnknown()
	_, err = session.StartOrSteer(unknownCtx, claudeTextInput(strings.Repeat("x", 1<<20)))
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))

	receipt, secondErr := session.StartOrSteer(ctx, claudeTextInput("must not be queued"))
	if secondErr != nil {
		require.Equal(t, agent.ErrorClosed, agent.CodeOf(secondErr))
	} else {
		assert.Equal(t, agent.InputNotApplied, receipt.Result)
		assert.Empty(t, receipt.Submission)
	}
}

func claudeStartOrSteerFrame(t *testing.T, text string) string {
	t.Helper()
	return `>{"message":{"role":"user","content":` + jsonString(t, text) +
		`},"parent_tool_use_id":null,"priority":"next","session_id":"","shouldQuery":true,"type":"user"}`
}

func claudeTextInput(text string) agent.Input {
	return agent.Input{Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}
