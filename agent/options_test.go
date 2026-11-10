package agent_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestOpenOptionsValidateRequiresExplicitAbsoluteCwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
	}{
		{name: "empty", cwd: ""},
		{name: "relative", cwd: "relative/path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (agent.OpenOptions{Cwd: tt.cwd}).Validate()

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
		})
	}
}

func TestOpenOptionsValidateAcceptsAbsoluteCwdWithoutImplicitDefaults(t *testing.T) {
	cwd := t.TempDir()
	require.True(t, filepath.IsAbs(cwd))

	require.NoError(t, (agent.OpenOptions{Cwd: cwd}).Validate())
}

func TestResumeOptionsValidateRequiresSessionIdentity(t *testing.T) {
	err := (agent.ResumeOptions{}).Validate()

	require.Error(t, err)
	assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))

	err = (agent.ResumeOptions{
		SessionID: "session-1",
		OpenOptions: agent.OpenOptions{
			Cwd: t.TempDir(),
		},
	}).Validate()
	require.NoError(t, err)
}

func TestOpenOptionsValidateAdditionalSystemPrompt(t *testing.T) {
	const sentinel = "additional-prompt-secret-t02"
	tests := []struct {
		name       string
		value      string
		wantValid  bool
		wantReason string
	}{
		{
			name:      "empty",
			wantValid: true,
		},
		{
			name:      "literal text is preserved",
			value:     "  --literal leading dash\r\n附加指令：ünïcødé 🚀\n",
			wantValid: true,
		},
		{
			name:       "invalid UTF-8",
			value:      sentinel + string([]byte{0xff}),
			wantReason: agent.ErrorReasonAdditionalSystemPromptInvalidUTF8,
		},
		{
			name:       "NUL",
			value:      sentinel + "\x00tail",
			wantReason: agent.ErrorReasonAdditionalSystemPromptContainsNUL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := agent.OpenOptions{
				Cwd:                    t.TempDir(),
				AdditionalSystemPrompt: tt.value,
			}
			err := options.Validate()
			if tt.wantValid {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, agent.ErrorInvalidArgument, agent.CodeOf(err))
			var classified *agent.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, tt.wantReason, classified.Reason)
			assert.NotContains(t, err.Error(), sentinel)
		})
	}
}

func TestAdditionalSystemPromptValidationDoesNotExposeSentinel(t *testing.T) {
	const sentinel = "additional-prompt-secret-log-t02"
	for _, value := range []string{
		sentinel + string([]byte{0xff}),
		sentinel + "\x00tail",
	} {
		options := agent.OpenOptions{
			Cwd:                    t.TempDir(),
			AdditionalSystemPrompt: value,
		}
		err := options.Validate()
		require.Error(t, err)
		var classified *agent.Error
		require.ErrorAs(t, err, &classified)

		event := agent.Event{
			EventID:   "31f2ea35-1687-4207-b19c-cf767c430e38",
			SessionID: "session-1",
			Time:      time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
			Payload: agent.SessionErrorPayload{
				Message: classified.Message,
				Reason:  classified.Reason,
			},
			Meta: map[string]any{
				"errorCode": agent.CodeOf(err),
				"reason":    classified.Reason,
			},
		}
		encoded, encodeErr := json.Marshal(event)
		require.NoError(t, encodeErr)
		assert.NotContains(t, string(encoded), sentinel)

		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		logger.Error(
			"additional system prompt rejected",
			"error", err,
			"reason", classified.Reason,
		)
		assert.NotContains(t, logs.String(), sentinel)
	}
}

func TestExecutionOptionsHaveOnlyModelAndReasoningEffort(t *testing.T) {
	optionType := reflect.TypeFor[agent.ExecutionOptions]()

	require.Equal(t, 2, optionType.NumField())
	assert.Equal(t, "Model", optionType.Field(0).Name)
	assert.Equal(t, reflect.TypeFor[string](), optionType.Field(0).Type)
	assert.Equal(t, "ReasoningEffort", optionType.Field(1).Name)
	assert.Equal(t, reflect.TypeFor[string](), optionType.Field(1).Type)
}

func TestResumeOptionsInheritOpenExecutionOptions(t *testing.T) {
	execution := agent.ExecutionOptions{
		Model:           "native-model",
		ReasoningEffort: "native-effort",
	}
	opened := agent.OpenOptions{
		Cwd:       t.TempDir(),
		Execution: execution,
	}
	resumed := agent.ResumeOptions{
		SessionID:   "session-1",
		OpenOptions: opened,
	}

	assert.Equal(t, execution, opened.Execution)
	assert.Equal(t, execution, resumed.Execution)
}
