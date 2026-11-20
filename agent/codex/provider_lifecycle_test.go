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
	`>{"id":91,"method":"initialize","params":{"capabilities":{"experimentalApi":true},"clientInfo":{"name":"agent-codex-test","version":"1"}}}`,
	`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
	`>{"method":"initialized"}`,
}

func TestProviderStartOwnsOneConnectionAndStopIsTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t, mockCodexInit...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	capabilities, err := provider.Start(ctx)
	require.NoError(t, err)
	require.NoError(t, capabilities.Validate())
	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))
	assert.True(t, capabilities.Supports(agent.CapabilityPermission))
	assert.True(t, capabilities.Supports(agent.CapabilityForm))

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
	require.NoError(t, session.Capabilities().Validate())
	assert.True(t, session.Capabilities().Supports(agent.CapabilityTextInput))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityPermission))
	assert.True(t, session.Capabilities().Supports(agent.CapabilityForm))
	assert.Equal(t, agent.EffectiveFeatures{AgentQuestions: true}, session.EffectiveFeatures())

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
		require.NoError(t, ctx.Err())
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

func TestNaturalTerminationReleasesProviderLeaseAndKeepsEventsDrainable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item-a","delta":"kept"}}`,
		`<{"method":"item/agentMessage/delta","params":{"threadId":"root-a","turnId":"turn-a","itemId":"item-a"}}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
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
	assert.Equal(t, agent.EventAgentMessageChunk, event.Type())
	_, err = stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	assert.Same(t, session.Err(), err)

	require.NoError(t, session.Close(ctx))
}

func codexThreadStartResult(threadID string) string {
	return codexPermissionResult(threadID, "on-request", "auto_review", "workspaceWrite")
}

func codexPermissionResult(threadID, approvalPolicy, reviewer, sandbox string) string {
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
		"approvalPolicy": "` + approvalPolicy + `",
		"approvalsReviewer": "` + reviewer + `",
		"cwd": "/synthetic",
		"model": "synthetic",
		"modelProvider": "synthetic",
		"sandbox": {"type": "` + sandbox + `"}
	}`
}

func newTestProvider(t *testing.T, peerSession *mocktest.Session) *agentcodex.Core {
	return newTestProviderWithExperimentalAPI(t, peerSession, true)
}

func newTestProviderWithExperimentalAPI(
	t *testing.T,
	peerSession *mocktest.Session,
	enabled bool,
) *agentcodex.Core {
	t.Helper()
	return newTestProviderWithInitialize(t, peerSession, codextypes.InitializeParams{
		ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
		Capabilities: codextypes.Some(codextypes.InitializeCapabilities{
			ExperimentalAPI: codextypes.Some(enabled),
		}),
	})
}

func newTestProviderWithInitialize(
	t *testing.T,
	peerSession *mocktest.Session,
	initialize codextypes.InitializeParams,
) *agentcodex.Core {
	t.Helper()
	return newTestProviderWithWrappers(
		t,
		peerSession,
		initialize,
		testStreamWrappers{},
	)
}

type testStreamWrappers struct {
	reader    func(io.ReadCloser) io.ReadCloser
	writer    func(io.WriteCloser) io.WriteCloser
	tracer    agent.Tracer
	configure func(*agentcodex.Config)
}

