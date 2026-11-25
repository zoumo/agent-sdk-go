package agentclaude

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

func TestChildModelDoesNotChangeMainUsage(t *testing.T) {
	for _, childFrame := range []struct {
		name string
		raw  string
	}{
		{
			name: "stream",
			raw:  `{"type":"stream_event","session_id":"main","parent_tool_use_id":"child-tool","event":{"type":"message_start","message":{"id":"child-response","model":"child-model"}}}`,
		},
		{
			name: "assistant",
			raw:  `{"type":"assistant","session_id":"main","parent_tool_use_id":"child-tool","message":{"id":"child-response","model":"child-model","role":"assistant","content":[{"type":"text","text":"child answer"}]}}`,
		},
	} {
		t.Run(childFrame.name, func(t *testing.T) {
			s := &session{id: "main"}
			frames := []string{
				`{"type":"stream_event","session_id":"main","parent_tool_use_id":null,"event":{"type":"message_start","message":{"id":"main-response","model":"main-model"}}}`,
				childFrame.raw,
				`{"type":"result","subtype":"success","session_id":"main","is_error":false,"modelUsage":{"main-model":{"inputTokens":1000,"outputTokens":100,"contextWindow":200000,"costUSD":1},"child-model":{"inputTokens":10,"outputTokens":5,"contextWindow":1000000,"costUSD":0.01}}}`,
			}
			var usages []agent.UsageUpdatePayload
			for _, raw := range frames {
				event, err := claude.ParseEvent([]byte(raw))
				require.NoError(t, err)
				events, err := s.convertReceived(claude.Received{
					Kind: claude.ReceivedNative, Event: &event, Raw: []byte(raw),
				})
				require.NoError(t, err)
				for _, unified := range events {
					if usage, ok := unified.Payload.(agent.UsageUpdatePayload); ok {
						usages = append(usages, usage)
					}
				}
			}
			require.Len(t, usages, 1)
			assert.Equal(t, agent.UsageUpdatePayload{
				Used: 1100,
				Size: 200000,
				Cost: &agent.UsageCost{Amount: json.Number("1"), Currency: "USD"},
			}, usages[0])
		})
	}
}

func TestPermissionStateEventDoesNotAliasSession(t *testing.T) {
	s := &session{id: "main", interactionLedger: interactionledger.New("test-")}
	s.updateState(agent.StateRunning)
	events, err := s.registerInteraction(&pendingInteraction{requestID: "req"}, agent.InteractionRequest{
		Kind: agent.InteractionPermission, ToolCallID: "tool", Title: "test",
		Options: []agent.PermissionOption{{OptionID: "deny", Name: "Deny", Kind: agent.PermissionReject, Scope: agent.PermissionScopeOnce}},
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	payload, ok := events[0].Payload.(agent.StateUpdatePayload)
	require.True(t, ok)
	require.NotNil(t, payload.State)
	require.NotNil(t, payload.PreviousState)
	*payload.State = agent.StateIdle
	*payload.PreviousState = agent.StateIdle
	assert.Equal(t, agent.StateRequiresAction, *s.state)
	previous := s.updateState(agent.StateRunning)
	require.NotNil(t, previous)
	assert.Equal(t, agent.StateRequiresAction, *previous)
	assert.Equal(t, agent.StateIdle, *payload.State)
}
