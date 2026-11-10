package agentcodex_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestProviderOpenPassesLiteralExecutionOptionsAndReportsNativeEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":" Native/High "},"cwd":"/synthetic","model":" Caller/Model v2 "}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "observed/model", "medium")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           " Caller/Model v2 ",
			ReasoningEffort: " Native/High ",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	report := executionOptionsReport(t, session)
	requireExecutionValue(t, " Caller/Model v2 ", "observed/model", report.Model)
	requireExecutionValue(t, " Native/High ", "medium", report.ReasoningEffort)
}

func TestProviderOpenAndResumeOmitEmptyExecutionOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-open", "default/model", "low")+`}`,
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

	report := executionOptionsReport(t, opened)
	requireExecutionValue(t, "", "default/model", report.Model)
	requireExecutionValue(t, "", "low", report.ReasoningEffort)
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: agent.OpenOptions{Cwd: "/synthetic"},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)

	report = executionOptionsReport(t, resumed)
	requireExecutionValue(t, "", "synthetic", report.Model)
	assert.Empty(t, report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestProviderPassesExecutionFieldsIndependently(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		params    string
		resume    bool
		execution agent.ExecutionOptions
		nativeID  string
	}{
		{
			name:      "open model only",
			method:    "thread/start",
			params:    `{"cwd":"/synthetic","model":"model-only"}`,
			execution: agent.ExecutionOptions{Model: "model-only"},
			nativeID:  "root-open",
		},
		{
			name:      "open effort only",
			method:    "thread/start",
			params:    `{"config":{"model_reasoning_effort":"effort-only"},"cwd":"/synthetic"}`,
			execution: agent.ExecutionOptions{ReasoningEffort: "effort-only"},
			nativeID:  "root-open",
		},
		{
			name:      "resume model only",
			method:    "thread/resume",
			params:    `{"cwd":"/synthetic","excludeTurns":true,"model":"model-only","threadId":"root-resume"}`,
			resume:    true,
			execution: agent.ExecutionOptions{Model: "model-only"},
			nativeID:  "root-resume",
		},
		{
			name:      "resume effort only",
			method:    "thread/resume",
			params:    `{"config":{"model_reasoning_effort":"effort-only"},"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}`,
			resume:    true,
			execution: agent.ExecutionOptions{ReasoningEffort: "effort-only"},
			nativeID:  "root-resume",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := append([]string(nil), mockCodexInit...)
			frames = append(frames,
				fmt.Sprintf(`>{"id":92,"method":%q,"params":%s}`, test.method, test.params),
				`<{"id":92,"result":`+codexExecutionResponseResult(test.resume, test.nativeID)+`}`,
				`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":`+fmt.Sprintf("%q", test.nativeID)+`}}`,
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
						Cwd:       "/synthetic",
						Execution: test.execution,
					},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{
					Cwd:       "/synthetic",
					Execution: test.execution,
				})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			report := executionOptionsReport(t, session)
			requireExecutionValue(t, test.execution.Model, "observed/model", report.Model)
			requireExecutionValue(t, test.execution.ReasoningEffort, "observed-effort", report.ReasoningEffort)
		})
	}
}

func TestProviderResumePassesLiteralExecutionOptionsAndReportsNativeEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/resume","params":{"config":{"model_reasoning_effort":" Native/Resume "},"cwd":"/synthetic","excludeTurns":true,"model":" Caller/Resume ","threadId":"root-resume"}}`,
		`<{"id":92,"result":`+codexExecutionResumeResult("root-resume", "observed/resume", "high")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "root-resume",
		OpenOptions: agent.OpenOptions{
			Cwd: "/synthetic",
			Execution: agent.ExecutionOptions{
				Model:           " Caller/Resume ",
				ReasoningEffort: " Native/Resume ",
			},
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	report := executionOptionsReport(t, session)
	requireExecutionValue(t, " Caller/Resume ", "observed/resume", report.Model)
	requireExecutionValue(t, " Native/Resume ", "high", report.ReasoningEffort)
}

