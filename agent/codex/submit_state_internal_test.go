package agentcodex

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/codex"
)

func TestUnknownSubmitKeepsWorkUnsettled(t *testing.T) {
	session := &session{}
	session.mu.Lock()
	defer session.mu.Unlock()

	session.recordSubmitErrorLocked(errors.New("transport failed"), session.workEpoch)
	assert.True(t, session.submitUnknown)
	assert.True(t, session.hasUnsettledWorkLocked())
}

func TestSubmitFailureDoesNotRearmAfterTurnEvidence(t *testing.T) {
	session := &session{}
	epoch := session.workEpoch

	session.noteWorkEvidenceLocked()
	session.recordSubmitErrorLocked(errors.New("transport failed"), epoch)

	assert.False(t, session.submitUnknown)
	assert.False(t, session.hasUnsettledWorkLocked())
}

func TestNativeEvidenceClearsUnknownSubmit(t *testing.T) {
	session := &session{submitUnknown: true}

	session.noteWorkEvidenceLocked()

	assert.False(t, session.submitUnknown)
	assert.False(t, session.hasUnsettledWorkLocked())
}

func TestSubmitOutcomeUnknownClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "written without response",
			err: &codex.CallError{
				Method:   "turn/start",
				Dispatch: codex.OutcomeUnknown,
				Cause:    context.Canceled,
			},
			want: true,
		},
		{
			name: "rejected before write",
			err: &codex.CallError{
				Method:   "turn/start",
				Dispatch: codex.NotSent,
				Cause:    context.Canceled,
			},
		},
		{
			name: "native rejection",
			err: &codex.CallError{
				Method:   "turn/start",
				Dispatch: codex.Responded,
				Cause:    &codex.RPCError{Code: -32602, Message: "invalid"},
			},
		},
		{
			name: "untyped failure",
			err:  errors.New("transport failed"),
			want: true,
		},
		{
			name: "validated before writing",
			err:  codex.ErrInvalidInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, submitOutcomeUnknown(test.err))
		})
	}
}
