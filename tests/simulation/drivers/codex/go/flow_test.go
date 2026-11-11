package codexsim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestJoinInputCloseError(t *testing.T) {
	prior := errors.New("prior failure")
	closeFailure := errors.New("close failure")
	for _, tc := range []struct {
		name     string
		result   error
		inputErr error
		want     []error
	}{
		{name: "no error"},
		{name: "expected sentinel"},
		{name: "wrapped expected sentinel", inputErr: fmt.Errorf("watch: %w", codex.ErrInputClosed)},
		{name: "preserves prior result with sentinel", result: prior, inputErr: codex.ErrInputClosed, want: []error{prior}},
		{name: "joins unexpected failure", inputErr: closeFailure, want: []error{closeFailure}},
		{name: "joins with prior result", result: prior, inputErr: closeFailure, want: []error{prior, closeFailure}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := joinInputCloseError(tc.result, tc.inputErr)
			if len(tc.want) == 0 {
				require.NoError(t, got)
			}
			for _, want := range tc.want {
				require.ErrorIs(t, got, want)
			}
			require.NotErrorIs(t, got, codex.ErrInputClosed)
		})
	}
}

func TestFlowReproducesConfigurationApprovalAndNativeFacts(t *testing.T) {
	for _, decision := range []string{"accept", "decline"} {
		t.Run(decision, func(t *testing.T) {
			fs := rootPrefix(t)
			// Real initializations may emit notifications before initialized.
			fs = append(fs[:2], append([]recording.Frame{frame(0, "s2c", `{"method":"remoteControl/status/changed","params":{"enabled":false}}`)}, fs[2:]...)...)
			addRoot(&fs, "c2s", `{"id":103,"method":"thread/settings/update","params":{"threadId":"thread-001","model":"synthetic-model"}}`)
			addRoot(&fs, "s2c", `{"id":103,"result":{"futureReceipt":true}}`)
			addRoot(&fs, "c2s", `{"id":104,"method":"thread/inject_items","params":{"threadId":"thread-001","items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"synthetic context"}]}]}}`)
			addRoot(&fs, "s2c", `{"id":104,"result":{}}`)
			addRoot(&fs, "c2s", `{"id":105,"method":"thread/loaded/list","params":{}}`)
			addRoot(&fs, "s2c", `{"id":105,"result":{"data":["thread-001"],"nextCursor":null}}`)
			addRoot(&fs, "c2s", `{"id":106,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic input"}]}}`)
			addRoot(&fs, "s2c", `{"id":106,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
			addRoot(&fs, "s2c", `{"id":"permission","method":"item/commandExecution/requestApproval","params":{"threadId":"thread-001","turnId":"turn-1","itemId":"item-1","command":"DO NOT EXECUTE"}}`)
			addRoot(&fs, "c2s", `{"id":"permission","result":{"decision":"`+decision+`"}}`)
			addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"completed","error":null}}}`)
			var capture bytes.Buffer
			for i, f := range fs {
				row := struct {
					Ordinal   int             `json:"ordinal"`
					Direction string          `json:"direction"`
					Channel   string          `json:"channel"`
					Epoch     int             `json:"epoch"`
					ElapsedMS int             `json:"elapsedMs"`
					Wire      json.RawMessage `json:"wire"`
				}{i + 1, f.Direction, "protocol", 1, 0, f.Wire}
				require.NoError(t, json.NewEncoder(&capture).Encode(row))
			}
			root := t.TempDir()
			source := filepath.Join(root, "raw.jsonl")
			require.NoError(t, os.WriteFile(source, capture.Bytes(), 0o600))
			data, err := os.ReadFile(source)
			require.NoError(t, err)
			peerSession, err := mocktest.Prepare(t.TempDir(), "codex", data, mocktest.Options{})
			require.NoError(t, err)
			t.Cleanup(func() { verifySDKSession(t, peerSession, "passed") })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, AcceptFlow(ctx, peerSession, source, codex.ChildDetailPreserve, io.Discard))
		})
	}
}

func frame(n int, d, w string) recording.Frame {
	return recording.Frame{Ordinal: n, Direction: d, Epoch: 1, Channel: "protocol", Wire: json.RawMessage(w)}
}

func rootPrefix(t *testing.T) []recording.Frame {
	t.Helper()
	b, err := fixturedata.Files.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, err)
	fs, err := recording.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	fs = fs[:5]
	fs[0].Wire = json.RawMessage(`{"id":101,"method":"initialize","params":{"clientInfo":{"name":"synthetic-preflight","version":"1"},"capabilities":{"experimentalApi":true}}}`)
	return fs
}
func addRoot(fs *[]recording.Frame, d, w string) { *fs = append(*fs, frame(len(*fs)+1, d, w)) }

func verifySDKSession(t *testing.T, session *mocktest.Session, want string) {
	t.Helper()
	report, err := session.Report()
	require.NotNil(t, report)
	require.NoError(t, err)
	require.Equal(t, want, report.Status)
	require.NoError(t, session.Close())
}

func TestBusinessFailureClosesStreamsBeforeJoiningReceiver(t *testing.T) {
	frames := rootPrefix(t)
	addRoot(&frames, "c2s", `{"id":103,"method":"unsupported/operation","params":{}}`)
	addRoot(&frames, "s2c", `{"id":103,"result":{}}`)
	var capture bytes.Buffer
	for i, f := range frames {
		require.NoError(t, json.NewEncoder(&capture).Encode(map[string]any{"ordinal": i + 1, "direction": f.Direction, "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": f.Wire}))
	}
	source := filepath.Join(t.TempDir(), "raw.jsonl")
	require.NoError(t, os.WriteFile(source, capture.Bytes(), 0o600))
	mock, err := mocktest.Prepare(t.TempDir(), "codex", capture.Bytes(), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	started := time.Now()
	err = AcceptFlow(ctx, mock, source, codex.ChildDetailPreserve, io.Discard)
	require.ErrorContains(t, err, "unsupported recorded operation")
	require.Less(t, time.Since(started), 2*time.Second)
	require.NoError(t, ctx.Err())
	report, err := mock.Report()
	require.NoError(t, err)
	require.Equal(t, "failed", report.Status)
}
