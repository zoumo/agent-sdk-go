// SDK integration against the Go mock CLI; all scenarios are synthetic.
package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type mockCLISuite struct {
	suite.Suite
	executable string
}

func TestMockCLI(t *testing.T) { suite.Run(t, new(mockCLISuite)) }

func (s *mockCLISuite) SetupTest() {
	var err error
	s.executable, err = mocktest.Executable()
	s.Require().NoError(err)
}

func mockCLIScenario(t *testing.T, wires ...string) string {
	t.Helper()
	frames := make([]map[string]any, 0, len(wires))
	for i, wire := range wires {
		direction, data := wire[:1], wire[1:]
		frame := map[string]any{"ordinal": i + 1, "channel": "protocol"}
		frame["epoch"] = 1
		frame["direction"] = "s2c"
		if direction == ">" {
			frame["direction"] = "c2s"
		}
		if direction == "!" {
			frame["disconnect"] = true
		} else {
			frame["wire"] = json.RawMessage(data)
		}
		frames = append(frames, frame)
	}
	b, err := json.Marshal(map[string]any{"version": 1, "provenance": "synthetic", "frames": frames})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "codex.json")
	require.NoError(t, os.WriteFile(path, b, 0600))
	return path
}

var mockCodexInit = []string{
	`>{"id":91,"method":"initialize","params":{"clientInfo":{"name":"consumer","version":"1"}}}`,
	`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
	`>{"method":"initialized"}`,
}
var mockCodexOpen = []string{
	`>{"id":100,"method":"thread/start","params":{}}`,
	`<{"id":100,"result":{"thread":{"id":"root","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}}`,
}

func (s *mockCLISuite) TestInteractions() {
	for _, mode := range []string{"question-and-interrupt", "disconnect", "mismatch"} {
		s.Run(mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames := append(append([]string(nil), mockCodexInit...), mockCodexOpen...)
			if mode == "question-and-interrupt" {
				frames = append(frames,
					`>{"id":92,"method":"consumer/ask","params":{}}`,
					`<{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"turn","itemId":"item","questions":[{"id":"format","header":"Format","question":"Which format?","options":[{"label":"json","description":"JSON output"}],"isOther":false,"isSecret":false}]}}`,
					`>{"id":"question","result":{"answers":{"format":{"answers":["json"]}}}}`,
					`<{"id":92,"result":{"answered":true}}`,
					`>{"id":93,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`,
					`<{"id":93,"result":{}}`)
			} else if mode == "disconnect" {
				frames = append(frames, `>{"id":92,"method":"consumer/fault","params":{}}`, `!`)
			} else {
				frames = append(frames, `>{"id":92,"method":"consumer/expected","params":{}}`)
			}
			path := mockCLIScenario(t, frames...)
			client, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: s.executable, Args: []string{"mock", "--provider", "codex", "--scenario", path, "app-server"}, Env: []string{"PATH="}, Initialize: codextypes.InitializeParams{ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"}}})
			require.NoError(t, err)
			defer client.Close()
			session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
			require.NoError(t, err)
			defer func() { _ = client.Close(); _ = session.Close() }()
			if mode != "question-and-interrupt" {
				var result any
				err = client.Call(ctx, "consumer/fault", map[string]any{}, &result)
				require.Error(t, err)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, ctx.Err())
				require.NoError(t, client.Close())
				require.Greater(t, client.Diagnostics().StderrBytes, uint64(0))
				return
			}
			result := make(chan error, 1)
			go func() {
				var response struct {
					Answered bool `json:"answered"`
				}
				err := client.Call(ctx, "consumer/ask", map[string]any{}, &response)
				if err == nil && !response.Answered {
					err = context.Canceled
				}
				result <- err
			}()
			received, err := session.Receive(ctx)
			require.NoError(t, err)
			require.NotNil(t, received.Request)
			request := received.Request
			require.Equal(t, "item/tool/requestUserInput", request.Method())
			require.NoError(t, request.Answer(ctx, codextypes.ToolRequestUserInputResponse{Answers: map[string]codextypes.ToolRequestUserInputAnswer{"format": {Answers: []string{"json"}}}}))

			require.NoError(t, <-result)
			canceled, stop := context.WithCancel(ctx)
			stop()
			err = session.Interrupt(canceled, codex.InterruptOptions{TurnID: "turn"})
			require.ErrorIs(t, err, context.Canceled)
			err = session.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"})
			require.NoError(t, err)
			require.NoError(t, client.Close())
			assert.Zero(t, client.Diagnostics().StderrBytes)
		})
	}
}

// Controls and answers must remain available during an unread event burst.
// After disconnection, every queued event must drain in order.
func (s *mockCLISuite) TestSlowConsumerBurst() {
	const count = 2048
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	frames := append(append([]string(nil), mockCodexInit...), mockCodexOpen...)
	frames = append(frames, `<{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"turn","itemId":"item","questions":[]}}`)
	for i := 0; i < count; i++ {
		frames = append(frames, fmt.Sprintf(`<{"method":"fixture/burst","params":{"threadId":"root","index":%d}}`, i))
	}
	frames = append(frames, `>{"id":93,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`, `<{"id":93,"result":{}}`, `>{"id":"question","result":{"answers":{}}}`, `>{"id":94,"method":"consumer/release","params":{}}`, `!`)
	path := mockCLIScenario(t, frames...)
	client, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: s.executable, Args: []string{"mock", "--provider", "codex", "--scenario", path, "app-server"}, Env: []string{"PATH="}, Initialize: codextypes.InitializeParams{ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"}}})
	require.NoError(t, err)
	defer client.Close()
	session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
	require.NoError(t, err)
	defer session.Abort()
	request, err := session.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, request.Request)
	require.NoError(t, session.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"}))
	require.NoError(t, request.Request.Answer(ctx, codextypes.ToolRequestUserInputResponse{Answers: map[string]codextypes.ToolRequestUserInputAnswer{}}))
	require.Error(t, client.Call(ctx, "consumer/release", map[string]any{}, nil))
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Error(t, session.Err())
	seen := 0
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, count, seen)
			break
		}
		require.NotNil(t, record.Event)
		require.Equal(t, "fixture/burst", record.Event.Method)
		var payload struct{ Index int }
		require.NoError(t, json.Unmarshal(record.Params, &payload))
		require.Equal(t, seen, payload.Index)
		seen++
	}
}
