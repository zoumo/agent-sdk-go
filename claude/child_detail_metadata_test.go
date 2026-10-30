package claude

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChildDetailForwardedMetadataSchema(t *testing.T) {
	const message = `"message":{"type":"message","id":"message","role":"assistant","model":"model","content":[{"type":"text","text":"saved child output"}],"usage":{"input_tokens":0,"output_tokens":1}}`
	for _, tc := range []struct {
		name, extra string
		pure        bool
	}{
		{"original schema", "", true},
		{"native forwarded schema", `,"timestamp":"2026-10-29T13:57:36.879Z","subagent_type":"general-purpose","task_description":"Return saved child output"`, true},
		{"timestamp only", `,"timestamp":"2026-10-29T13:57:36Z"`, true},
		{"invalid timestamp", `,"timestamp":"yesterday"`, false},
		{"empty timestamp", `,"timestamp":""`, false},
		{"numeric timestamp", `,"timestamp":1793291877941`, false},
		{"null timestamp", `,"timestamp":null`, false},
		{"empty type", `,"subagent_type":""`, false},
		{"null type", `,"subagent_type":null`, false},
		{"boolean type", `,"subagent_type":true`, false},
		{"empty description", `,"task_description":""`, false},
		{"null description", `,"task_description":null`, false},
		{"object description", `,"task_description":{"status":"completed"}`, false},
		{"unknown field", `,"timestamp":"2026-10-29T13:57:36Z","new_fact":"value"`, false},
		{"error fact", `,"error":"failed"`, false},
		{"request identity", `,"request_id":"request"`, false},
		{"tool metadata", `,"tool_use_meta":[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"assistant","session_id":"root","uuid":"uuid","parent_tool_use_id":"tool",` + message + tc.extra + `}`)
			event, err := ParseEvent(raw)
			if err != nil {
				require.False(t, tc.pure, "malformed native metadata must not be classified as suppressible")
				return
			}
			require.Equal(t, tc.pure, pureChildDetail(event))
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &envelope))
			envelope["message"] = json.RawMessage(`{"type":"message","id":"m","role":"assistant","model":"model","content":[{"type":"text","text":"mixed"},{"type":"tool_use","id":"call","name":"Read","input":{}}]}`)
			mixed, err := json.Marshal(envelope)
			require.NoError(t, err)
			event, err = ParseEvent(mixed)
			require.NoError(t, err)
			require.False(t, pureChildDetail(event), "mixed tool facts must remain visible")
		})
	}
}

func TestChildDetailContextManagementPlaceholder(t *testing.T) {
	for _, value := range []string{`null`, `{}`, `[]`, `""`, `"context"`, `true`, `false`, `0`, `{"edits":[]}`} {
		t.Run(value, func(t *testing.T) {
			raw := []byte(`{"type":"assistant","session_id":"root","uuid":"uuid","parent_tool_use_id":"tool","timestamp":"2026-10-29T16:37:53.111Z","subagent_type":"general-purpose","task_description":"Reply with saved child marker","message":{"id":"message","type":"message","role":"assistant","model":"GLM-5.3","content":[{"type":"text","text":"saved child output"}],"usage":{"input_tokens":0,"output_tokens":0},"context_management":` + value + `}}`)
			event, err := ParseEvent(raw)
			require.NoError(t, err)
			require.Equal(t, value == `null`, pureChildDetail(event))
		})
	}
}