func TestProviderOpenSurfacesNativeExecutionOptionRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"future-effort"},"cwd":"/synthetic","model":"future-model"}}`,
		`<{"id":92,"error":{"code":-32602,"message":"unsupported execution option"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "future-model",
			ReasoningEffort: "future-effort",
		},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestProviderRejectsInvalidUTF8BeforeNativeRequest(t *testing.T) {
	tests := []struct {
		name       string
		execution  agent.ExecutionOptions
		wantReason string
	}{
		{
			name:       "model",
			execution:  agent.ExecutionOptions{Model: string([]byte{0xff})},
			wantReason: "execution_option_invalid_utf8",
		},
		{
			name:       "reasoning effort",
			execution:  agent.ExecutionOptions{ReasoningEffort: string([]byte{0xff})},
			wantReason: "execution_option_invalid_utf8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			peerSession := prepareCodexPeer(t, mockCodexInit...)
			provider := newTestProvider(t, peerSession)
			t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			session, err := provider.Open(ctx, agent.OpenOptions{
				Cwd:       "/synthetic",
				Execution: test.execution,
			})
			require.Error(t, err)
			assert.Nil(t, session)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, test.wantReason, classified.Reason)
		})
	}
}

func TestReporterTracksAcceptedLegacyModelChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"initial-effort"},"cwd":"/synthetic","model":"initial-request"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next-model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "initial-request",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	controller, ok := session.(agent.ModelController)
	require.True(t, ok)
	receipt, err := controller.SetModel(ctx, "next-model")
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.NativeAccepted)

	report := executionOptionsReport(t, session)
	assert.Equal(t, "next-model", report.Model.Requested)
	assert.Nil(t, report.Model.Effective)
	assert.Equal(t, "initial-effort", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestSessionUpdatesExecutionOptionsThroughPublicController(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"initial-effort"},"cwd":"/synthetic","model":"initial-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-effective", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next-model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/settings/update","params":{"effort":"next-effort","threadId":"root-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "initial-model",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	modelReceipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "next-model",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.ExecutionOptionModel, modelReceipt.Change.Option)
	assert.Equal(t, "next-model", modelReceipt.Change.Value)
	require.NotNil(t, modelReceipt.Acceptance.Sent)
	require.NotNil(t, modelReceipt.Acceptance.NativeAccepted)
	assert.True(t, *modelReceipt.Acceptance.Sent)
	assert.True(t, *modelReceipt.Acceptance.NativeAccepted)
	assert.Equal(t, "next-model", modelReceipt.Report.Model.Requested)
	assert.Nil(t, modelReceipt.Report.Model.Effective)
	assert.Equal(t, "initial-effort", modelReceipt.Report.ReasoningEffort.Requested)
	assert.Nil(t, modelReceipt.Report.ReasoningEffort.Effective)

	effortReceipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "next-effort",
	})
	require.NoError(t, err)
	require.NotNil(t, effortReceipt.Acceptance.NativeAccepted)
	assert.True(t, *effortReceipt.Acceptance.NativeAccepted)
	assert.Equal(t, "next-model", effortReceipt.Report.Model.Requested)
	assert.Equal(t, "next-effort", effortReceipt.Report.ReasoningEffort.Requested)
	assert.Nil(t, effortReceipt.Report.Model.Effective)
	assert.Nil(t, effortReceipt.Report.ReasoningEffort.Effective)
}

