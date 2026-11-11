package agentclaude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func TestOpenAssignsNativeUUIDBeforeAnyUserInput(t *testing.T) {
	for _, submit := range []bool{false, true} {
		t.Run(map[bool]string{false: "close without input", true: "one input"}[submit], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			argvPath := filepath.Join(t.TempDir(), "argv.json")
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD: cwd, ArgvPath: argvPath, WaitForUser: true,
				Frames: []string{
					`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","claude_code_version":"2.1.258","slash_commands":["help"]}`,
					`{"type":"assistant","session_id":"__SESSION_ID__","message":{"id":"answer","content":[{"type":"text","text":"answer","apiBlockIndex":0}]}}`,
					`{"type":"result","subtype":"success","session_id":"__SESSION_ID__","is_error":false,"result":"answer"}`,
					`{"type":"system","subtype":"session_state_changed","session_id":"__SESSION_ID__","state":"idle"}`,
				},
			})
			provider := agentclaude.New(agentclaude.Config{CLIPath: peer.CLIPath, Env: peer.Env})
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = provider.Stop(context.Background()) })
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			initialID := session.ID()
			require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, initialID)
			argv := readClaudeArgv(t, argvPath)
			assert.Contains(t, argv, "--session-id="+initialID)
			for _, arg := range argv {
				assert.False(t, strings.HasPrefix(arg, "--resume"))
			}
			observation := readOpenPeerObservation(t, peer.Observation)
			assert.Equal(t, initialID, observation.SessionID)
			assert.Empty(t, observation.UserFrames)
			assert.Empty(t, observation.CallbackID)
			assert.False(t, agentclaude.InjectSupportedForTest(session))
			rejected, err := session.(agent.Injector).Inject(ctx, claudeTextInput("before init"))
			assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(false)}, rejected.Acceptance)
			stream, err := session.Events()
			require.NoError(t, err)
			requireStartupForwardingGap(t, ctx, stream)
			if submit {
				receipt, err := session.StartOrSteer(ctx, agent.Input{
					Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "one input"}}},
				})
				require.NoError(t, err)
				assert.Equal(t, agent.InputAppliedBestEffort, receipt.Result)
				assert.Nil(t, receipt.Acceptance.NativeAccepted)
				seenCommands, seenAnswer := false, false
				for {
					event, err := stream.Next(ctx)
					require.NoError(t, err)
					assert.Equal(t, initialID, event.SessionID)
					switch payload := event.Payload.(type) {
					case agent.AvailableCommandsUpdatePayload:
						assert.Equal(t, []agent.AvailableCommand{{Name: "help"}}, payload.AvailableCommands)
						seenCommands = true
					case agent.AgentMessage:
						seenAnswer = true
					case agent.StateUpdatePayload:
						if payload.State != nil && *payload.State == agent.StateIdle {
							require.True(t, seenCommands)
							require.True(t, seenAnswer)
							goto completed
						}
					}
				}
			}
		completed:
			assert.Equal(t, submit, agentclaude.InjectSupportedForTest(session))
			var closes sync.WaitGroup
			for range 3 {
				closes.Go(func() { assert.NoError(t, session.Close(ctx)) })
			}
			closes.Wait()
			require.NoError(t, provider.Stop(ctx))
			observation = readOpenPeerObservation(t, peer.Observation)
			if submit {
				require.Len(t, observation.UserFrames, 1)
				var wire struct {
					Type    string `json:"type"`
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				}
				require.NoError(t, json.Unmarshal(observation.UserFrames[0], &wire))
				assert.Equal(t, "user", wire.Type)
				assert.Equal(t, "one input", wire.Message.Content)
			} else {
				assert.Empty(t, observation.UserFrames)
			}
			assert.Equal(t, initialID, session.ID())
			assert.NoError(t, session.Err())
		})
	}
}

