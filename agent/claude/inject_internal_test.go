package agentclaude

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestInjectDoesNotWriteDuringOverlappingOrUnknownInput(t *testing.T) {
	for name, session := range map[string]*session{
		"query in flight":     {injectSupported: true, submitting: true},
		"injection in flight": {injectSupported: true, injecting: true},
		"unknown query":       {injectSupported: true, submitUnknown: true},
	} {
		t.Run(name, func(t *testing.T) {
			receipt, err := session.Inject(context.Background(), agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "context"}}},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
			assert.Empty(t, session.activeSubmit)
			assert.False(t, session.workActive)
		})
	}
}

func TestInjectInFlightDoesNotAdmitQuery(t *testing.T) {
	session := &session{injecting: true}
	receipt, err := session.StartOrSteer(context.Background(), agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "query"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, receipt.Result)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	assert.Empty(t, session.activeSubmit)
	assert.False(t, session.workActive)
}

func TestInjectVersionBoundary(t *testing.T) {
	for _, version := range []string{"2.1.293", "2.1.294", "2.1.322", "2.2.0", "3.0.0", "2.1.293+build.1"} {
		assert.True(t, supportsInject(version), version)
	}
	for _, version := range []string{"", "2.0.999", "2.1.258", "2.1.292", "2.1.-293", "2.1.293-beta", "2.1.293.extra"} {
		assert.False(t, supportsInject(version), version)
	}
}