func TestReporterUsesOnlyMainSessionSettingsNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"request-effort"},"cwd":"/synthetic","model":"request-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "medium")+`}`,
		`<{"method":"thread/settings/updated","params":{"threadId":"root-a","threadSettings":{"cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"},"model":"observed/model","modelProvider":"synthetic","collaborationMode":{"mode":"default","settings":{"model":"observed/model"}},"effort":"observed-effort"}}}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "request-model",
			ReasoningEffort: "request-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	event, err := stream.Next(ctx)
	require.NoError(t, err)
	payload, ok := event.Payload.(agent.SessionErrorPayload)
	require.True(t, ok)
	assert.Equal(t, "config_options_unsupported", payload.Reason)
	report := executionOptionsReport(t, session)
	requireExecutionValue(t, "request-model", "observed/model", report.Model)
	requireExecutionValue(
		t,
		"request-effort",
		"observed-effort",
		report.ReasoningEffort,
	)
}

func TestModelUpdateClearsEffortUntilNativeObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"effort-request"},"cwd":"/synthetic","model":"model-request"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next-model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`<{"method":"thread/settings/updated","params":{"threadId":"root-a","threadSettings":{"cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"readOnly"},"model":"observed/model","modelProvider":"synthetic","collaborationMode":{"mode":"default","settings":{"model":"observed/model"}}}}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "model-request",
			ReasoningEffort: "effort-request",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "next-model",
	})
	require.NoError(t, err)
	assert.Equal(t, "next-model", receipt.Report.Model.Requested)
	if receipt.Report.Model.Effective != nil {
		assert.Equal(t, "observed/model", *receipt.Report.Model.Effective)
	}
	assert.Equal(t, "effort-request", receipt.Report.ReasoningEffort.Requested)
	assert.Nil(t, receipt.Report.ReasoningEffort.Effective)

	var settingsEvent agent.Event
	for range 4 {
		settingsEvent, err = stream.Next(ctx)
		require.NoError(t, err)
		if codexEventMethod(settingsEvent) == "thread/settings/updated" {
			break
		}
	}
	require.Equal(t, "thread/settings/updated", codexEventMethod(settingsEvent))
	report := executionOptionsReport(t, session)
	requireExecutionValue(t, "next-model", "observed/model", report.Model)
	assert.Equal(t, "effort-request", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestControllerRejectsInvalidAndClosedBeforeNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
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
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)
	before := executionOptionsReport(t, session)

	tests := []struct {
		name   string
		change agent.ExecutionOptionChange
	}{
		{
			name: "unknown option",
			change: agent.ExecutionOptionChange{
				Option: "sandbox",
				Value:  "danger-full-access",
			},
		},
		{
			name: "empty value",
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
			},
		},
		{
			name: "invalid utf8",
			change: agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  string([]byte{0xff}),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt, err := controller.SetExecutionOption(ctx, test.change)
			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.False(t, *receipt.Acceptance.Sent)
			assert.Nil(t, receipt.Acceptance.NativeAccepted)
			assert.Equal(t, before, receipt.Report)
		})
	}

	require.NoError(t, session.Close(ctx))
	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "after-close",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Equal(t, before, receipt.Report)
}

func TestControllerRequiresExperimentalAPIBeforeNativeWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t,
		`>{"id":91,"method":"initialize","params":{"capabilities":{"experimentalApi":false},"clientInfo":{"name":"agent-codex-test","version":"1"}}}`,
		`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
		`>{"method":"initialized"}`,
		`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
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
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)
	before := executionOptionsReport(t, session)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "next-model",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Equal(t, before, receipt.Report)
}

func TestControllerPreservesNativeRejectionAndSessionAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"initial-effort"},"cwd":"/synthetic","model":"initial-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-effective", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"rejected-model","threadId":"root-a"}}`,
		`<{"id":93,"error":{"code":-32602,"message":"unsupported model"}}`,
		`>{"id":94,"method":"thread/settings/update","params":{"effort":"after-reject","threadId":"root-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "initial-model",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "rejected-model",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.NativeAccepted)
	requireExecutionValue(t, "initial-model", "initial-effective", receipt.Report.Model)
	requireExecutionValue(t, "initial-effort", "high", receipt.Report.ReasoningEffort)

	receipt, err = controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "after-reject",
	})
	require.NoError(t, err)
	assert.Equal(t, "initial-model", receipt.Report.Model.Requested)
	require.NotNil(t, receipt.Report.Model.Effective)
	assert.Equal(t, "initial-effective", *receipt.Report.Model.Effective)
	assert.Equal(t, "after-reject", receipt.Report.ReasoningEffort.Requested)
	assert.Nil(t, receipt.Report.ReasoningEffort.Effective)
}