func newTestProviderWithWrappers(
	t *testing.T,
	peerSession *mocktest.Session,
	initialize codextypes.InitializeParams,
	wrappers testStreamWrappers,
) *agentcodex.Core {
	t.Helper()
	runCtx, cancelRun := context.WithTimeout(
		context.WithoutCancel(t.Context()),
		30*time.Second,
	)
	// This is an embedded synthetic fixture, not a native agent process.
	args := []string{"app-server", "--listen", "stdio://"}
	run, err := peerSession.NewRun(runCtx, mocktest.RunOptions{NativeArgs: args})
	require.NoError(t, err)
	require.Equal(t, args, run.Start.NativeArgs)
	reader, writer := run.Reader, run.Writer
	if wrappers.reader != nil {
		reader = wrappers.reader(reader)
	}
	if wrappers.writer != nil {
		writer = wrappers.writer(writer)
	}
	var completion <-chan error
	config := agentcodex.Config{Stdio: codex.StdioOptions{
		Args:       run.Start.NativeArgs,
		Env:        run.Environment(),
		Dir:        run.Start.ProtocolCwd,
		Initialize: initialize,
	}, Tracer: wrappers.tracer}
	if wrappers.configure != nil {
		wrappers.configure(&config)
	}
	provider := agentcodex.NewWithStream(config, codex.StreamTransport{
		Reader: reader,
		Writer: writer,
		Stderr: run.Stderr,
		Wait:   run.WaitError,
	}, func(client *codex.Client) {
		completion = run.CloseInputWhenComplete(client.CloseInput)
	})
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = provider.Stop(stopCtx)
		_ = run.CloseSDK()
		waitCtx, stopWait := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopWait()
		_, _ = run.Wait(waitCtx)
		if completion != nil {
			select {
			case err := <-completion:
				if err != nil {
					assert.ErrorIs(t, err, codex.ErrInputClosed, "provider Stop may close input before the watcher")
				}
			case <-waitCtx.Done():
				t.Error("input completion watcher did not finish")
			}
		}
		cancelRun()
	})
	return provider
}

func prepareCodexPeer(t *testing.T, frames ...string) *mocktest.Session {
	t.Helper()
	return prepareCodexPeerWithStatus(t, "passed", frames...)
}

func prepareCodexPeerWithStatus(
	t *testing.T,
	wantStatus string,
	frames ...string,
) *mocktest.Session {
	t.Helper()
	return prepareCodexPeerWithFrameDelays(t, wantStatus, nil, frames...)
}

func prepareCodexPeerWithFrameDelays(
	t *testing.T,
	wantStatus string,
	delays map[int]time.Duration,
	frames ...string,
) *mocktest.Session {
	t.Helper()
	withDefaults := make([]string, len(frames))
	for index, frame := range frames {
		withDefaults[index] = expectDefaultCodexPermissions(t, frame)
	}
	return prepareCodexPeerWithExactFrames(t, wantStatus, delays, withDefaults...)
}

// expectDefaultCodexPermissions adds the fixed unified defaults to fixtures
// for tests that exercise other session settings. The peer still compares all
// arguments. Permission-specific tests supply their complete wire frames.
func expectDefaultCodexPermissions(t *testing.T, frame string) string {
	t.Helper()
	if frame[0] != '>' {
		return frame
	}
	var request map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(frame[1:]), &request))
	switch string(request["method"]) {
	case `"thread/start"`, `"thread/resume"`:
		request["params"] = withDefaultCodexPermissionParams(t, request["params"])
		data, err := json.Marshal(request)
		require.NoError(t, err)
		return ">" + string(data)
	default:
		return frame
	}
}

func withDefaultCodexPermissionParams(t *testing.T, data json.RawMessage) json.RawMessage {
	t.Helper()
	var params map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &params))
	for field, value := range map[string]json.RawMessage{
		"approvalPolicy":    json.RawMessage(`"on-request"`),
		"approvalsReviewer": json.RawMessage(`"auto_review"`),
		"sandbox":           json.RawMessage(`"workspace-write"`),
	} {
		require.NotContains(t, params, field)
		params[field] = value
	}
	expected, err := json.Marshal(params)
	require.NoError(t, err)
	return expected
}

func prepareCodexPeerWithExactFrames(
	t *testing.T,
	wantStatus string,
	delays map[int]time.Duration,
	frames ...string,
) *mocktest.Session {
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
		if delay := delays[index]; delay > 0 {
			item["delayMs"] = delay.Milliseconds()
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
		assert.Equal(t, wantStatus, report.Status)
		require.NoError(t, peerSession.Close())
	})
	return peerSession
}

func closeSessionOnCleanup(t *testing.T, session agent.Session) {
	t.Helper()
	t.Cleanup(func() {
		assert.NoError(t, session.Close(context.Background()))
	})
}
