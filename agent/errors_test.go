package agent_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestErrorClassificationSurvivesWrapping(t *testing.T) {
	cause := errors.New("native transport failed")
	classified := &agent.Error{
		Code:    agent.ErrorOutcomeUnknown,
		Reason:  "answer_dispatch_unverified",
		Message: "answer delivery is unknown; do not retry",
		Cause:   cause,
	}

	assert.Equal(
		t,
		"outcome_unknown: answer delivery is unknown; do not retry",
		classified.Error(),
	)
	assert.ErrorIs(t, classified, cause)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(classified))

	wrapped := fmt.Errorf("answer request: %w", classified)
	require.Error(t, wrapped)
	assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(wrapped))
	assert.ErrorIs(t, wrapped, cause)
}

func TestUnclassifiedErrorsHaveNoAgentCode(t *testing.T) {
	assert.Empty(t, agent.CodeOf(errors.New("unclassified")))
	assert.Empty(t, agent.CodeOf(nil))

	var nilError *agent.Error
	assert.Empty(t, agent.CodeOf(nilError))
}
