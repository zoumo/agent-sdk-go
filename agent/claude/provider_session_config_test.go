package agentclaude_test

import (
	"context"
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

func TestClaudeOpenAndResumePassLiteralSessionConfig(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cwd := t.TempDir()
		frames := append([]string(nil), mockClaudeInit...)
		frames = append(frames,
			`<{"type":"system","subtype":"init","session_id":"native-open",`+
				`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
				`"model":"observed/model"}`,
		)
		provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
		session, err := provider.Open(ctx, agent.OpenOptions{
			Cwd: cwd,
			Config: agent.SessionConfig{
				Model:  " Caller/Model ",
				Effort: " Native/High ",
			},
		})
		require.NoError(t, err)
		closeSessionOnCleanup(t, session)
		assert.Equal(t, claudeExpectedArgs(
			"--model= Caller/Model ",
			"--effort= Native/High ",
			"--session-id="+session.ID(),
		), capture.argv(t))
	})

	t.Run("resume", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cwd := t.TempDir()
		initJSON := `{"type":"system","subtype":"init","session_id":` +
			jsonString(t, claudeResumePeerSessionID) + `,"cwd":` + jsonString(t, cwd) +
			`,"claude_code_version":"2.1.258","model":"observed/model"}`
		provider, argvPath := startClaudeResumeExecutionProvider(
			t,
			ctx,
			cwd,
			claudeResumePeerSessionID,
			initJSON,
		)
		session, err := provider.Resume(ctx, agent.ResumeOptions{
			SessionID: claudeResumePeerSessionID,
			OpenOptions: agent.OpenOptions{
				Cwd: cwd,
				Config: agent.SessionConfig{
					Model:  " Resume/Model ",
					Effort: " Resume/Effort ",
				},
			},
		})
		require.NoError(t, err)
		closeSessionOnCleanup(t, session)
		assert.Equal(t, claudeExpectedArgs(
			"--model= Resume/Model ",
			"--effort= Resume/Effort ",
			"--resume="+claudeResumePeerSessionID,
		), readClaudeArgv(t, argvPath))
	})
}

func TestClaudeOpenAndResumePassSessionConfigFieldsIndependently(t *testing.T) {
	tests := []struct {
		name     string
		resume   bool
		config   agent.SessionConfig
		wantArgs []string
	}{
		{
			name:     "open model only",
			config:   agent.SessionConfig{Model: "model-only"},
			wantArgs: claudeExpectedArgs("--model=model-only", "--session-id=native-open"),
		},
		{
			name:     "open effort only",
			config:   agent.SessionConfig{Effort: "effort-only"},
			wantArgs: claudeExpectedArgs("--effort=effort-only", "--session-id=native-open"),
		},
		{
			name:     "resume model only",
			resume:   true,
			config:   agent.SessionConfig{Model: "model-only"},
			wantArgs: claudeExpectedArgs("--model=model-only", "--resume="+claudeResumePeerSessionID),
		},
		{
			name:     "resume effort only",
			resume:   true,
			config:   agent.SessionConfig{Effort: "effort-only"},
			wantArgs: claudeExpectedArgs("--effort=effort-only", "--resume="+claudeResumePeerSessionID),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			capturedArgv := startClaudeSessionConfigForArgv(
				t,
				ctx,
				test.resume,
				test.config,
			)
			assert.Equal(t, test.wantArgs, capturedArgv(t))
		})
	}
}

func TestClaudeOpenAndResumeOmitEmptySessionConfig(t *testing.T) {
	tests := []struct {
		name     string
		resume   bool
		wantArgs []string
	}{
		{
			name:     "open",
			wantArgs: claudeExpectedArgs("--session-id=native-open"),
		},
		{
			name:     "resume",
			resume:   true,
			wantArgs: claudeExpectedArgs("--resume=" + claudeResumePeerSessionID),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			capturedArgv := startClaudeSessionConfigForArgv(
				t,
				ctx,
				test.resume,
				agent.SessionConfig{},
			)
			assert.Equal(t, test.wantArgs, capturedArgv(t))
		})
	}
}

func TestClaudeRejectsInvalidSessionConfigBeforeLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	launched := 0
	provider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		func(claude.SessionOptions) {
			launched++
		},
	)
	_, err := provider.Start(ctx)
	require.NoError(t, err)

	const sentinel = "session-config-secret-claude"
	tests := []struct {
		name       string
		config     agent.SessionConfig
		wantReason string
	}{
		{
			name: "invalid UTF-8",
			config: agent.SessionConfig{
				Model: sentinel + string([]byte{0xff}),
			},
			wantReason: "config_value_invalid_utf8",
		},
		{
			name: "NUL",
			config: agent.SessionConfig{
				Effort: sentinel + "\x00tail",
			},
			wantReason: "config_value_contains_nul",
		},
	}

	for _, test := range tests {
		for _, operation := range []string{"open", "resume"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				options := agent.OpenOptions{Cwd: cwd, Config: test.config}
				var operationErr error
				if operation == "open" {
					_, operationErr = provider.Open(ctx, options)
				} else {
					_, operationErr = provider.Resume(ctx, agent.ResumeOptions{
						SessionID:   "session-config-existing",
						OpenOptions: options,
					})
				}
				require.Error(t, operationErr)
				assert.Zero(t, launched)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(operationErr))
				var classified *agent.Error
				require.ErrorAs(t, operationErr, &classified)
				assert.Equal(t, test.wantReason, classified.Reason)
				assert.NotContains(t, operationErr.Error(), sentinel)
			})
		}
	}
}

func TestClaudeOpenPreservesNativeSessionConfigRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := []string{
		`>{"type":"control_request","request_id":"init",` +
			`"request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`,
		`<{"type":"control_response","response":{"subtype":"error",` +
			`"request_id":"init","error":"model rejected"}}`,
	}
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)

	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:    t.TempDir(),
		Config: agent.SessionConfig{Model: "rejected/model"},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestClaudeSetConfigMapsIDsToNativeOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/model"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"get_settings"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_1","response":{"applied":{"ultracode":false}}}}`,
		`>{"type":"control_request","request_id":"go_2","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":"high","ultracode":false}}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_2","response":{"applied":true}}}`,
		`>{"type":"control_request","request_id":"go_3","request":{"subtype":"set_model","model":"next/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_3","response":{}}}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ConfigController)
	require.True(t, ok)

	effortChange := agent.ConfigChange{ID: agent.ConfigEffort, Value: "high"}
	effortReceipt, err := controller.SetConfig(ctx, effortChange)
	require.NoError(t, err)
	assert.Equal(t, effortChange, effortReceipt.Change)
	requireAccepted(t, effortReceipt.Acceptance)

	modelChange := agent.ConfigChange{ID: agent.ConfigModel, Value: "next/model"}
	modelReceipt, err := controller.SetConfig(ctx, modelChange)
	require.NoError(t, err)
	assert.Equal(t, modelChange, modelReceipt.Change)
	requireAccepted(t, modelReceipt.Acceptance)
}

func TestClaudeSetConfigRejectsInvalidAndClosedBeforeNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/model"}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ConfigController)
	require.True(t, ok)

	for _, change := range []agent.ConfigChange{
		{ID: "sandbox", Value: "read-only"},
		{ID: agent.ConfigModel},
		{ID: agent.ConfigEffort, Value: string([]byte{0xff})},
	} {
		receipt, updateErr := controller.SetConfig(ctx, change)
		require.Error(t, updateErr)
		assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(updateErr))
		assert.Equal(t, change, receipt.Change)
		require.NotNil(t, receipt.Acceptance.Sent)
		assert.False(t, *receipt.Acceptance.Sent)
		assert.Nil(t, receipt.Acceptance.NativeAccepted)
	}

	require.NoError(t, session.Close(ctx))
	change := agent.ConfigChange{ID: agent.ConfigModel, Value: "after-close"}
	receipt, err := controller.SetConfig(ctx, change)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	assert.Equal(t, change, receipt.Change)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
}

func TestClaudeSetConfigPreservesNativeRejectionAndOutcomeUnknown(t *testing.T) {
	t.Run("native rejection then success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cwd := t.TempDir()
		frames := append([]string(nil), mockClaudeInit...)
		frames = append(frames,
			`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
				`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
				`"model":"initial/model"}`,
			`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"rejected/model"}}`,
			`<{"type":"control_response","response":{"subtype":"error","request_id":"go_1","error":"unsupported model"}}`,
			`>{"type":"control_request","request_id":"go_2","request":{"subtype":"set_model","model":"accepted/model"}}`,
			`<{"type":"control_response","response":{"subtype":"success","request_id":"go_2","response":{}}}`,
		)
		provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
		session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
		require.NoError(t, err)
		closeSessionOnCleanup(t, session)

		change := agent.ConfigChange{ID: agent.ConfigModel, Value: "rejected/model"}
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
		assert.Equal(t, change, receipt.Change)
		require.NotNil(t, receipt.Acceptance.Sent)
		require.NotNil(t, receipt.Acceptance.NativeAccepted)
		assert.True(t, *receipt.Acceptance.Sent)
		assert.False(t, *receipt.Acceptance.NativeAccepted)

		recoveryChange := agent.ConfigChange{
			ID:    agent.ConfigModel,
			Value: "accepted/model",
		}
		recoveryReceipt, err := session.(agent.ConfigController).SetConfig(
			ctx,
			recoveryChange,
		)
		require.NoError(t, err)
		assert.Equal(t, recoveryChange, recoveryReceipt.Change)
		requireAccepted(t, recoveryReceipt.Acceptance)
	})

	t.Run("outcome unknown", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cwd := t.TempDir()
		frames := append([]string(nil), mockClaudeInit...)
		frames = append(frames,
			`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
				`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
				`"model":"initial/model"}`,
			`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"uncertain/model"}}`,
			`!`,
		)
		peerSession := prepareClaudePeer(t, "failed", frames...)
		provider := newTestClaudeProvider(t, peerSession)
		_, err := provider.Start(ctx)
		require.NoError(t, err)
		session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close(context.Background()) })

		change := agent.ConfigChange{ID: agent.ConfigModel, Value: "uncertain/model"}
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
		assert.Equal(t, change, receipt.Change)
		require.NotNil(t, receipt.Acceptance.Sent)
		assert.True(t, *receipt.Acceptance.Sent)
		assert.Nil(t, receipt.Acceptance.NativeAccepted)
	})
}

func TestClaudeEffortPreflightFailureIsNotSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		mockClaudeInit[0],
		mockClaudeInit[1],
		`<{"type":"system","subtype":"init","session_id":"native-runtime",` +
			`"cwd":` + jsonString(t, cwd) + `,"claude_code_version":"2.1.258",` +
			`"model":"initial/model"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"get_settings"}}`,
		`!`,
	}
	peerSession := prepareClaudePeer(t, "failed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	change := agent.ConfigChange{ID: agent.ConfigEffort, Value: "high"}
	receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	// The read-only get_settings request was sent but lost its response. Its
	// native outcome is unknown; the effort mutation itself was never sent.
	var nativeErr *claude.Error
	require.ErrorAs(t, err, &nativeErr)
	assert.True(t, nativeErr.OutcomeUnknown)
	assert.Equal(t, change, receipt.Change)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
}

func TestClaudeSetConfigRepeatsIdenticalRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/model"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"same/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_1","response":{}}}`,
		`>{"type":"control_request","request_id":"go_2","request":{"subtype":"set_model","model":"same/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_2","response":{}}}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	change := agent.ConfigChange{ID: agent.ConfigModel, Value: "same/model"}
	for range 2 {
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		require.NoError(t, err)
		assert.Equal(t, change, receipt.Change)
		requireAccepted(t, receipt.Acceptance)
	}
}

func requireAccepted(t *testing.T, acceptance agent.Acceptance) {
	t.Helper()
	require.NotNil(t, acceptance.Sent)
	require.NotNil(t, acceptance.NativeAccepted)
	assert.True(t, *acceptance.Sent)
	assert.True(t, *acceptance.NativeAccepted)
}

func startClaudeSessionConfigForArgv(
	t *testing.T,
	ctx context.Context,
	resume bool,
	config agent.SessionConfig,
) func(*testing.T) []string {
	t.Helper()
	cwd := t.TempDir()
	sessionID := "native-open"
	if resume {
		sessionID = claudeResumePeerSessionID
	}
	initJSON := `{"type":"system","subtype":"init","session_id":` +
		jsonString(t, sessionID) + `,"cwd":` + jsonString(t, cwd) +
		`,"claude_code_version":"2.1.258","model":"default/model"}`

	var provider *agentclaude.Core
	var capturedArgv func(*testing.T) []string
	if resume {
		var argvPath string
		provider, argvPath = startClaudeResumeExecutionProvider(
			t,
			ctx,
			cwd,
			sessionID,
			initJSON,
		)
		capturedArgv = func(t *testing.T) []string {
			return readClaudeArgv(t, argvPath)
		}
	} else {
		frames := append([]string(nil), mockClaudeInit...)
		frames = append(frames, "<"+initJSON)
		var capture *claudeExecutionCapture
		provider, capture = startClaudeExecutionProvider(t, ctx, frames...)
		capturedArgv = capture.argv
	}

	var session agent.Session
	var err error
	if resume {
		session, err = provider.Resume(ctx, agent.ResumeOptions{
			SessionID: sessionID,
			OpenOptions: agent.OpenOptions{
				Cwd:    cwd,
				Config: config,
			},
		})
	} else {
		session, err = provider.Open(ctx, agent.OpenOptions{
			Cwd:    cwd,
			Config: config,
		})
	}
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	return capturedArgv
}

func startClaudeExecutionProvider(
	t *testing.T,
	ctx context.Context,
	frames ...string,
) (*agentclaude.Core, *claudeExecutionCapture) {
	t.Helper()
	peerSession := prepareClaudePeer(t, "passed", frames...)
	capture := &claudeExecutionCapture{}
	provider := newTestClaudeProviderWithObserver(
		t,
		agentclaude.Config{},
		capture.observe,
		peerSession,
	)
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	return provider, capture
}

type claudeExecutionCapture struct {
	mu      sync.Mutex
	options claude.SessionOptions
	seen    bool
}

func (c *claudeExecutionCapture) observe(options claude.SessionOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.options = options
	c.seen = true
}

func (c *claudeExecutionCapture) argv(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.True(t, c.seen, "Claude session did not observe configuration options")
	argv, err := claude.NativeArgs(c.options)
	require.NoError(t, err)
	return argv
}

func startClaudeResumeExecutionProvider(
	t *testing.T,
	ctx context.Context,
	cwd, sessionID string,
	frames ...string,
) (*agentclaude.Core, string) {
	t.Helper()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(t, configDir, "project", sessionID, cwd)
	argv := filepath.Join(t.TempDir(), "argv.json")
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{
		SessionID:      sessionID,
		CWD:            cwd,
		TranscriptPath: transcript,
		ArgvPath:       argv,
		Frames:         frames,
	})
	provider := agentclaude.New(agentclaude.Config{
		CLIPath:   peer.CLIPath,
		Env:       peer.Env,
		ConfigDir: configDir,
	})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	return provider, argv
}

func claudeExpectedArgs(extras ...string) []string {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--include-partial-messages",
	}
	return append(args, extras...)
}
