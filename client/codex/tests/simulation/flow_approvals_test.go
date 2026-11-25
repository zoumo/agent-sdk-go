package codexsim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
	recording "github.com/zoumo/agent-sdk-go/internal/testsupport/codex/replay"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
)

func TestFlowRenamesThreadBeforeAnsweringOriginalApproval(t *testing.T) {
	frames := rootPrefix(t)
	addRoot(&frames, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic task"}]}}`)
	addRoot(&frames, "s2c", `{"id":103,"result":{"turn":{"id":"turn-001","items":[],"status":"inProgress"}}}`)
	addRoot(&frames, "s2c", `{"id":"held","method":"item/commandExecution/requestApproval","params":{"threadId":"thread-001","turnId":"turn-001","itemId":"item-001","command":"DO NOT EXECUTE"}}`)
	addRoot(&frames, "c2s", `{"id":104,"method":"thread/name/set","params":{"threadId":"thread-001","name":"pending approval"}}`)
	addRoot(&frames, "s2c", `{"id":104,"result":{}}`)
	addRoot(&frames, "s2c", `{"method":"thread/name/updated","params":{"threadId":"thread-001","threadName":"pending approval"}}`)
	addRoot(&frames, "c2s", `{"id":"held","result":{"decision":"accept"}}`)
	addRoot(&frames, "s2c", `{"method":"serverRequest/resolved","params":{"threadId":"thread-001","requestId":"held"}}`)
	addRoot(&frames, "s2c", `{"method":"item/completed","params":{"threadId":"thread-001","turnId":"turn-001","item":{"type":"commandExecution","id":"item-001","command":"DO NOT EXECUTE","status":"completed","aggregatedOutput":"17\n25\n","exitCode":0}}}`)
	addRoot(&frames, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-001","items":[],"status":"completed","error":null}}}`)

	var deliveries bytes.Buffer
	runSyntheticApprovalFlow(t, frames, &deliveries)

	var methods []string
	decoder := json.NewDecoder(&deliveries)
	for decoder.More() {
		var delivery struct {
			Raw string `json:"raw"`
		}
		require.NoError(t, decoder.Decode(&delivery))
		if delivery.Raw == "" {
			continue
		}
		var message struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal([]byte(delivery.Raw), &message))
		methods = append(methods, message.Method)
	}
	require.Equal(t, []string{
		"item/commandExecution/requestApproval",
		"thread/name/updated",
		"serverRequest/resolved",
		"item/completed",
		"turn/completed",
	}, methods)
}

func TestFlowAnswersStandaloneElicitationOnOriginalRequests(t *testing.T) {
	frames := rootPrefix(t)
	addRoot(&frames, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic task"}]}}`)
	addRoot(&frames, "s2c", `{"id":103,"result":{"turn":{"id":"turn-001","items":[],"status":"inProgress"}}}`)
	for i, tc := range []struct {
		params string
		answer string
	}{
		{`"mode":"form","requestedSchema":{"type":"object"}`, `{"action":"accept","content":{"value":"synthetic","number":9007199254740993},"_meta":{"trace":null}}`},
		{`"mode":"form","requestedSchema":{"type":"object"}`, `{"action":"decline","content":null,"_meta":null}`},
		{`"mode":"form","requestedSchema":{"type":"object"}`, `{"action":"cancel","content":null,"_meta":null}`},
		{`"mode":"url","url":"https://example.invalid","elicitationId":"url-accept"`, `{"action":"accept","content":null,"_meta":null}`},
		{`"mode":"url","url":"https://example.invalid","elicitationId":"url-decline"`, `{"action":"decline","content":null,"_meta":null}`},
		{`"mode":"url","url":"https://example.invalid","elicitationId":"url-cancel"`, `{"action":"cancel","content":null,"_meta":null}`},
	} {
		addRoot(&frames, "s2c", fmt.Sprintf(`{"id":%d,"method":"mcpServer/elicitation/request","params":{"threadId":"thread-001","turnId":"turn-001","serverName":"elicitation_fixture","message":"Synthetic request","_meta":null,%s}}`, i, tc.params))
		addRoot(&frames, "c2s", fmt.Sprintf(`{"id":%d,"result":%s}`, i, tc.answer))
		addRoot(&frames, "s2c", fmt.Sprintf(`{"method":"serverRequest/resolved","params":{"threadId":"thread-001","requestId":%d}}`, i))
	}
	addRoot(&frames, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-001","items":[],"status":"completed","error":null}}}`)
	runSyntheticApprovalFlow(t, frames, io.Discard)
}

func runSyntheticApprovalFlow(t *testing.T, frames []recording.Frame, deliveries io.Writer) {
	t.Helper()
	var capture bytes.Buffer
	encoder := json.NewEncoder(&capture)
	for _, frame := range frames {
		require.NoError(t, encoder.Encode(frame))
	}
	source := filepath.Join(t.TempDir(), "raw.jsonl")
	require.NoError(t, os.WriteFile(source, capture.Bytes(), 0o600))
	mock, err := mocktest.Prepare(t.TempDir(), "codex", capture.Bytes(), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { verifySDKSession(t, mock, "passed") })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, AcceptFlow(ctx, mock, source, codex.ChildDetailPreserve, deliveries))
}
