package agentclaude_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestClaudeOpenPassesLiteralExecutionOptionsAndReportsInitModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-open",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":" observed/model "}`,
		`<{"type":"assistant","session_id":"native-open","message":{`+
			`"id":"later-output","role":"assistant","model":" later/output ",`+
			`"content":[{"type":"text","text":"effort is Native/High",`+
			`"apiBlockIndex":0}]}}`,
	)
	provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           " Caller/Model v2 ",
			ReasoningEffort: " Native/High ",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	assert.Equal(t, claudeExpectedArgs(
		"--model= Caller/Model v2 ",
		"--effort= Native/High ",
	), capture.argv(t))

	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())

	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, " Caller/Model v2 ", report.Model.Requested)
	require.NotNil(t, report.Model.Effective)
	assert.Equal(t, " observed/model ", *report.Model.Effective)
	assert.Equal(t, " Native/High ", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestClaudeOpenAndResumeOmitEmptyExecutionOptions(t *testing.T) {
	tests := []struct {
		name      string
		resume    bool
		sessionID string
		wantArgs  []string
	}{
		{
			name:      "open",
			sessionID: "native-open",
			wantArgs:  claudeExpectedArgs(),
		},
		{
			name:      "resume",
			resume:    true,
			sessionID: "native-resume",
			wantArgs:  claudeExpectedArgs("--resume=native-resume"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames,
				`<{"type":"system","subtype":"init","session_id":`+
					jsonString(t, test.sessionID)+`,"cwd":`+jsonString(t, cwd)+
					`,"claude_code_version":"2.1.258","model":" default/model "}`,
			)
			provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
			var session agent.Session
			var err error
			if test.resume {
				session, err = provider.Resume(ctx, agent.ResumeOptions{
					SessionID:   test.sessionID,
					OpenOptions: agent.OpenOptions{Cwd: cwd},
				})
			} else {
				session, err = provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			assert.Equal(t, test.wantArgs, capture.argv(t))
			report := claudeExecutionOptionsReport(t, session)
			assert.Empty(t, report.Model.Requested)
			require.NotNil(t, report.Model.Effective)
			assert.Equal(t, " default/model ", *report.Model.Effective)
			assert.Empty(t, report.ReasoningEffort.Requested)
			assert.Nil(t, report.ReasoningEffort.Effective)
		})
	}
}

func TestClaudeOpenPassesExecutionFieldsIndependently(t *testing.T) {
	tests := []struct {
		name       string
		execution  agent.ExecutionOptions
		wantArgs   []string
		wantModel  string
		wantEffort string
	}{
		{
			name:      "model only",
			execution: agent.ExecutionOptions{Model: "model-only"},
			wantArgs:  claudeExpectedArgs("--model=model-only"),
			wantModel: "model-only",
		},
		{
			name:       "effort only",
			execution:  agent.ExecutionOptions{ReasoningEffort: "effort-only"},
			wantArgs:   claudeExpectedArgs("--effort=effort-only"),
			wantEffort: "effort-only",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames,
				`<{"type":"system","subtype":"init","session_id":"native-open",`+
					`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
					`"model":"default/model"}`,
			)
			provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
			session, err := provider.Open(ctx, agent.OpenOptions{
				Cwd:       cwd,
				Execution: test.execution,
			})
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)

			assert.Equal(t, test.wantArgs, capture.argv(t))
			report := claudeExecutionOptionsReport(t, session)
			assert.Equal(t, test.wantModel, report.Model.Requested)
			require.NotNil(t, report.Model.Effective)
			assert.Equal(t, "default/model", *report.Model.Effective)
			assert.Equal(t, test.wantEffort, report.ReasoningEffort.Requested)
			assert.Nil(t, report.ReasoningEffort.Effective)
		})
	}
}

