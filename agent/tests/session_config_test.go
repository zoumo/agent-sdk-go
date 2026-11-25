package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/client/codex"
	codextypes "github.com/zoumo/agent-sdk-go/client/codex/types"
)

const (
	agentExecutionSessionID = "44444444-4444-4444-8444-444444444444"
	agentExecutionSuccess   = "agent-session-config-success"
	agentExecutionActive    = "agent-session-config-active"
	agentExecutionResume    = "agent-session-config-resume"
	agentExecutionReject    = "agent-session-config-reject"
	agentExecutionUnknown   = "agent-session-config-unknown"
)

func TestAgentSessionConfigRuntimeAcrossProviders(t *testing.T) {
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tracePath := agentExecutionTracePath(t)
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionSuccess,
			agent.SessionConfig{
				Model:  " Caller/Model v2 ",
				Effort: " Native/High ",
			},
		)
		controller, ok := session.(agent.ConfigController)
		require.True(t, ok)

		stream, err := session.Events()
		require.NoError(t, err)
		submitAgentExecutionInput(t, ctx, session, stream, "before/update")

		effortChange := agent.ConfigChange{
			ID:    agent.ConfigEffort,
			Value: " next/effort ",
		}
		effortReceipt, err := controller.SetConfig(ctx, effortChange)
		require.NoError(t, err)
		assert.Equal(t, effortChange, effortReceipt.Change)
		assertAgentExecutionAccepted(t, effortReceipt.Acceptance)

		submitAgentExecutionInput(t, ctx, session, stream, "after/effort")

		modelChange := agent.ConfigChange{
			ID:    agent.ConfigModel,
			Value: " next/model ",
		}
		modelReceipt, err := controller.SetConfig(ctx, modelChange)
		require.NoError(t, err)
		assert.Equal(t, modelChange, modelReceipt.Change)
		assertAgentExecutionAccepted(t, modelReceipt.Acceptance)

		submitAgentExecutionInput(t, ctx, session, stream, "after/model")
		assertAgentExecutionRuntimeTrace(t, providerName, tracePath)

		require.NoError(t, session.Close(ctx))
		closedChange := agent.ConfigChange{
			ID:    agent.ConfigModel,
			Value: " after/close ",
		}
		closedReceipt, err := controller.SetConfig(ctx, closedChange)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
		assert.Equal(t, closedChange, closedReceipt.Change)
		require.NotNil(t, closedReceipt.Acceptance.Sent)
		assert.False(t, *closedReceipt.Acceptance.Sent)
		assert.Nil(t, closedReceipt.Acceptance.NativeAccepted)
	})
}

func TestAgentSessionConfigUpdatesActiveWorkAcrossProviders(t *testing.T) {
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tracePath := agentExecutionTracePath(t)
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionActive,
			agent.SessionConfig{
				Model:  "initial/model",
				Effort: "initial-effort",
			},
		)
		controller, ok := session.(agent.ConfigController)
		require.True(t, ok)
		stream, err := session.Events()
		require.NoError(t, err)
		initialID := session.ID()

		submitted, err := session.StartOrSteer(ctx, agent.Input{
			Content: []agent.ContentBlock{
				{Text: &agent.ContentBlockText{Text: "active/before"}},
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, submitted.Submission)
		awaitAgentExecutionState(t, ctx, stream, agent.StateRunning)

		change := agent.ConfigChange{
			ID:    agent.ConfigModel,
			Value: "active/model",
		}
		receipt, err := controller.SetConfig(ctx, change)
		require.NoError(t, err)
		assert.Equal(t, change, receipt.Change)
		assertAgentExecutionAccepted(t, receipt.Acceptance)

		awaitAgentExecutionIdle(t, ctx, stream)
		submitAgentExecutionInput(t, ctx, session, stream, "active/after")
		assert.Equal(t, initialID, session.ID())
		assertAgentExecutionActiveConfigTrace(t, providerName, tracePath)
		require.NoError(t, session.Close(ctx))
	})
}

