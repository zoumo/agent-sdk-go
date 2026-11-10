package agentcodex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestStartOrSteerRefusesLocalConcurrentOrUnknownWrite(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*session)
	}{
		{
			name:  "concurrent write",
			setup: func(s *session) { s.submitting = true },
		},
		{
			name:  "unknown write",
			setup: func(s *session) { s.submitUnknown = true },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := &session{}
			test.setup(s)

			receipt, err := s.StartOrSteer(context.Background(), agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "retained"}}},
			})
			require.NoError(t, err)
			assert.Equal(t, agent.InputNotApplied, receipt.Result)
			assert.Empty(t, receipt.Submission)
			require.NotNil(t, receipt.Acceptance.Sent)
			assert.False(t, *receipt.Acceptance.Sent)
			assert.Nil(t, receipt.Acceptance.NativeAccepted)
			assert.Nil(t, receipt.Acceptance.NativeRecorded)
		})
	}
}

func TestStartOrSteerClosedSessionRemainsAnError(t *testing.T) {
	s := &session{closed: true}
	_, err := s.StartOrSteer(context.Background(), agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "closed"}}},
	})
	require.Error(t, err)
	assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
}

func TestCodexTurnInputNotAppliedRequiresDefinitiveResponse(t *testing.T) {
	rpc := &codex.RPCError{Code: -32600, Message: "no active turn to steer"}
	assert.True(t, codexTurnInputNotApplied(&codex.CallError{
		Method:   "turn/start",
		Dispatch: codex.Responded,
		Cause:    rpc,
	}))
	assert.False(t, codexTurnInputNotApplied(&codex.CallError{
		Method:   "turn/start",
		Dispatch: codex.NotSent,
		Cause:    rpc,
	}))
	assert.False(t, codexTurnInputNotApplied(&codex.CallError{
		Method:   "turn/start",
		Dispatch: codex.OutcomeUnknown,
		Cause:    rpc,
	}))
	assert.False(t, codexTurnInputNotApplied(errors.New("synthetic failure")))
}

func TestCodexTurnInputNotAppliedRecognizesStructuredTurnError(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"message": "cannot steer a review turn",
		"codexErrorInfo": map[string]any{
			"activeTurnNotSteerable": map[string]any{"turnKind": "review"},
		},
	})
	require.NoError(t, err)
	assert.True(t, codexTurnInputNotApplied(&codex.CallError{
		Method:   "turn/start",
		Dispatch: codex.Responded,
		Cause: &codex.RPCError{
			Code:    -32600,
			Message: "cannot create a synthetic turn",
			Data:    data,
		},
	}))
}