func TestClaudeResumePassesLiteralExecutionOptionsAndPreservesIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-resume",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":" resumed/model "}`,
	)
	provider, capture := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID: "native-resume",
		OpenOptions: agent.OpenOptions{
			Cwd: cwd,
			Execution: agent.ExecutionOptions{
				Model:           " Resume/Model ",
				ReasoningEffort: " Native/Resume ",
			},
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	assert.Equal(t, "native-resume", session.ID())
	assert.Equal(t, claudeExpectedArgs(
		"--model= Resume/Model ",
		"--effort= Native/Resume ",
		"--resume=native-resume",
	), capture.argv(t))
	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, " Resume/Model ", report.Model.Requested)
	require.NotNil(t, report.Model.Effective)
	assert.Equal(t, " resumed/model ", *report.Model.Effective)
	assert.Equal(t, " Native/Resume ", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestClaudeOpenPreservesNativeExecutionRejection(t *testing.T) {
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
		Cwd: t.TempDir(),
		Execution: agent.ExecutionOptions{
			Model: "rejected/model",
		},
	})
	require.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
}

func TestClaudeExecutionReporterRequiresInitModelObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-open",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "requested/model",
			ReasoningEffort: "requested-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)

	report := claudeExecutionOptionsReport(t, session)
	assert.Equal(t, "requested/model", report.Model.Requested)
	assert.Nil(t, report.Model.Effective)
	assert.Equal(t, "requested-effort", report.ReasoningEffort.Requested)
	assert.Nil(t, report.ReasoningEffort.Effective)
}

func TestClaudeControllerUpdatesExecutionOptionsAndSharesLegacyState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/effective"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"get_settings"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_1","response":{"effective":{"effortLevel":"medium","fastMode":true},"sources":[{"source":"flagSettings","settings":{"effortLevel":"medium","fastMode":true,"ultracode":true}}],"applied":{"model":"initial/effective","effort":"medium","advisor":null,"ultracode":true}}}}`,
		`>{"type":"control_request","request_id":"go_2","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":"high","ultracode":false}}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_2","response":{"applied":true}}}`,
		`>{"type":"control_request","request_id":"go_3","request":{"subtype":"set_model","model":"next/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_3","response":{}}}`,
		`>{"type":"control_request","request_id":"go_4","request":{"subtype":"set_model","model":"next/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_4","response":{}}}`,
		`>{"type":"control_request","request_id":"go_5","request":{"subtype":"set_model","model":"legacy/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_5","response":{}}}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "initial/request",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok, "Claude session must implement execution options control")

	initial := claudeExecutionOptionsReport(t, session)
	requireExecutionValue(t, "initial/request", "initial/effective", initial.Model)
	requireExecutionValue(t, "initial-effort", "", initial.ReasoningEffort)

	effortReceipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "high",
	})
	require.NoError(t, err)
	assert.Equal(t, agent.ExecutionOptionReasoningEffort, effortReceipt.Change.Option)
	assert.Equal(t, "high", effortReceipt.Change.Value)
	require.NotNil(t, effortReceipt.Acceptance.Sent)
	require.NotNil(t, effortReceipt.Acceptance.NativeAccepted)
	assert.True(t, *effortReceipt.Acceptance.Sent)
	assert.True(t, *effortReceipt.Acceptance.NativeAccepted)
	requireExecutionValue(t, "initial/request", "initial/effective", effortReceipt.Report.Model)
	requireExecutionValue(t, "high", "", effortReceipt.Report.ReasoningEffort)

	for range 2 {
		modelReceipt, updateErr := controller.SetExecutionOption(
			ctx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  "next/model",
			},
		)
		require.NoError(t, updateErr)
		require.NotNil(t, modelReceipt.Acceptance.NativeAccepted)
		assert.True(t, *modelReceipt.Acceptance.NativeAccepted)
		requireExecutionValue(t, "next/model", "", modelReceipt.Report.Model)
		requireExecutionValue(t, "high", "", modelReceipt.Report.ReasoningEffort)
	}

	legacy, ok := session.(agent.ModelController)
	require.True(t, ok, "Claude session must retain the legacy model controller")
	legacyReceipt, err := legacy.SetModel(ctx, "legacy/model")
	require.NoError(t, err)
	assert.Equal(t, "legacy/model", legacyReceipt.Model)
	require.NotNil(t, legacyReceipt.Acceptance.NativeAccepted)
	assert.True(t, *legacyReceipt.Acceptance.NativeAccepted)
	report := claudeExecutionOptionsReport(t, session)
	requireExecutionValue(t, "legacy/model", "", report.Model)
	requireExecutionValue(t, "high", "", report.ReasoningEffort)
}

func TestClaudeControllerRejectsInvalidAndClosedBeforeNativeWrite(t *testing.T) {
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
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "request/model",
			ReasoningEffort: "request-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)
	before := claudeExecutionOptionsReport(t, session)

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
			receipt, updateErr := controller.SetExecutionOption(ctx, test.change)
			require.Error(t, updateErr)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(updateErr))
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

func TestClaudeControllerPreservesNativeRejectionAndSessionAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/effective"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"rejected/model"}}`,
		`<{"type":"control_response","response":{"subtype":"error","request_id":"go_1","error":"unsupported model"}}`,
		`>{"type":"control_request","request_id":"go_2","request":{"subtype":"get_settings"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_2","response":{"sources":[],"applied":{"model":"initial/effective","effort":"medium","advisor":null,"ultracode":false}}}}`,
		`>{"type":"control_request","request_id":"go_3","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":"after-reject","ultracode":false}}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_3","response":{}}}`,
	)
	provider, _ := startClaudeExecutionProvider(t, ctx, frames...)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "initial/request",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "rejected/model",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.NativeAccepted)
	requireExecutionValue(t, "initial/request", "initial/effective", receipt.Report.Model)
	requireExecutionValue(t, "initial-effort", "", receipt.Report.ReasoningEffort)

	receipt, err = controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "after-reject",
	})
	require.NoError(t, err)
	requireExecutionValue(t, "initial/request", "initial/effective", receipt.Report.Model)
	requireExecutionValue(t, "after-reject", "", receipt.Report.ReasoningEffort)
}

