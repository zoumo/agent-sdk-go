package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlannedCaseApprovalKinds(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		allow      bool
	}{
		{"file-allow", "Write", true}, {"command-allow", "Bash", true}, {"command-deny", "Bash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wire.jsonl")
			session := openSessionFixture(t, func(o *SessionOptions) { o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path) })
			request := sessionPermission(t, session, tc.name, tc.tool, `{"synthetic":true}`)
			if tc.allow {
				require.NoError(t, request.Approve(context.Background(), ApprovalOptions{}))
			} else {
				require.NoError(t, request.Deny(context.Background(), DenialOptions{Message: "synthetic denial"}))
			}
			require.NoError(t, session.Interrupt(context.Background(), InterruptOptions{}))
			require.NoError(t, session.Close())
			responses := permissionResponses(t, permissionTranscript(t, path))
			require.Len(t, responses, 1)
			raw, err := json.Marshal(responses[0]["response"].(map[string]any)["response"])
			require.NoError(t, err)
			if tc.allow {
				require.Contains(t, string(raw), `"behavior":"allow"`)
			} else {
				require.Contains(t, string(raw), `"behavior":"deny"`)
			}
		})
	}
}

func TestGapConfigurationBetweenCompletedExecutions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	s := openSessionFixture(t, func(o *SessionOptions) { o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path) })
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	for i := 0; i < 2; i++ {
		if i == 1 {
			model := "second-model"
			r, err := s.SetModel(context.Background(), ModelChange{Model: &model})
			require.NoError(t, err)
			require.True(t, r.Accepted)
			require.Nil(t, r.Observed)
		}
		submitFrame(t, s, `{"type":"result","session_id":"s","subtype":"success","is_error":false,"result":"completed"}`)
		for {
			r := sessionNative(t, s)
			if r.Event.Type == "result" {
				break
			}
		}
	}
	require.NoError(t, s.Close())
	var order []string
	for _, raw := range permissionTranscript(t, path) {
		var f map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &f))
		var kind string
		require.NoError(t, json.Unmarshal(f["type"], &kind))
		if kind == "control_request" {
			var request struct {
				Subtype string `json:"subtype"`
			}
			require.NoError(t, json.Unmarshal(f["request"], &request))
			if request.Subtype == "set_model" {
				order = append(order, "set_model")
			}
		} else if kind == "user" {
			order = append(order, "input")
		}
	}
	require.Equal(t, []string{"input", "input", "set_model", "input"}, order)
}

func TestGapChildInheritanceRemainsUnknown(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	model := "root-requested"
	r, err := s.SetModel(context.Background(), ModelChange{Model: &model})
	require.NoError(t, err)
	require.True(t, r.Accepted)
	require.Nil(t, r.Observed)
	const child = `{"type":"assistant","session_id":"s","agent_id":"child","message":{"content":"native child output"},"inheritance":null}`
	submitFrame(t, s, child)
	for {
		got := sessionNative(t, s)
		if got.Event.Type == "assistant" && got.Source.AgentID == "child" {
			require.Equal(t, "child", got.Source.AgentID)
			require.JSONEq(t, child, string(got.Raw))
			break
		}
	}
	require.Nil(t, r.Observed, "child existence/output does not prove inherited model")
}

func TestGapNativeEffortCreation(t *testing.T) {
	for _, value := range []string{"", "low", "max", "native-future-value", "--literal-value"} {
		t.Run(value, func(t *testing.T) {
			o := SessionOptions(fixtureOptions(t, "input_config"))
			o.Effort = value
			capture := filepath.Join(t.TempDir(), "capture.json")
			o.Env = append(o.Env, "CLAUDE_GO_CAPTURE="+capture, "CLAUDE_GO_TRANSCRIPT="+filepath.Join(t.TempDir(), "wire.jsonl"))
			require.Equal(t, value, OptionsFromTypes(Options(o).Types()).Effort)
			s, err := Open(context.Background(), o)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Abort() })
			data, err := os.ReadFile(capture)
			require.NoError(t, err)
			var got struct{ Argv []string }
			require.NoError(t, json.Unmarshal(data, &got))
			if value != "" {
				require.Contains(t, got.Argv, "--effort="+value)
			} else {
				for _, arg := range got.Argv {
					require.NotContains(t, arg, "--effort")
				}
			}
			for _, arg := range got.Argv {
				require.NotContains(t, arg, "--max-thinking-tokens", "effort must not turn into a token budget")
			}
		})
	}
}
