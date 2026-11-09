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
		name      string
		execution agent.ExecutionOptions
	}{
		{
			name:      "model",
			execution: agent.ExecutionOptions{Model: string([]byte{0xff})},
		},
		{
			name:      "reasoning effort",
			execution: agent.ExecutionOptions{ReasoningEffort: string([]byte{0xff})},
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