func TestClaudeControllerEffortPreflightFailureIsUnsupportedNotSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		mockClaudeInit[0],
		mockClaudeInit[1],
		`<{"type":"system","subtype":"init","session_id":"native-runtime",` +
			`"cwd":` + jsonString(t, cwd) + `,"claude_code_version":"2.1.258",` +
			`"model":"initial/effective"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"get_settings"}}`,
		`!`,
	}
	peerSession := prepareClaudePeer(t, "failed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "initial/request",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)
	before := claudeExecutionOptionsReport(t, session)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "unconfirmed-effort",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
	var cause *claude.Error
	require.ErrorAs(t, err, &cause)
	assert.True(t, cause.OutcomeUnknown)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.False(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	assert.Equal(t, before, receipt.Report)
}

func TestClaudeControllerReportsUnknownOutcomeAndClearsUncertainEffective(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := []string{
		mockClaudeInit[0],
		mockClaudeInit[1],
		`<{"type":"system","subtype":"init","session_id":"native-runtime",` +
			`"cwd":` + jsonString(t, cwd) + `,"claude_code_version":"2.1.258",` +
			`"model":"initial/effective"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"uncertain/model"}}`,
		`!`,
	}
	peerSession := prepareClaudePeer(t, "failed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: cwd,
		Execution: agent.ExecutionOptions{
			Model:           "initial/request",
			ReasoningEffort: "initial-effort",
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)

	receipt, err := controller.SetExecutionOption(ctx, agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionModel,
		Value:  "uncertain/model",
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.Nil(t, receipt.Acceptance.NativeAccepted)
	requireExecutionValue(t, "initial/request", "", receipt.Report.Model)
	requireExecutionValue(t, "initial-effort", "", receipt.Report.ReasoningEffort)
}

func TestClaudeLegacyAndControllerSerializeConcurrentUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"native-runtime",`+
			`"cwd":`+jsonString(t, cwd)+`,"claude_code_version":"2.1.258",`+
			`"model":"initial/model"}`,
		`>{"type":"control_request","request_id":"go_1","request":{"subtype":"set_model","model":"slow/model"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_1","response":{}}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	gate := newClaudeTestGate(`"subtype":"set_model"`)
	provider := newTestClaudeProviderWithStreamHook(
		t,
		agentclaude.Config{},
		nil,
		gate.wrap,
		peerSession,
	)
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	controller, ok := session.(agent.ExecutionOptionsController)
	require.True(t, ok)
	legacy, ok := session.(agent.ModelController)
	require.True(t, ok)

	type result struct {
		name    string
		receipt agent.ExecutionOptionsReceipt
		err     error
	}
	results := make(chan result, 2)
	go func() {
		_, updateErr := legacy.SetModel(ctx, "slow/model")
		results <- result{
			name: "slow",
			err:  updateErr,
		}
	}()

	select {
	case <-gate.requestSeen:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "legacy update did not reach the native request")
	}

	fastCtx, cancelFast := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelFast()
	fastStarted := make(chan struct{})
	go func() {
		close(fastStarted)
		receipt, updateErr := controller.SetExecutionOption(
			fastCtx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "fast-effort",
			},
		)
		results <- result{
			name:    "fast",
			receipt: receipt,
			err:     updateErr,
		}
	}()
	<-fastStarted
	select {
	case got := <-results:
		require.FailNow(t, "execution option update returned while the first response was held", "%+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	gate.releaseResponse()

	byName := make(map[string]result, 2)
	for range 2 {
		got := <-results
		byName[got.name] = got
	}
	slow, fast := byName["slow"], byName["fast"]
	require.NoError(t, slow.err)
	require.Error(t, fast.err)
	require.NotNil(t, fast.receipt.Acceptance.Sent)
	assert.False(t, *fast.receipt.Acceptance.Sent)
}

func startClaudeExecutionProvider(
	t *testing.T,
	ctx context.Context,
	frames ...string,
) (*agentclaude.Provider, *claudeExecutionCapture) {
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
	require.True(t, c.seen, "Claude session did not observe execution options")
	argv, err := claude.NativeArgs(c.options)
	require.NoError(t, err)
	return argv
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

func claudeExecutionOptionsReport(
	t *testing.T,
	session agent.Session,
) agent.ExecutionOptionsReport {
	t.Helper()
	reporter, ok := session.(agent.ExecutionOptionsReporter)
	require.True(t, ok, "Claude session must report execution options")
	return reporter.EffectiveExecutionOptions()
}

func requireExecutionValue(
	t *testing.T,
	requested string,
	effective string,
	value agent.ExecutionValue,
) {
	t.Helper()
	require.Equal(t, requested, value.Requested)
	if effective == "" {
		require.Nil(t, value.Effective)
		return
	}
	require.NotNil(t, value.Effective)
	require.Equal(t, effective, *value.Effective)
}

type claudeTestGate struct {
	match        []byte
	requestSeen  chan struct{}
	responseOpen chan struct{}
	signalOnce   sync.Once
	releaseOnce  sync.Once
}

func newClaudeTestGate(match string) *claudeTestGate {
	return &claudeTestGate{
		match:        []byte(match),
		requestSeen:  make(chan struct{}),
		responseOpen: make(chan struct{}),
	}
}

func (g *claudeTestGate) wrap(transport claude.StreamTransport) claude.StreamTransport {
	transport.Writer = &claudeTestGateWriter{
		WriteCloser: transport.Writer,
		gate:        g,
	}
	transport.Reader = newClaudeTestGateReader(transport.Reader, g)
	return transport
}

func (g *claudeTestGate) signalRequest() {
	g.signalOnce.Do(func() { close(g.requestSeen) })
}

func (g *claudeTestGate) releaseResponse() {
	g.releaseOnce.Do(func() { close(g.responseOpen) })
}

func (g *claudeTestGate) waitForRelease() {
	select {
	case <-g.requestSeen:
		<-g.responseOpen
	default:
	}
}

type claudeTestGateWriter struct {
	io.WriteCloser
	gate    *claudeTestGate
	mu      sync.Mutex
	pending []byte
}

func (w *claudeTestGateWriter) Write(p []byte) (int, error) {
	n, err := w.WriteCloser.Write(p)
	if n == 0 {
		return n, err
	}
	w.mu.Lock()
	w.pending = append(w.pending, p[:n]...)
	if bytes.Contains(w.pending, w.gate.match) {
		w.gate.signalRequest()
	}
	if index := bytes.LastIndexByte(w.pending, '\n'); index >= 0 {
		w.pending = append(w.pending[:0], w.pending[index+1:]...)
	}
	w.mu.Unlock()
	return n, err
}

type claudeTestGateReader struct {
	source    io.ReadCloser
	reader    *io.PipeReader
	writer    *io.PipeWriter
	gate      *claudeTestGate
	done      chan struct{}
	closeOnce sync.Once
}

func newClaudeTestGateReader(
	source io.ReadCloser,
	gate *claudeTestGate,
) *claudeTestGateReader {
	reader, writer := io.Pipe()
	result := &claudeTestGateReader{
		source: source,
		reader: reader,
		writer: writer,
		gate:   gate,
		done:   make(chan struct{}),
	}
	go result.pump()
	return result
}

func (r *claudeTestGateReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *claudeTestGateReader) Close() error {
	r.closeOnce.Do(func() {
		_ = r.reader.Close()
		_ = r.source.Close()
	})
	<-r.done
	return nil
}

func (r *claudeTestGateReader) pump() {
	defer close(r.done)
	defer func() { _ = r.writer.Close() }()
	buffer := make([]byte, 32*1024)
	for {
		n, err := r.source.Read(buffer)
		if n > 0 {
			r.gate.waitForRelease()
			if _, writeErr := r.writer.Write(buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				_ = r.writer.CloseWithError(err)
			}
			return
		}
	}
}
