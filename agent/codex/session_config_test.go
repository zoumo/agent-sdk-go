package agentcodex_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestOpenAndResumePassLiteralSessionConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":" Native/High "},"cwd":"/synthetic","model":" Caller/Model "}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-open"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
		`>{"id":94,"method":"thread/resume","params":{"config":{"model_reasoning_effort":" Resume/Effort "},"cwd":"/synthetic","excludeTurns":true,"model":" Resume/Model ","threadId":"root-resume"}}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Config: agent.SessionConfig{
			Model:           " Caller/Model ",
			ReasoningEffort: " Native/High ",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "root-resume",
		OpenOptions: agent.OpenOptions{
			Cwd: "/synthetic",
			Config: agent.SessionConfig{
				Model:           " Resume/Model ",
				ReasoningEffort: " Resume/Effort ",
			},
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
}

func TestOpenAndResumePassSessionConfigFieldsIndependently(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		params   string
		resume   bool
		config   agent.SessionConfig
		nativeID string
	}{
		{
			name:     "open model only",
			method:   "thread/start",
			params:   `{"cwd":"/synthetic","model":"model-only"}`,
			config:   agent.SessionConfig{Model: "model-only"},
			nativeID: "root-open",
		},
		{
			name:     "open effort only",
			method:   "thread/start",
			params:   `{"config":{"model_reasoning_effort":"effort-only"},"cwd":"/synthetic"}`,
			config:   agent.SessionConfig{ReasoningEffort: "effort-only"},
			nativeID: "root-open",
		},
		{
			name:     "resume model only",
			method:   "thread/resume",
			params:   `{"cwd":"/synthetic","excludeTurns":true,"model":"model-only","threadId":"root-resume"}`,
			resume:   true,
			config:   agent.SessionConfig{Model: "model-only"},
			nativeID: "root-resume",
		},
		{
			name:     "resume effort only",
			method:   "thread/resume",
			params:   `{"config":{"model_reasoning_effort":"effort-only"},"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}`,
			resume:   true,
			config:   agent.SessionConfig{ReasoningEffort: "effort-only"},
			nativeID: "root-resume",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result := codexThreadStartResult(test.nativeID)
			if test.resume {
				result = codexThreadResumeResult(test.nativeID)
			}
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				fmt.Sprintf(`>{"id":92,"method":%q,"params":%s}`, test.method, test.params),
				`<{"id":92,"result":`+result+`}`,
				fmt.Sprintf(`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":%q}}`, test.nativeID),
				`<{"id":93,"result":{"status":"unsubscribed"}}`,
			)
			peerSession := prepareCodexPeer(t, frames...)
			provider := newTestProvider(t, peerSession)
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			var session agent.Session
			if test.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID: test.nativeID,
					OpenOptions: agent.OpenOptions{
						Cwd:    "/synthetic",
						Config: test.config,
					},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{
					Cwd:    "/synthetic",
					Config: test.config,
				})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
		})
	}
}

func TestOpenAndResumeOmitEmptySessionConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-open"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
		`>{"id":94,"method":"thread/resume","params":{"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	opened, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, opened)
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: agent.OpenOptions{Cwd: "/synthetic"},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
}

func TestOpenRejectsInvalidSessionConfigBeforeNativeRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reader, output := io.Pipe()
	input, writer := io.Pipe()
	requests := make(chan string, 8)
	peerDone := make(chan error, 1)
	// Keep the peer connected after initialization so an accidental native
	// start/resume cannot hide behind a completed fixture or closed transport.
	go func() {
		defer output.Close()
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			var frame struct {
				ID     json.RawMessage
				Method string
			}
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				peerDone <- err
				return
			}
			var err error
			switch frame.Method {
			case "initialize":
				_, err = fmt.Fprintf(output, "{\"id\":%s,\"result\":{\"userAgent\":\"synthetic\",\"codexHome\":\"/synthetic\",\"platformFamily\":\"unix\",\"platformOs\":\"linux\"}}\n", frame.ID)
			case "initialized":
				continue
			default:
				requests <- frame.Method
				_, err = fmt.Fprintf(output, "{\"id\":%s,\"error\":{\"code\":-32602,\"message\":\"unexpected native request\"}}\n", frame.ID)
			}
			if err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- scanner.Err()
	}()
	provider := agentcodex.NewWithStream(agentcodex.Config{}, codex.StreamTransport{Reader: reader, Writer: writer}, nil)
	t.Cleanup(func() {
		assert.NoError(t, provider.Stop(context.Background()))
		_ = writer.Close()
		_ = output.Close()
		select {
		case err := <-peerDone:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("synthetic peer did not stop")
		}
		_ = input.Close()
		assert.Empty(t, requests, "invalid config must not reach native thread/start or thread/resume")
	})

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		config  agent.SessionConfig
		reason  string
		message string
	}{
		{"model invalid UTF-8", agent.SessionConfig{Model: string([]byte{0xff})}, "config_value_invalid_utf8", "model must be valid UTF-8"},
		{"model NUL", agent.SessionConfig{Model: "model\x00tail"}, "config_value_contains_nul", "model must not contain NUL"},
		{"effort invalid UTF-8", agent.SessionConfig{ReasoningEffort: string([]byte{0xff})}, "config_value_invalid_utf8", "reasoning effort must be valid UTF-8"},
		{"effort NUL", agent.SessionConfig{ReasoningEffort: "effort\x00tail"}, "config_value_contains_nul", "reasoning effort must not contain NUL"},
	} {
		for _, operation := range []string{"open", "resume"} {
			t.Run(test.name+"/"+operation, func(t *testing.T) {
				options := agent.OpenOptions{Cwd: "/synthetic", Config: test.config}
				var session agent.Session
				var err error
				if operation == "open" {
					session, err = provider.Open(ctx, options)
				} else {
					session, err = provider.Resume(ctx, agent.ResumeOptions{
						SessionID: "root-resume", OpenOptions: options,
					})
				}
				require.Error(t, err)
				assert.Nil(t, session)
				assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
				var classified *agent.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, test.reason, classified.Reason)
				assert.Equal(t, test.message, classified.Message)
				assert.EqualError(t, err, "invalid_argument: "+test.message)
				assert.Empty(t, requests, "validation must precede native thread requests")
			})
		}
	}
}

func TestOpenPreservesNativeSessionConfigRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"future-effort"},"cwd":"/synthetic","model":"future-model"}}`,
		`<{"id":92,"error":{"code":-32602,"message":"unsupported session config"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Config: agent.SessionConfig{
			Model:           "future-model",
			ReasoningEffort: "future-effort",
		},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestSetConfigMapsIDsToCodexNativeOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next/model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/settings/update","params":{"effort":"next/effort","threadId":"root-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ConfigController)
	require.True(t, ok)

	modelChange := agent.ConfigChange{ID: agent.ConfigModel, Value: "next/model"}
	modelReceipt, err := controller.SetConfig(ctx, modelChange)
	require.NoError(t, err)
	assert.Equal(t, modelChange, modelReceipt.Change)
	assertAccepted(t, modelReceipt.Acceptance)

	effortChange := agent.ConfigChange{ID: agent.ConfigEffort, Value: "next/effort"}
	effortReceipt, err := controller.SetConfig(ctx, effortChange)
	require.NoError(t, err)
	assert.Equal(t, effortChange, effortReceipt.Change)
	assertAccepted(t, effortReceipt.Acceptance)
}

func TestSetConfigRejectsInvalidAndClosedBeforeNativeWrite(t *testing.T) {
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

func TestSetConfigRequiresExperimentalAPIBeforeNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t,
		`>{"id":91,"method":"initialize","params":{"capabilities":{"experimentalApi":false},"clientInfo":{"name":"agent-codex-test","version":"1"}}}`,
		`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
		`>{"method":"initialized"}`,
		`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	provider := newTestProviderWithExperimentalAPI(t, peerSession, false)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller := session.(agent.ConfigController)

	change := agent.ConfigChange{ID: agent.ConfigEffort, Value: "high"}
	receipt, err := controller.SetConfig(ctx, change)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	assert.Equal(t, change, receipt.Change)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
}

func TestSetConfigPreservesNativeRejectionAndOutcomeUnknown(t *testing.T) {
	t.Run("native rejection then success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		frames := append([]string(nil), mockCodexInit...)
		frames = append(frames,
			`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
			`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
			`>{"id":93,"method":"thread/settings/update","params":{"model":"rejected/model","threadId":"root-a"}}`,
			`<{"id":93,"error":{"code":-32602,"message":"unsupported model"}}`,
			`>{"id":94,"method":"thread/settings/update","params":{"model":"accepted/model","threadId":"root-a"}}`,
			`<{"id":94,"result":{}}`,
			`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
			`<{"id":95,"result":{"status":"unsubscribed"}}`,
		)
		peerSession := prepareCodexPeer(t, frames...)
		provider := newTestProvider(t, peerSession)
		t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
		_, err := provider.Start(ctx)
		require.NoError(t, err)
		session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
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
		assertAccepted(t, recoveryReceipt.Acceptance)
	})

	t.Run("outcome unknown", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		frames := append([]string(nil), mockCodexInit...)
		frames = append(frames,
			`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
			`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
			`>{"id":93,"method":"thread/settings/update","params":{"effort":"uncertain/effort","threadId":"root-a"}}`,
			`!`,
		)
		peerSession := prepareCodexPeerWithStatus(t, "failed", frames...)
		provider := newTestProvider(t, peerSession)
		t.Cleanup(func() { _ = provider.Stop(context.Background()) })
		_, err := provider.Start(ctx)
		require.NoError(t, err)
		session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close(context.Background()) })

		change := agent.ConfigChange{ID: agent.ConfigEffort, Value: "uncertain/effort"}
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
		assert.Equal(t, change, receipt.Change)
		require.NotNil(t, receipt.Acceptance.Sent)
		assert.True(t, *receipt.Acceptance.Sent)
		assert.Nil(t, receipt.Acceptance.NativeAccepted)
	})
}

