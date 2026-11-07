package agentcodex

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestNativeOperationErrorPreservesOutcomeAndRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code agent.ErrorCode
	}{
		{
			name: "outcome unknown",
			err: &codex.CallError{
				Method:   "thread/start",
				Dispatch: codex.OutcomeUnknown,
				Cause:    context.Canceled,
			},
			code: agent.ErrorOutcomeUnknown,
		},
		{
			name: "native rejection",
			err: &codex.CallError{
				Method:   "turn/start",
				Dispatch: codex.Responded,
				Cause:    &codex.RPCError{Code: -32000, Message: "rejected"},
			},
			code: agent.ErrorNativeRejected,
		},
		{
			name: "closed",
			err: errors.Join(&codex.CallError{
				Method:   "thread/resume",
				Dispatch: codex.NotSent,
			}, codex.ErrClosed),
			code: agent.ErrorClosed,
		},
		{
			name: "other",
			err:  errors.New("transport failed"),
			code: agent.ErrorUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := nativeOperationError("open", test.err)
			assert.Equal(t, test.code, agent.CodeOf(err))
			assert.ErrorIs(t, err, test.err)
		})
	}
}