func TestControllerReportsUnknownOutcomeAndClearsUncertainEffective(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"model_reasoning_effort":"initial-effort"},"cwd":"/synthetic","model":"initial-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-effective", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"uncertain-model","threadId":"root-a"}}`,
		`!`,
	)
	peerSession := prepareCodexPeerWithStatus(t, "failed", frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model:           "initial-model",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "uncertain-model",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Equal(t, "initial-model", receipt.Report.Model.Requested)
	assert.Nil(t, receipt.Report.Model.Effective)
	assert.Equal(t, "initial-effort", receipt.Report.ReasoningEffort.Requested)
	assert.Nil(t, receipt.Report.ReasoningEffort.Effective)
}

func TestReporterIgnoresChildSettingsNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
		`<{"method":"thread/started","params":{"thread":{"id":"child-a","parentThreadId":"root-a"}}}`,
		`<{"method":"thread/settings/updated","params":{"threadId":"child-a","threadSettings":{"cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"readOnly"},"model":"child-model","modelProvider":"synthetic","collaborationMode":{"mode":"default","settings":{"model":"child-model"}},"effort":"low"}}}`,
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
	report := executionOptionsReport(t, session)
	requireExecutionValue(t, "", "initial-model", report.Model)
	requireExecutionValue(t, "", "high", report.ReasoningEffort)

	var settingsEvent agent.Event
	for range 6 {
		settingsEvent, err = stream.Next(ctx)
		require.NoError(t, err)
		if codexEventMethod(settingsEvent) == "thread/settings/updated" {
			break
		}
	}
	require.Equal(t, "thread/settings/updated", codexEventMethod(settingsEvent))
	assert.Equal(t, report, executionOptionsReport(t, session))
}

func TestControllerRepeatsIdenticalRequestsAndSharesLegacyState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic","model":"initial-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-effective", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"same-model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`>{"id":94,"method":"thread/settings/update","params":{"model":"same-model","threadId":"root-a"}}`,
		`<{"id":94,"result":{}}`,
		`>{"id":95,"method":"thread/settings/update","params":{"model":"legacy-model","threadId":"root-a"}}`,
		`<{"id":95,"result":{}}`,
		`>{"id":96,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":96,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: "/synthetic",
		Execution: agent.ExecutionOptions{
			Model: "initial-model",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	for range 2 {
		receipt, updateErr := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
			Option: agent.ExecutionOptionModel,
			Value:  "same-model",
		})
		require.NoError(t, updateErr)
		assert.Equal(t, "same-model", receipt.Report.Model.Requested)
		assert.Nil(t, receipt.Report.Model.Effective)
	}

	legacy, ok := session.(agent.ModelController)
	require.True(t, ok)
	legacyReceipt, err := legacy.SetModel(ctx, "legacy-model")
	require.NoError(t, err)
	require.NotNil(t, legacyReceipt.Acceptance.NativeAccepted)
	assert.True(t, *legacyReceipt.Acceptance.NativeAccepted)
	report := executionOptionsReport(t, session)
	assert.Equal(t, "legacy-model", report.Model.Requested)
	assert.Nil(t, report.Model.Effective)
}

func TestControllerSerializesConcurrentNativeUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic","model":"initial-model"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-effective", "high")+`}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"slow-model","threadId":"root-a"}}`,
		`>{"id":94,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":94,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeerWithStatus(t, "failed", frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:       "/synthetic",
		Execution: agent.ExecutionOptions{Model: "initial-model"},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	type result struct {
		name    string
		elapsed time.Duration
		receipt agent.ExecutionOptionsReceipt
		err     error
	}
	started := time.Now()
	slowCtx, cancelSlow := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancelSlow()
	results := make(chan result, 2)
	go func() {
		receipt, updateErr := controller.SetExecutionOption(
			slowCtx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  "slow-model",
			},
		)
		results <- result{
			name:    "slow",
			elapsed: time.Since(started),
			receipt: receipt,
			err:     updateErr,
		}
	}()

	time.Sleep(50 * time.Millisecond)
	fastCtx, cancelFast := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelFast()
	go func() {
		receipt, updateErr := controller.SetExecutionOption(
			fastCtx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "fast-effort",
			},
		)
		results <- result{
			name:    "fast",
			elapsed: time.Since(started),
			receipt: receipt,
			err:     updateErr,
		}
	}()

	byName := make(map[string]result, 2)
	for range 2 {
		got := <-results
		byName[got.name] = got
	}
	slow, fast := byName["slow"], byName["fast"]
	require.Error(t, slow.err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(slow.err))
	require.Error(t, fast.err)
	require.NotNil(t, fast.receipt.Acceptance.Sent)
	assert.False(t, *fast.receipt.Acceptance.Sent)
	assert.GreaterOrEqual(
		t,
		fast.elapsed,
		350*time.Millisecond,
		"the second request must wait for the in-flight native update",
	)
}

