package pi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

func TestCycleSettings(t *testing.T) {
	const modelCommand = "cycle_model"
	const levelCommand = "cycle_thinking_level"
	for _, tc := range []struct {
		name    string
		command string
		data    string
		invalid bool
	}{
		{"model chosen", modelCommand, `{"model":{"id":"m","future":[1,2]},"thinkingLevel":"off","isScoped":false}`, false},
		{"level chosen", levelCommand, `{"level":"future-level"}`, false},
		{"no other model", modelCommand, "null", false},
		{"no other level", levelCommand, "null", false},
		{"model missing scope", modelCommand, `{"model":{},"thinkingLevel":"off"}`, true},
		{"model malformed", modelCommand, `{"model":[],"thinkingLevel":"off","isScoped":true}`, true},
		{"level missing", levelCommand, `{}`, true},
		{"level wrong type", levelCommand, `{"level":2}`, true},
		{"model rejected", modelCommand, "", false},
		{"level rejected", levelCommand, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, ctx := runPeer(t, func(p peer) error {
				var request map[string]string
				if err := p.input.Decode(&request); err != nil {
					return err
				}
				if len(request) != 2 || request["id"] == "" || request["type"] != tc.command {
					return fmt.Errorf("unexpected request %v", request)
				}
				if tc.data == "" {
					return json.NewEncoder(p.output).Encode(map[string]any{
						"type": "response", "id": request["id"], "command": tc.command, "success": false, "error": "rejected",
					})
				}
				return p.reply(command{ID: request["id"], Type: tc.command}, json.RawMessage(tc.data))
			})

			var err error
			if tc.command == modelCommand {
				var result *pi.ModelSelection
				result, err = session.CycleModel(ctx)
				if tc.data != "null" && tc.data != "" && !tc.invalid {
					require.NotNil(t, result)
					assert.JSONEq(t, `{"id":"m","future":[1,2]}`, string(result.Model))
					assert.Equal(t, "off", result.ThinkingLevel)
					assert.False(t, result.IsScoped)
				} else {
					assert.Nil(t, result)
				}
			} else {
				var level *string
				level, err = session.CycleThinkingLevel(ctx)
				if tc.data != "null" && tc.data != "" && !tc.invalid {
					require.NotNil(t, level)
					assert.Equal(t, "future-level", *level)
				} else {
					assert.Nil(t, level)
				}
			}
			switch {
			case tc.invalid:
				var malformed *pi.DispatchError
				require.ErrorAs(t, err, &malformed)
				assert.True(t, malformed.Sent)
			case tc.data == "":
				var rejected *pi.CommandError
				require.ErrorAs(t, err, &rejected)
				assert.Equal(t, tc.command, rejected.Command)
			default:
				require.NoError(t, err)
			}
		})
	}
}
