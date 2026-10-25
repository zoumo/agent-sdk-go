// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStrictReplayPreservesChildAliasesAndOriginalPermission(t *testing.T) {
	frames := []Frame{
		{1, "server", "protocol", json.RawMessage(`{"type":"system","subtype":"task_started","task_id":"a","tool_use_id":"spawn-a","task_type":"local_agent","future":null}`), 0},
		{2, "server", "protocol", json.RawMessage(`{"type":"system","subtype":"task_started","task_id":"c","parent_task_id":"a","tool_use_id":"spawn-c","task_type":"local_agent"}`), 0},
		{3, "server", "control", json.RawMessage(`{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","agent_id":"opaque","tool_use_id":"child-read","input":{"file_path":"numbers.txt"}}}`), 0},
		{4, "client", "control", json.RawMessage(`{"type":"control_response","response":{"request_id":"permission","response":{"behavior":"deny"}}}`), 0},
		{5, "server", "protocol", json.RawMessage(`{"type":"result","subtype":"success","is_error":false}`), 0},
		{6, "server", "protocol", json.RawMessage(`{"type":"system","subtype":"task_notification","task_id":"c","status":"completed","unknown":{"exact":9007199254740993}}`), 0},
	}
	var capture bytes.Buffer
	for _, frame := range frames {
		require.NoError(t, json.NewEncoder(&capture).Encode(frame))
	}
	input := string(frames[3].Data) + "\n"
	var output bytes.Buffer
	require.NoError(t, Replay(strings.NewReader(input), &output, bytes.NewReader(capture.Bytes()), nil))
	var expected bytes.Buffer
	for _, frame := range frames {
		if frame.Direction == "server" {
			expected.Write(frame.Data)
			expected.WriteByte('\n')
		}
	}
	require.Equal(t, expected.String(), output.String(), "native aliases, parent facts, unknown fields and late completion remain byte exact")
	wrong := strings.Replace(input, "permission", "copied-child-authority", 1)
	require.Error(t, Replay(strings.NewReader(wrong), &bytes.Buffer{}, bytes.NewReader(capture.Bytes()), nil))
}