func TestSetConfigMalformedSuccessDoesNotImplyNativeRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next/model","threadId":"root-a"}}`,
		`<{"id":93,"result":[]}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	change := agent.ConfigChange{ID: agent.ConfigModel, Value: "next/model"}
	receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))
	assert.Equal(t, change, receipt.Change)
	assert.Equal(t, agent.Acceptance{Sent: new(true)}, receipt.Acceptance)
	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, "set_config_failed", classified.Reason)
	var callErr *codex.CallError
	require.ErrorAs(t, err, &callErr)
	assert.Equal(t, "thread/settings/update", callErr.Method)
	assert.Equal(t, codex.Responded, callErr.Dispatch)
	assert.ErrorIs(t, err, codex.ErrProtocol)
	assert.EqualError(t, callErr.Cause, "codex: protocol error: decode thread/settings/update result: protocol: expected JSON object")
	var rpcErr *codex.RPCError
	assert.NotErrorAs(t, err, &rpcErr)
}

func TestSetConfigRepeatsIdenticalRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"same/model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/settings/update","params":{"model":"same/model","threadId":"root-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	change := agent.ConfigChange{ID: agent.ConfigModel, Value: "same/model"}
	for range 2 {
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		require.NoError(t, err)
		assert.Equal(t, change, receipt.Change)
		assertAccepted(t, receipt.Acceptance)
	}
}

func TestSettingsNotificationRemainsUnsupportedWithoutSyntheticSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`<{"method":"thread/settings/updated","params":{"threadId":"root-a","threadSettings":{"cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"readOnly"},"model":"observed/model","modelProvider":"synthetic","collaborationMode":{"mode":"default","settings":{"model":"observed/model"}},"effort":"high"}}}`,
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
	stream, err := session.Events()
	require.NoError(t, err)

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionError, event.Type())
	payload, ok := event.Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "config_options_unsupported", payload.Reason)
}