func TestAssignedSessionRejectsLaterNativeIdentityConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		frames []string
		reason string
	}{
		{"init identity", []string{`{"type":"system","subtype":"init","session_id":"other","cwd":"__CWD__"}`}, agentclaude.ErrorReasonSessionIdentityMismatch},
		{"assistant identity", []string{`{"type":"assistant","session_id":"other","message":{"id":"foreign","content":[]}}`}, agentclaude.ErrorReasonSessionIdentityMismatch},
		{"result identity", []string{`{"type":"result","subtype":"success","session_id":"other","is_error":false}`}, agentclaude.ErrorReasonSessionIdentityMismatch},
		{"replay init", []string{`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","isReplay":true}`}, agentclaude.ErrorReasonInitializeReplayed},
		{"cwd", []string{`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"/nonexistent-assigned-cwd"}`}, agentclaude.ErrorReasonSessionCWDMismatch},
		{"duplicate", []string{
			`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","claude_code_version":"2.1.258"}`,
			`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__"}`,
		}, agentclaude.ErrorReasonInitializeDuplicate},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, WaitForUser: true, Frames: test.frames})
			provider := agentclaude.New(agentclaude.Config{CLIPath: peer.CLIPath, Env: peer.Env})
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = provider.Stop(context.Background()) })
			session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close(context.Background()) })
			initialID := session.ID()
			stream, err := session.Events()
			require.NoError(t, err)
			requireStartupForwardingGap(t, ctx, stream)
			_, err = session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "trigger"}}}})
			require.NoError(t, err)
			select {
			case <-session.Done():
			case <-ctx.Done():
				t.Fatal("identity conflict did not terminate the session")
			}
			assert.Equal(t, initialID, session.ID())
			var classified *agent.Error
			require.ErrorAs(t, session.Err(), &classified)
			assert.Equal(t, agent.ErrorNativeRejected, classified.Code)
			assert.Equal(t, test.reason, classified.Reason)
			for {
				_, err = stream.Next(ctx)
				if err != nil {
					break
				}
			}
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
			assert.Error(t, session.Close(ctx))
			require.NoError(t, provider.Stop(ctx))
		})
	}
}

func TestAssignedSessionAcceptsCanonicalInitCwd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(cwd, alias))
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD: cwd, WaitForUser: true,
		Frames: []string{`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","claude_code_version":"2.1.258","slash_commands":["help"]}`},
	})
	provider := agentclaude.New(agentclaude.Config{CLIPath: peer.CLIPath, Env: peer.Env})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: alias})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	requireStartupForwardingGap(t, ctx, stream)
	_, err = session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "trigger"}}}})
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.IsType(t, agent.AvailableCommandsUpdatePayload{}, event.Payload)
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}

