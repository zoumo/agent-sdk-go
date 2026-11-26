package agentcodex

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
)

func beginTestSubmit(t *testing.T, work *mainTurnOwnership) submitSnapshot {
	t.Helper()
	before, admitted := work.beginSubmit()
	require.True(t, admitted)
	return before
}

func setUnknownTestSubmit(t *testing.T, work *mainTurnOwnership) {
	t.Helper()
	before := beginTestSubmit(t, work)
	work.failSubmit(before, true)
}

func TestUnknownWriteKeepsWorkUnsettled(t *testing.T) {
	var work mainTurnOwnership
	setUnknownTestSubmit(t, &work)
	_, admitted := work.beginSubmit()
	assert.False(t, admitted)
}

func TestWriteFailureDoesNotRearmAfterTurnEvidence(t *testing.T) {
	var work mainTurnOwnership
	before := beginTestSubmit(t, &work)
	require.True(t, work.observeStarted("turn-a"))
	work.failSubmit(before, true)
	_, admitted := work.beginSubmit()
	assert.True(t, admitted)
}

func TestNativeEvidenceClearsUnknownWrite(t *testing.T) {
	var work mainTurnOwnership
	setUnknownTestSubmit(t, &work)
	require.True(t, work.observeStarted("turn-a"))
	_, admitted := work.beginSubmit()
	assert.True(t, admitted)
}

func TestWriteOutcomeUnknownClassification(t *testing.T) {
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