func TestAgentSessionConfigResumeAcrossProviders(t *testing.T) {
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tracePath := agentExecutionTracePath(t)
		cwd := t.TempDir()
		configDir := ""
		if providerName == "claude" {
			configDir = t.TempDir()
			writeAgentExecutionResumeTranscript(t, configDir, cwd)
		}
		provider := newAgentExecutionProvider(
			t,
			providerName,
			agentExecutionResume,
			configDir,
		)
		t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
		_, err := provider.Start(ctx)
		require.NoError(t, err)
		session, err := provider.Resume(ctx, agent.ResumeOptions{
			SessionID: agentExecutionSessionID,
			OpenOptions: agent.OpenOptions{
				Cwd: cwd,
				Config: agent.SessionConfig{
					Model:  " Resume/Model ",
					Effort: " Native/Resume ",
				},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			closeAgentExecutionSession(t, session, false, false)
		})

		assert.Equal(t, agentExecutionSessionID, session.ID())
		assertAgentExecutionResumeTrace(t, providerName, tracePath)
		require.NoError(t, session.Close(ctx))
	})
}

func TestAgentSessionConfigNativeRejectionAcrossProviders(t *testing.T) {
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionReject,
			agent.SessionConfig{
				Model:  "initial/model",
				Effort: "initial-effort",
			},
		)
		controller, ok := session.(agent.ConfigController)
		require.True(t, ok)

		assertRejected := func(change agent.ConfigChange) {
			receipt, err := controller.SetConfig(ctx, change)
			require.Error(t, err)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
			assert.Equal(t, change, receipt.Change)
			require.NotNil(t, receipt.Acceptance.Sent)
			require.NotNil(t, receipt.Acceptance.NativeAccepted)
			assert.True(t, *receipt.Acceptance.Sent)
			assert.False(t, *receipt.Acceptance.NativeAccepted)
		}
		assertRejected(agent.ConfigChange{
			ID:    agent.ConfigEffort,
			Value: " rejected/effort ",
		})
		assertRejected(agent.ConfigChange{
			ID:    agent.ConfigModel,
			Value: " rejected/model ",
		})
		require.NoError(t, session.Close(ctx))
	})
}

func TestAgentSessionConfigUnknownOutcomeAcrossProviders(t *testing.T) {
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tests := []struct {
			name   string
			change agent.ConfigChange
		}{
			{
				name: "model",
				change: agent.ConfigChange{
					ID:    agent.ConfigModel,
					Value: " uncertain/model ",
				},
			},
			{
				name: "effort",
				change: agent.ConfigChange{
					ID:    agent.ConfigEffort,
					Value: " uncertain/effort ",
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				session := openAgentExecutionSession(
					t,
					ctx,
					providerName,
					agentExecutionUnknown,
					agent.SessionConfig{
						Model:  "initial/model",
						Effort: "initial-effort",
					},
				)
				controller, ok := session.(agent.ConfigController)
				require.True(t, ok)

				receipt, err := controller.SetConfig(ctx, test.change)
				require.Error(t, err)
				assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
				assert.Equal(t, test.change, receipt.Change)
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.True(t, *receipt.Acceptance.Sent)
				assert.Nil(t, receipt.Acceptance.NativeAccepted)
				closeAgentExecutionSession(
					t,
					session,
					true,
					providerName == "claude",
				)
			})
		}
	})
}

func forEachAgentExecutionProvider(
	t *testing.T,
	run func(*testing.T, string, context.Context),
) {
	t.Helper()
	for _, providerName := range []string{"codex", "claude"} {
		t.Run(providerName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			run(t, providerName, ctx)
		})
	}
}

type agentExecutionTracer struct {
	file    *os.File
	encoder *json.Encoder
}

func openAgentExecutionTracer() (*agentExecutionTracer, error) {
	path := os.Getenv("AGENT_EXECUTION_TRACE")
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &agentExecutionTracer{file: file, encoder: json.NewEncoder(file)}, nil
}

func (t *agentExecutionTracer) Record(kind string, body any) error {
	if t == nil {
		return nil
	}
	return t.encoder.Encode(map[string]any{"kind": kind, "body": body})
}

func (t *agentExecutionTracer) Close() error {
	if t == nil {
		return nil
	}
	return t.file.Close()
}