func TestAssignedSessionRoutesEarlyMessagesAndOriginalRequestWithoutInit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		CWD: cwd,
		BeforeInput: []string{
			`{"type":"assistant","session_id":"__SESSION_ID__","message":{"id":"early","content":[{"type":"text","text":"early","apiBlockIndex":0}]}}`,
			`{"type":"system","subtype":"session_state_changed","session_id":"__SESSION_ID__","state":"running"}`,
			strings.TrimPrefix(claudePermissionRequestFrame("early-permission", "Read", "early-read", `{"file_path":"a.txt"}`), "<"),
		},
	})
	provider := agentclaude.New(agentclaude.Config{CLIPath: peer.CLIPath, Env: peer.Env})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	requireStartupForwardingGap(t, ctx, stream)
	early, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, session.ID(), early.SessionID)
	assert.Equal(t, "early:0", early.ItemID)
	assert.Equal(t, agent.EventAgentMessage, early.Type())
	var requested agent.InteractionRequestedPayload
	for requested.Interaction == "" {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		assert.Equal(t, session.ID(), event.SessionID)
		if payload, ok := event.Payload.(agent.InteractionRequestedPayload); ok {
			requested = payload
		}
	}
	receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome: agent.PermissionSelected, OptionID: "allow-once",
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(peer.Observation)
		var observation claudeResumePeerObservation
		return err == nil && json.Unmarshal(data, &observation) == nil && len(observation.Responses) == 1
	}, 3*time.Second, time.Millisecond)
	observation := readOpenPeerObservation(t, peer.Observation)
	var response struct {
		Response struct {
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior string `json:"behavior"`
			} `json:"response"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(observation.Responses[0], &response))
	assert.Equal(t, "early-permission", response.Response.RequestID)
	assert.Equal(t, "allow", response.Response.Response.Behavior)
	assert.Empty(t, observation.UserFrames)
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}

func readOpenPeerObservation(t *testing.T, path string) claudeResumePeerObservation {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var observation claudeResumePeerObservation
	require.NoError(t, json.Unmarshal(data, &observation))
	return observation
}

func requireStartupForwardingGap(t *testing.T, ctx context.Context, stream agent.EventStream) {
	t.Helper()
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	payload, ok := event.Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	require.Equal(t, "native_relationship_gap", payload.Reason)
	meta, ok := event.Meta["claude"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "forwarding-not-confirmed", meta["observationReason"])
	require.Equal(t, "initialize-forwardSubagentText-is-a-request", meta["evidence"])
}

func TestAssignedSessionForwardingCoverageFlowsThroughUnifiedEvents(t *testing.T) {
	for _, test := range []struct {
		name       string
		initialize *claudetypes.InitializeOptions
		requested  bool
	}{
		{"default", nil, true},
		{"enabled", &claudetypes.InitializeOptions{ForwardSubagentText: new(true)}, true},
		{"disabled", &claudetypes.InitializeOptions{ForwardSubagentText: new(false)}, false},
		{"omitted", &claudetypes.InitializeOptions{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{
				CWD: cwd,
				Frames: []string{
					`{"type":"system","subtype":"init","session_id":"__SESSION_ID__","cwd":"__CWD__","claude_code_version":"2.1.258","slash_commands":["help"]}`,
				},
			})
			core := agentclaude.New(agentclaude.Config{
				CLIPath: peer.CLIPath, Env: peer.Env, Initialize: test.initialize,
				Subagents: claude.SubagentOptions{History: []claude.SubagentHistory{
					{SessionID: claudeResumePeerSessionID, TaskID: "parent"},
					{SessionID: claudeResumePeerSessionID, TaskID: "child", ParentTaskID: "parent"},
				}},
			})
			agentclaude.SetSessionIDGeneratorForTest(core, func() (string, error) {
				return claudeResumePeerSessionID, nil
			})
			_, err := core.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, core.Stop(context.Background())) })
			session, err := core.Open(ctx, agent.OpenOptions{Cwd: cwd})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			require.Equal(t, claudeResumePeerSessionID, session.ID())
			stream, err := session.Events()
			require.NoError(t, err)
			if test.requested {
				requireStartupForwardingGap(t, ctx, stream)
			}
			event, err := stream.Next(ctx)
			require.NoError(t, err)
			require.Equal(t, session.ID(), event.SessionID)
			require.IsType(t, agent.AvailableCommandsUpdatePayload{}, event.Payload)
			assert.Empty(t, readOpenPeerObservation(t, peer.Observation).UserFrames)
			require.NoError(t, session.Close(ctx))
			require.NoError(t, core.Stop(ctx))
		})
	}
}

func TestOpenInitializeFailureReleasesProvider(t *testing.T) {
	for _, mode := range []string{"rejection", "timeout", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			scenario := claudeResumePeerScenario{CWD: t.TempDir(), WaitForUser: true}
			timeout := 3 * time.Second
			if mode == "rejection" {
				scenario.InitializeError = true
			} else {
				scenario.InitializeDelay = 10000
				if mode == "timeout" {
					timeout = 500 * time.Millisecond
				}
			}
			peer := newClaudeResumePeer(t, scenario)
			provider := agentclaude.New(agentclaude.Config{
				CLIPath: peer.CLIPath, Env: peer.Env, InitializeTimeout: timeout,
				ShutdownTimeout: 300 * time.Millisecond,
			})
			_, err := provider.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = provider.Stop(context.Background()) })
			openCtx, cancelOpen := context.WithCancel(ctx)
			defer cancelOpen()
			if mode == "cancellation" {
				go func() {
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						if _, err := os.Stat(peer.Observation); err == nil {
							cancelOpen()
							return
						}
						select {
						case <-ticker.C:
						case <-ctx.Done():
							return
						}
					}
				}()
			}
			session, err := provider.Open(openCtx, agent.OpenOptions{Cwd: scenario.CWD})
			require.Error(t, err)
			assert.Nil(t, session)
			require.NoError(t, provider.Stop(ctx))
			if _, err := os.Stat(peer.Observation); err == nil {
				assert.Empty(t, readOpenPeerObservation(t, peer.Observation).UserFrames)
			}
		})
	}
}