func TestControllerUpdatesWhileTurnIsActiveWithoutWaitingOrInterrupting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexExecutionStartResult("root-a", "initial-model", "high")+`}`,
		`<{"method":"turn/started","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"inProgress","startedAt":1700000000}}}`,
		`>{"id":93,"method":"thread/settings/update","params":{"model":"next-model","threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		`<{"method":"turn/completed","params":{"threadId":"root-a","turn":{"id":"turn-a","items":[],"status":"completed","startedAt":1700000000,"completedAt":1700000001}}}`,
		`>{"id":94,"method":"turn/start","params":{"input":[{"text":"after-update","type":"text"}],"threadId":"root-a"}}`,
		`<{"id":94,"result":{"turn":{"id":"turn-b","items":[],"status":"inProgress"}}}`,
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
	stream, err := session.Events()
	require.NoError(t, err)
	runningEvent, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventSessionStateUpdate, runningEvent.Type())
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "next-model",
	})
	require.NoError(t, err)
	assert.Equal(t, "next-model", receipt.Report.Model.Requested)
	var completed bool
	for range 3 {
		event, nextErr := stream.Next(ctx)
		require.NoError(t, nextErr)
		if codexEventMethod(event) == "turn/completed" {
			completed = true
			break
		}
	}
	require.True(t, completed, "the active turn must be able to complete after the update")
	nextReceipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{agent.TextBlock{Text: "after-update"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "turn-b", nextReceipt.Submission)
	select {
	case <-session.Done():
		require.Fail(t, "execution option update ended the active session")
	default:
	}
}

func codexEventMethod(event agent.Event) any {
	meta, _ := event.Meta["codex"].(map[string]any)
	return meta["method"]
}

func codexExecutionStartResult(threadID, model, effort string) string {
	return replaceExecutionValues(
		codexThreadStartResult(threadID),
		model,
		effort,
	)
}

func codexExecutionResumeResult(threadID, model, effort string) string {
	return replaceExecutionValues(
		codexThreadResumeResult(threadID),
		model,
		effort,
	)
}

func codexExecutionResponseResult(resume bool, threadID string) string {
	if resume {
		return codexExecutionResumeResult(threadID, "observed/model", "observed-effort")
	}
	return codexExecutionStartResult(threadID, "observed/model", "observed-effort")
}

func replaceExecutionValues(result, model, effort string) string {
	result = strings.Replace(
		result,
		`"model": "synthetic"`,
		fmt.Sprintf(`"model": %q`, model),
		1,
	)
	return strings.Replace(
		result,
		`"sandbox"`,
		fmt.Sprintf(`"reasoningEffort": %q, "sandbox"`, effort),
		1,
	)
}

func executionOptionsReport(
	t *testing.T,
	session agent.Session,
) agent.ExecutionOptionsReport {
	t.Helper()
	reporter, ok := session.(agent.ExecutionOptionsReporter)
	require.True(t, ok)
	return reporter.EffectiveExecutionOptions()
}

func requireExecutionValue(
	t *testing.T,
	requested string,
	effective string,
	value agent.ExecutionValue,
) {
	t.Helper()
	assert.Equal(t, requested, value.Requested)
	require.NotNil(t, value.Effective)
	assert.Equal(t, effective, *value.Effective)
}