func agentExecutionCodexResult(sessionID, cwd string) map[string]any {
	return map[string]any{
		"thread": map[string]any{
			"id":            sessionID,
			"sessionId":     sessionID,
			"cliVersion":    "0.162.1",
			"createdAt":     1,
			"updatedAt":     2,
			"cwd":           cwd,
			"ephemeral":     false,
			"modelProvider": "synthetic",
			"preview":       "",
			"projectId":     nil,
			"source":        "appServer",
			"status":        map[string]any{"type": "idle"},
			"turns":         []any{},
		},
		"approvalPolicy":    "never",
		"approvalsReviewer": "user",
		"cwd":               cwd,
		"model":             "observed/model",
		"modelProvider":     "synthetic",
		"reasoningEffort":   "observed-effort",
		"sandbox":           map[string]any{"type": "dangerFullAccess"},
	}
}

func openAgentExecutionSession(
	t *testing.T,
	ctx context.Context,
	providerName string,
	scenario string,
	config agent.SessionConfig,
) agent.Session {
	t.Helper()
	provider := newAgentExecutionProvider(t, providerName, scenario, "")
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:    t.TempDir(),
		Config: config,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		closeAgentExecutionSession(
			t,
			session,
			scenario == agentExecutionUnknown,
			providerName == "claude" && scenario == agentExecutionUnknown,
		)
	})
	return session
}

func newAgentExecutionProvider(
	t *testing.T,
	providerName string,
	scenario string,
	configDir string,
) agent.Core {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	env := append(
		os.Environ(),
		"SDK_INTEGRATION_PEER="+providerName,
		"SDK_INTEGRATION="+scenario,
		"GORACE=atexit_sleep_ms=0",
	)
	switch providerName {
	case "codex":
		return agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
			Executable: executable,
			Env:        env,
			Dir:        t.TempDir(),
			Initialize: codextypes.InitializeParams{
				ClientInfo: codextypes.ClientInfo{
					Name:    "agent-session-config-integration",
					Version: "synthetic",
				},
			},
		}})
	case "claude":
		return agentclaude.New(agentclaude.Config{
			CLIPath:           executable,
			Env:               env,
			ConfigDir:         configDir,
			InitializeTimeout: 10 * time.Second,
			ShutdownTimeout:   5 * time.Second,
		})
	default:
		require.FailNow(t, "unknown provider", providerName)
		return nil
	}
}

func writeAgentExecutionResumeTranscript(t *testing.T, configDir, cwd string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonical, "-")
	directory := filepath.Join(configDir, "projects", key)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	source, err := json.Marshal(map[string]any{
		"type":      "user",
		"sessionId": agentExecutionSessionID,
		"cwd":       cwd,
		"message": map[string]any{
			"role":    "user",
			"content": "saved",
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, agentExecutionSessionID+".jsonl"),
		append(source, '\n'),
		0o600,
	))
}

func assertAgentExecutionAccepted(t *testing.T, acceptance agent.Acceptance) {
	t.Helper()
	require.NotNil(t, acceptance.Sent)
	require.NotNil(t, acceptance.NativeAccepted)
	assert.True(t, *acceptance.Sent)
	assert.True(t, *acceptance.NativeAccepted)
}

// closeAgentExecutionSession releases a session and verifies its local
// termination. An unknown-outcome peer exits without completing the native
// close handshake, so the session must report unavailable after peer
// termination. Claude's native close also reports unavailable; Codex reports a
// clean close while retaining the unavailable session error.
func closeAgentExecutionSession(
	t *testing.T,
	session agent.Session,
	expectPeerExit bool,
	wantCloseError bool,
) {
	t.Helper()
	if expectPeerExit {
		select {
		case <-session.Done():
		case <-time.After(5 * time.Second):
			t.Error("session did not terminate after the native peer exit")
		}
	}
	err := session.Close(context.Background())
	if expectPeerExit {
		require.Error(t, session.Err())
		assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(session.Err()))
	} else {
		assert.NoError(t, session.Err())
	}
	if wantCloseError {
		assert.Error(t, err)
		assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	} else {
		assert.NoError(t, err)
	}
	select {
	case <-session.Done():
	default:
		t.Error("session did not terminate after close")
	}
}

func awaitAgentExecutionIdle(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
) {
	t.Helper()
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		payload, ok := event.Payload.(agent.StateUpdatePayload)
		if ok && payload.State != nil && *payload.State == agent.StateIdle {
			return
		}
	}
}

