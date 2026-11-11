package agentclaude_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestProviderLifecycleIsIdempotentAndTerminal(t *testing.T) {
	ctx := context.Background()
	streamCalls := 0
	provider := agentclaude.NewWithStream(
		agentclaude.Config{
			CLIPath: filepath.Join(t.TempDir(), "missing-claude"),
		},
		func(context.Context, claude.SessionOptions) (agentclaude.TestStreamOpen, error) {
			streamCalls++
			return agentclaude.TestStreamOpen{}, nil
		},
	)

	capabilities, err := provider.Start(ctx)
	require.NoError(t, err)
	require.NoError(t, capabilities.Validate())
	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))
	assert.True(t, capabilities.Supports(agent.CapabilityPermission))
	assert.True(t, capabilities.Supports(agent.CapabilityForm))
	assert.Zero(t, streamCalls)

	capabilities[agent.CapabilityForm] = false
	repeated, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.True(t, repeated.Supports(agent.CapabilityTextInput))
	assert.True(t, repeated.Supports(agent.CapabilityPermission))
	assert.True(t, repeated.Supports(agent.CapabilityForm))
	assert.Zero(t, streamCalls)

	require.NoError(t, provider.Stop(ctx))
	require.NoError(t, provider.Stop(ctx))

	_, err = provider.Start(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestProviderRequiresStartBeforeOpen(t *testing.T) {
	provider := agentclaude.New(agentclaude.Config{})

	_, err := provider.Open(context.Background(), agent.OpenOptions{Cwd: t.TempDir()})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
}

func TestProviderStopRejectsInFlightOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-in-flight","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	openEntered := make(chan struct{})
	releaseOpen := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseOpen)
		})
	}
	defer release()
	provider := newTestClaudeProviderWithStreamHook(
		t,
		agentclaude.Config{},
		nil,
		func(transport claude.StreamTransport) claude.StreamTransport {
			close(openEntered)
			<-releaseOpen
			return transport
		},
		peerSession,
	)

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	type openResult struct {
		session agent.Session
		err     error
	}
	opened := make(chan openResult, 1)
	go func() {
		session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
		opened <- openResult{session: session, err: err}
	}()
	select {
	case <-openEntered:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}

	stopErr := provider.Stop(ctx)
	require.Error(t, stopErr)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(stopErr))
	var classified *agent.Error
	require.ErrorAs(t, stopErr, &classified)
	assert.Equal(t, agent.ErrorReasonCoreHasOpenSessions, classified.Reason)

	release()
	var result openResult
	select {
	case result = <-opened:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	require.NoError(t, result.err)
	closeSessionOnCleanup(t, result.session)
	require.NoError(t, result.session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}

func TestProviderStopRejectsInFlightResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(
		t,
		configDir,
		"project",
		claudeResumePeerSessionID,
		cwd,
	)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD:            cwd,
		TranscriptPath: transcript,
	})
	provider := newResumeProvider(
		t,
		peer.CLIPath,
		peer.Env,
		configDir,
		10*time.Second,
	)

	resumeCtx, cancelResume := context.WithCancel(ctx)
	defer cancelResume()
	type resumeResult struct {
		session agent.Session
		err     error
	}
	resumed := make(chan resumeResult, 1)
	go func() {
		session, err := provider.Resume(resumeCtx, agent.ResumeOptions{
			SessionID:   claudeResumePeerSessionID,
			OpenOptions: agent.OpenOptions{Cwd: cwd},
		})
		resumed <- resumeResult{session: session, err: err}
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(peer.SessionMark)
		return err == nil
	}, 3*time.Second, 10*time.Millisecond, "native Resume did not start")

	stopErr := provider.Stop(ctx)
	require.Error(t, stopErr)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(stopErr))
	var classified *agent.Error
	require.ErrorAs(t, stopErr, &classified)
	assert.Equal(t, agent.ErrorReasonCoreHasOpenSessions, classified.Reason)

	cancelResume()
	var result resumeResult
	select {
	case result = <-resumed:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	require.Error(t, result.err)
	assert.Nil(t, result.session)
	require.NoError(t, provider.Stop(ctx))
}

func TestOpenFailureSettlesLifecycleAndProviderCanOpenAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	wrongCwd := t.TempDir()
	firstFrames := append([]string(nil), mockClaudeInit...)
	firstFrames = append(firstFrames,
		`<{"type":"system","subtype":"init","session_id":"native-wrong-cwd","cwd":`+
			jsonString(t, wrongCwd)+`,"claude_code_version":"2.1.258"}`,
	)
	secondFrames := append([]string(nil), mockClaudeInit...)
	secondFrames = append(secondFrames,
		`<{"type":"system","subtype":"init","session_id":"native-retry","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	provider := newTestClaudeProviderSequence(
		t,
		prepareClaudePeer(t, "passed", firstFrames...),
		prepareClaudePeer(t, "passed", secondFrames...),
	)

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, agentclaude.ErrorReasonSessionCWDMismatch, classified.Reason)

	session, err = provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, "native-retry", session.ID())

	require.Error(t, provider.Stop(ctx))
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}
