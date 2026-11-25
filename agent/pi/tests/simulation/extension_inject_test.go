package pisim

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestPiInjectActiveAppendRequiresTurnEnd(t *testing.T) {
	const fact = "controlled-pi-inject-fact-038"
	start := injectTestEvent(t, "message_start", fact, 123)
	end := injectTestEvent(t, "message_end", fact, 123)
	for _, scenario := range []struct {
		name, turnEndID string
		events          []agent.Event
		wantError       string
	}{
		{name: "after turn", turnEndID: "original-turn-end", events: []agent.Event{start, end}},
		{name: "before turn", events: []agent.Event{start}, wantError: "outside its expected turn boundary"},
		{name: "different fact", turnEndID: "original-turn-end", events: []agent.Event{injectTestEvent(t, "message_start", "another-fact", 123)}, wantError: "hidden appended fact"},
		{name: "unrelated end", turnEndID: "original-turn-end", events: []agent.Event{start, injectTestEvent(t, "message_end", fact, 124)}, wantError: "did not end its original append"},
		{name: "append repeated", turnEndID: "original-turn-end", events: []agent.Event{start, end, start}, wantError: "started twice"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			turn := injectTurn{endID: scenario.turnEndID}
			injection := injectAppend{fact: fact}
			var err error
			for _, event := range scenario.events {
				err = turn.observe(event, &injection)
				if err != nil {
					break
				}
			}
			if scenario.wantError != "" {
				require.ErrorContains(t, err, scenario.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, end.EventID, injection.endID)
		})
	}
}

func injectTestEvent(t *testing.T, kind, fact string, timestamp int64) agent.Event {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": kind,
		"message": map[string]any{
			"role": "custom", "customType": "agent-sdk-inject", "display": false, "timestamp": timestamp,
			"content": []map[string]string{{"type": "text", "text": fact}},
		},
	})
	require.NoError(t, err)
	return agent.Event{
		EventID: kind, Payload: agent.UnsupportedNativeEventPayload{AgentType: "pi", Raw: raw},
		Meta: map[string]any{"pi": map[string]any{"nativeType": kind}},
	}
}
