package agentcodex_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

var mockCodexInit = []string{
	`>{"id":91,"method":"initialize","params":{"clientInfo":{"name":"agent-codex-test","version":"1"}}}`,
	`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
	`>{"method":"initialized"}`,
}

func TestProviderStartOwnsOneConnectionAndStopIsTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t, mockCodexInit...)
	provider := agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
		Executable: mustCommand(t, peerSession),
		Args:       mustCommandArgs(t, peerSession),
		Env:        peerSession.Env,
		Dir:        peerSession.Workspace,
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
		},
	}})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	capabilities, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))

	repeated, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, capabilities, repeated)

	client, err := provider.NativeClient()
	require.NoError(t, err)
	require.NotNil(t, client)
	same, err := provider.NativeClient()
	require.NoError(t, err)
	assert.Same(t, client, same)

	require.NoError(t, provider.Stop(ctx))
	_, err = provider.NativeClient()
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	require.NoError(t, provider.Stop(ctx))
	_, err = provider.Start(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestProviderOpenHasOneEventStreamAndCloseKeepsProviderHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, "root-a", session.ID())
	assert.True(t, session.Capabilities().Supports(agent.CapabilityTextInput))

	stream, err := session.Events()
	require.NoError(t, err)
	require.NotNil(t, stream)
	_, err = session.Events()
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))

	require.NoError(t, session.Close(ctx))
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assert.NoError(t, session.Err())
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, session.Close(ctx))
	_, err = session.Events()
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))

	client, err := provider.NativeClient()
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestProviderStopRejectsOpenSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	err = provider.Stop(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}

func codexThreadStartResult(threadID string) string {
	return `{
		"thread": {
			"id": "` + threadID + `",
			"sessionId": "synthetic",
			"cliVersion": "0.162.1",
			"createdAt": 1,
			"updatedAt": 1,
			"cwd": "/synthetic",
			"ephemeral": false,
			"modelProvider": "synthetic",
			"preview": "",
			"projectId": null,
			"source": "appServer",
			"status": {"type": "idle"},
			"turns": []
		},
		"approvalPolicy": "never",
		"approvalsReviewer": "user",
		"cwd": "/synthetic",
		"model": "synthetic",
		"modelProvider": "synthetic",
		"sandbox": {"type": "readOnly"}
	}`
}

func newTestProvider(t *testing.T, peerSession *mocktest.Session) *agentcodex.Provider {
	t.Helper()
	return agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
		Executable: mustCommand(t, peerSession),
		Args:       mustCommandArgs(t, peerSession),
		Env:        peerSession.Env,
		Dir:        peerSession.Workspace,
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
		},
	}})
}

func prepareCodexPeer(t *testing.T, frames ...string) *mocktest.Session {
	t.Helper()
	scenario := make([]map[string]any, 0, len(frames))
	for index, frame := range frames {
		direction, data := frame[:1], frame[1:]
		item := map[string]any{
			"ordinal":   index + 1,
			"epoch":     1,
			"channel":   "protocol",
			"direction": "s2c",
		}
		if direction == ">" {
			item["direction"] = "c2s"
		}
		if direction == "!" {
			item["disconnect"] = true
		} else {
			item["wire"] = json.RawMessage(data)
		}
		scenario = append(scenario, item)
	}
	data, err := json.Marshal(map[string]any{
		"version":    1,
		"provenance": "synthetic",
		"frames":     scenario,
	})
	require.NoError(t, err)
	peerSession, err := mocktest.Prepare(t.TempDir(), "codex", data, mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() {
		report, reportErr := peerSession.Report()
		require.NoError(t, reportErr)
		require.NotNil(t, report)
		assert.Equal(t, "passed", report.Status)
		require.NoError(t, peerSession.Close())
	})
	return peerSession
}

func mustCommand(t *testing.T, session *mocktest.Session) string {
	t.Helper()
	executable, _, err := session.Command("main")
	require.NoError(t, err)
	return executable
}

func mustCommandArgs(t *testing.T, session *mocktest.Session) []string {
	t.Helper()
	_, args, err := session.Command("main")
	require.NoError(t, err)
	return args
}

func closeSessionOnCleanup(t *testing.T, session agent.Session) {
	t.Helper()
	t.Cleanup(func() {
		assert.NoError(t, session.Close(context.Background()))
	})
}