func submitAgentExecutionInput(
	t *testing.T,
	ctx context.Context,
	session agent.Session,
	stream agent.EventStream,
	text string,
) {
	t.Helper()
	receipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: text}}},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, receipt.Submission)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	awaitAgentExecutionIdle(t, ctx, stream)
}

type agentExecutionTraceEntry struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

type agentExecutionTraceFrame struct {
	Method  string          `json:"method"`
	Type    string          `json:"type"`
	Params  json.RawMessage `json:"params"`
	Request struct {
		Subtype  string          `json:"subtype"`
		Model    string          `json:"model"`
		Settings json.RawMessage `json:"settings"`
	} `json:"request"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type agentExecutionFrameAt struct {
	index int
	frame agentExecutionTraceFrame
}

func agentExecutionTracePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session-config-trace.jsonl")
	t.Setenv("AGENT_EXECUTION_TRACE", path)
	return path
}

func readAgentExecutionTrace(
	t *testing.T,
	path string,
) []agentExecutionTraceEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries []agentExecutionTraceEntry
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry agentExecutionTraceEntry
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		entries = append(entries, entry)
	}
	require.NotEmpty(t, entries)
	return entries
}

func agentExecutionTraceArgv(
	t *testing.T,
	entries []agentExecutionTraceEntry,
) []string {
	t.Helper()
	var argvs [][]string
	for _, entry := range entries {
		if entry.Kind != "argv" {
			continue
		}
		var argv []string
		require.NoError(t, json.Unmarshal(entry.Body, &argv))
		argvs = append(argvs, argv)
	}
	require.Len(t, argvs, 1, "agent execution trace must contain one process argv")
	return argvs[0]
}

func agentExecutionTraceFrames(
	t *testing.T,
	entries []agentExecutionTraceEntry,
) []agentExecutionFrameAt {
	t.Helper()
	frames := make([]agentExecutionFrameAt, 0, len(entries))
	for index, entry := range entries {
		if entry.Kind != "frame" {
			continue
		}
		var frame agentExecutionTraceFrame
		require.NoError(t, json.Unmarshal(entry.Body, &frame))
		frames = append(frames, agentExecutionFrameAt{index: index, frame: frame})
	}
	return frames
}

func agentExecutionFramesMatching(
	frames []agentExecutionFrameAt,
	match func(agentExecutionTraceFrame) bool,
) []agentExecutionFrameAt {
	var matched []agentExecutionFrameAt
	for _, frame := range frames {
		if match(frame.frame) {
			matched = append(matched, frame)
		}
	}
	return matched
}

func awaitAgentExecutionState(
	t *testing.T,
	ctx context.Context,
	stream agent.EventStream,
	want agent.SessionState,
) {
	t.Helper()
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		payload, ok := event.Payload.(agent.StateUpdatePayload)
		if !ok || payload.State == nil {
			continue
		}
		if *payload.State == want {
			return
		}
	}
}

func assertAgentExecutionActiveConfigTrace(
	t *testing.T,
	providerName string,
	path string,
) {
	t.Helper()
	entries := readAgentExecutionTrace(t, path)
	_ = agentExecutionTraceArgv(t, entries)
	frames := agentExecutionTraceFrames(t, entries)
	switch providerName {
	case "codex":
		starts := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "thread/start"
		})
		turns := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "turn/start"
		})
		settings := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "thread/settings/update"
		})
		require.Len(t, starts, 1)
		require.Len(t, turns, 2)
		require.Len(t, settings, 1)
		requireAgentExecutionTurnText(t, turns[0].frame.Params, "active/before")
		requireAgentExecutionTurnText(t, turns[1].frame.Params, "active/after")
		require.Less(t, turns[0].index, settings[0].index)
		require.Less(t, settings[0].index, turns[1].index)
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(settings[0].frame.Params, &params))
		requireAgentExecutionString(t, params, "model", "active/model")
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "turn/interrupt"
		}))
	case "claude":
		users := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "user"
		})
		models := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "control_request" &&
				frame.Request.Subtype == "set_model"
		})
		require.Len(t, users, 2)
		require.Len(t, models, 1)
		requireAgentExecutionClaudeText(t, users[0].frame, "active/before")
		requireAgentExecutionClaudeText(t, users[1].frame, "active/after")
		require.Less(t, users[0].index, models[0].index)
		require.Less(t, models[0].index, users[1].index)
		assert.Equal(t, "active/model", models[0].frame.Request.Model)
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "control_request" &&
				frame.Request.Subtype == "interrupt"
		}))
	default:
		require.FailNow(t, "unknown provider", providerName)
	}
}

func assertAgentExecutionRuntimeTrace(t *testing.T, providerName, path string) {
	t.Helper()
	entries := readAgentExecutionTrace(t, path)
	_ = agentExecutionTraceArgv(t, entries)
	frames := agentExecutionTraceFrames(t, entries)
	switch providerName {
	case "codex":
		assertAgentExecutionCodexRuntimeTrace(t, frames)
	case "claude":
		assertAgentExecutionClaudeRuntimeTrace(t, entries, frames)
	default:
		require.FailNow(t, "unknown provider", providerName)
	}
}

func assertAgentExecutionCodexRuntimeTrace(
	t *testing.T,
	frames []agentExecutionFrameAt,
) {
	t.Helper()
	initializes := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Method == "initialize"
	})
	require.Len(t, initializes, 1)
	starts := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Method == "thread/start"
	})
	require.Len(t, starts, 1)
	assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Method == "thread/resume"
	}))
	var startParams struct {
		Model  string                     `json:"model"`
		Config map[string]json.RawMessage `json:"config"`
	}
	require.NoError(t, json.Unmarshal(starts[0].frame.Params, &startParams))
	assert.Equal(t, " Caller/Model v2 ", startParams.Model)
	var startEffort string
	require.NoError(t, json.Unmarshal(
		startParams.Config["model_reasoning_effort"],
		&startEffort,
	))
	assert.Equal(t, " Native/High ", startEffort)

	turns := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Method == "turn/start"
	})
	settings := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Method == "thread/settings/update"
	})
	require.Len(t, turns, 3)
	require.Len(t, settings, 2)
	requireAgentExecutionTurnText(t, turns[0].frame.Params, "before/update")
	requireAgentExecutionTurnText(t, turns[1].frame.Params, "after/effort")
	requireAgentExecutionTurnText(t, turns[2].frame.Params, "after/model")
	require.Less(t, turns[0].index, settings[0].index)
	require.Less(t, settings[0].index, turns[1].index)
	require.Less(t, turns[1].index, settings[1].index)
	require.Less(t, settings[1].index, turns[2].index)

	var effortParams, modelParams map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(settings[0].frame.Params, &effortParams))
	requireAgentExecutionString(t, effortParams, "threadId", agentExecutionSessionID)
	requireAgentExecutionString(t, effortParams, "effort", " next/effort ")
	assert.NotContains(t, effortParams, "model")

	require.NoError(t, json.Unmarshal(settings[1].frame.Params, &modelParams))
	requireAgentExecutionString(t, modelParams, "threadId", agentExecutionSessionID)
	requireAgentExecutionString(t, modelParams, "model", " next/model ")
	assert.NotContains(t, modelParams, "effort")
}

func assertAgentExecutionClaudeRuntimeTrace(
	t *testing.T,
	entries []agentExecutionTraceEntry,
	frames []agentExecutionFrameAt,
) {
	t.Helper()
	argv := agentExecutionTraceArgv(t, entries)
	assert.Contains(t, argv, "--model= Caller/Model v2 ")
	assert.Contains(t, argv, "--effort= Native/High ")
	assert.Contains(t, argv, "--permission-mode=bypassPermissions")

	initializes := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Type == "control_request" && frame.Request.Subtype == "initialize"
	})
	require.Len(t, initializes, 1)

	users := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Type == "user"
	})
	require.Len(t, users, 3)
	requireAgentExecutionClaudeText(t, users[0].frame, "before/update")
	requireAgentExecutionClaudeText(t, users[1].frame, "after/effort")
	requireAgentExecutionClaudeText(t, users[2].frame, "after/model")

	efforts := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Type == "control_request" &&
			frame.Request.Subtype == "apply_flag_settings"
	})
	require.Len(t, efforts, 1)
	models := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Type == "control_request" && frame.Request.Subtype == "set_model"
	})
	require.Len(t, models, 1)
	getSettings := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
		return frame.Type == "control_request" && frame.Request.Subtype == "get_settings"
	})
	require.Len(t, getSettings, 2)
	var settings struct {
		EffortLevel string `json:"effortLevel"`
		Ultracode   *bool  `json:"ultracode"`
	}
	require.NoError(t, json.Unmarshal(efforts[0].frame.Request.Settings, &settings))
	assert.Equal(t, " next/effort ", settings.EffortLevel)
	require.NotNil(t, settings.Ultracode)
	assert.False(t, *settings.Ultracode)
	assert.Equal(t, " next/model ", models[0].frame.Request.Model)

	require.Less(t, users[0].index, efforts[0].index)
	require.Less(t, initializes[0].index, getSettings[0].index)
	require.Less(t, getSettings[0].index, users[0].index)
	require.Less(t, users[0].index, getSettings[1].index)
	require.Less(t, getSettings[1].index, efforts[0].index)
	require.Less(t, efforts[0].index, users[1].index)
	require.Less(t, users[1].index, models[0].index)
	require.Less(t, models[0].index, users[2].index)
}

func assertAgentExecutionResumeTrace(t *testing.T, providerName, path string) {
	t.Helper()
	entries := readAgentExecutionTrace(t, path)
	_ = agentExecutionTraceArgv(t, entries)
	frames := agentExecutionTraceFrames(t, entries)
	switch providerName {
	case "codex":
		resumes := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "thread/resume"
		})
		require.Len(t, resumes, 1)
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "thread/start"
		}))
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "thread/settings/update"
		}))
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "turn/start"
		}))
		initializes := agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Method == "initialize"
		})
		require.Len(t, initializes, 1)
		var params struct {
			ExcludeTurns *bool                      `json:"excludeTurns"`
			ThreadID     string                     `json:"threadId"`
			Model        string                     `json:"model"`
			Config       map[string]json.RawMessage `json:"config"`
		}
		require.NoError(t, json.Unmarshal(resumes[0].frame.Params, &params))
		require.NotNil(t, params.ExcludeTurns)
		assert.True(t, *params.ExcludeTurns)
		assert.Equal(t, agentExecutionSessionID, params.ThreadID)
		assert.Equal(t, " Resume/Model ", params.Model)
		var effort string
		require.NoError(t, json.Unmarshal(
			params.Config["model_reasoning_effort"],
			&effort,
		))
		assert.Equal(t, " Native/Resume ", effort)
	case "claude":
		argv := agentExecutionTraceArgv(t, entries)
		assert.Len(t, agentExecutionFramesMatching(
			frames,
			func(frame agentExecutionTraceFrame) bool {
				return frame.Type == "control_request" && frame.Request.Subtype == "initialize"
			},
		), 1)
		assert.Contains(t, argv, "--resume="+agentExecutionSessionID)
		assert.Contains(t, argv, "--model= Resume/Model ")
		assert.Contains(t, argv, "--effort= Native/Resume ")
		assert.Contains(t, argv, "--permission-mode=bypassPermissions")
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "user"
		}))
		assert.Len(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "control_request" && frame.Request.Subtype == "get_settings"
		}), 1)
		assert.Empty(t, agentExecutionFramesMatching(frames, func(frame agentExecutionTraceFrame) bool {
			return frame.Type == "control_request" &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings")
		}))
	default:
		require.FailNow(t, "unknown provider", providerName)
	}
}

func requireAgentExecutionTurnText(
	t *testing.T,
	params json.RawMessage,
	want string,
) {
	t.Helper()
	var value struct {
		Input []struct {
			Text string `json:"text"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(params, &value))
	require.Len(t, value.Input, 1)
	assert.Equal(t, want, value.Input[0].Text)
}

func requireAgentExecutionClaudeText(
	t *testing.T,
	frame agentExecutionTraceFrame,
	want string,
) {
	t.Helper()
	var text string
	require.NoError(t, json.Unmarshal(frame.Message.Content, &text))
	assert.Equal(t, want, text)
}

func requireAgentExecutionString(
	t *testing.T,
	values map[string]json.RawMessage,
	key string,
	want string,
) {
	t.Helper()
	raw, ok := values[key]
	require.True(t, ok, "missing session config field %s", key)
	var got string
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}
