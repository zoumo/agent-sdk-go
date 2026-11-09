package agentclaude_test

import (
	"context"
	"encoding/json"
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
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

var mockClaudeInit = []string{
	`>{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`,
	`<{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`,
}

func TestOpenConfirmsInitAndCloseKeepsProviderHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-open","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258","future":{"precise":9007199254740993}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, "native-open", session.ID())
	assert.True(t, session.Capabilities().Supports(agent.CapabilityTextInput))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityPermission))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityForm))
	assert.Equal(t, agent.EffectiveFeatures{AgentQuestions: true}, session.EffectiveFeatures())

	stream, err := session.Events()
	require.NoError(t, err)
	_, err = session.Events()
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	err = provider.Stop(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	assert.NoError(t, session.Err())
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, session.Close(ctx))

	repeated, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.True(t, repeated.Supports(agent.CapabilityTextInput))
	require.NoError(t, provider.Stop(ctx))
}

func TestClosingOneSessionKeepsOtherSessionAndProviderHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	firstFrames := append([]string(nil), mockClaudeInit...)
	firstFrames = append(firstFrames,
		`<{"type":"system","subtype":"init","session_id":"native-first","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-first","message":{"id":"first-message","content":[{"type":"text","text":"first","apiBlockIndex":0}]}}`,
	)
	secondFrames := append([]string(nil), mockClaudeInit...)
	secondFrames = append(secondFrames,
		`<{"type":"system","subtype":"init","session_id":"native-second","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"native-second","message":{"id":"second-message","content":[{"type":"text","text":"second","apiBlockIndex":0}]}}`,
	)
	firstPeer := prepareClaudePeer(t, "passed", firstFrames...)
	secondPeer := prepareClaudePeer(t, "passed", secondFrames...)
	provider := newTestClaudeProviderSequence(t, firstPeer, secondPeer)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	first, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	second, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, second)
	assert.Equal(t, "native-first", first.ID())
	assert.Equal(t, "native-second", second.ID())

	stream, err := second.Events()
	require.NoError(t, err)
	require.NoError(t, first.Close(ctx))
	select {
	case <-second.Done():
		require.Fail(t, "closing one session terminated another")
	default:
	}

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "second-message:0", event.ItemID)

	stopErr := provider.Stop(ctx)
	require.Error(t, stopErr)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(stopErr))
	require.NoError(t, second.Close(ctx))
}

func TestOpenWaitsForInitWithinConfiguredTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cwd := t.TempDir()
	peerSession := prepareClaudePeerWithReplayTimeout(
		t,
		"passed",
		// The peer must stay open long enough to observe Close's stdin EOF
		// after the provider's separate startup budget expires.
		30*time.Second,
		mockClaudeInit...,
	)
	provider := newTestClaudeProviderWithConfig(t, agentclaude.Config{
		// This budget also bounds the native initialize control handshake.
		// Keep it well above mock process-startup variance under race so the
		// timeout proves that the separate system/init frame never arrived.
		InitializeTimeout: 10 * time.Second,
	}, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	_, err = provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.Error(t, err)
	assert.Equalf(t, agent.ErrorUnavailable, agent.CodeOf(err), "Open error: %v", err)
}

func newTestClaudeProvider(
	t *testing.T,
	peerSession *mocktest.Session,
) *agentclaude.Provider {
	t.Helper()
	return newTestClaudeProviderWithConfig(t, agentclaude.Config{}, peerSession)
}

func newTestClaudeProviderWithConfig(
	t *testing.T,
	config agentclaude.Config,
	peers ...*mocktest.Session,
) *agentclaude.Provider {
	t.Helper()
	var mu sync.Mutex
	next := 0
	provider := agentclaude.NewWithStream(config, func(ctx context.Context) (agentclaude.TestStreamOpen, error) {
		mu.Lock()
		if next >= len(peers) {
			mu.Unlock()
			return agentclaude.TestStreamOpen{}, errors.New("unexpected Claude session")
		}
		peer := peers[next]
		next++
		mu.Unlock()
		runCtx, cancelRun := context.WithTimeout(
			context.WithoutCancel(ctx),
			30*time.Second,
		)
		run, err := peer.NewRun(runCtx, mocktest.RunOptions{})
		if err != nil {
			cancelRun()
			return agentclaude.TestStreamOpen{}, err
		}
		return agentclaude.TestStreamOpen{
			Transport: claude.StreamTransport{
				Reader: run.Reader,
				Writer: run.Writer,
				Stderr: run.Stderr,
				Wait:   run.WaitError,
			},
			Workspace: run.Start.Workspace,
			Env:       run.Environment(),
			Started: func(session *claude.Session) {
				run.CloseInputWhenComplete(session.CloseInput)
				t.Cleanup(func() {
					_ = session.Close()
					_ = run.CloseSDK()
					cancelRun()
				})
			},
		}, nil
	})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = provider.Stop(stopCtx)
	})
	return provider
}

func newTestClaudeProviderSequence(t *testing.T, peers ...*mocktest.Session) *agentclaude.Provider {
	t.Helper()
	return newTestClaudeProviderWithConfig(t, agentclaude.Config{}, peers...)
}

func prepareClaudePeer(
	t *testing.T,
	wantStatus string,
	frames ...string,
) *mocktest.Session {
	return prepareClaudePeerWithReplayTimeout(t, wantStatus, 0, frames...)
}

func prepareClaudePeerWithReplayTimeout(
	t *testing.T,
	wantStatus string,
	timeout time.Duration,
	frames ...string,
) *mocktest.Session {
	t.Helper()
	scenario := make([]map[string]any, 0, len(frames))
	for index, frame := range frames {
		direction, data := frame[:1], frame[1:]
		item := map[string]any{"ordinal": index + 1, "channel": "protocol"}
		item["direction"] = map[string]string{
			">": "client",
			"<": "server",
			"!": "disconnect",
		}[direction]
		if direction != "!" {
			item["data"] = json.RawMessage(data)
		}
		scenario = append(scenario, item)
	}
	envelope := map[string]any{
		"version":    1,
		"provenance": "synthetic",
		"frames":     scenario,
	}
	if timeout > 0 {
		envelope["timeoutMs"] = timeout.Milliseconds()
	}
	data, err := json.Marshal(envelope)
	require.NoError(t, err)
	peerSession, err := mocktest.Prepare(t.TempDir(), "claude", data, mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() {
		report, reportErr := peerSession.Report()
		require.NoError(t, reportErr)
		require.NotNil(t, report)
		assert.Equalf(t, wantStatus, report.Status, "mock report: %+v", report)
		require.NoError(t, peerSession.Close())
	})
	return peerSession
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func closeSessionOnCleanup(t *testing.T, session agent.Session) {
	t.Helper()
	t.Cleanup(func() {
		assert.NoError(t, session.Close(context.Background()))
	})
}
