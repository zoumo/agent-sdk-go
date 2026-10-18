// All scenarios here are synthetic protocol examples, not real CLI recordings.
package mockconsumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	claude "github.com/zoumo/agent-sdk-go/claude"
	codex "github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

type consumerSuite struct{ suite.Suite }

func TestMockConsumer(t *testing.T) {
	if os.Getenv("SDK_MOCK_CODEX_BIN") == "" || os.Getenv("SDK_MOCK_CLAUDE_BIN") == "" {
		t.Skip("run scripts/check-consumers.sh with a pushed revision to build external mock CLIs")
	}
	suite.Run(t, new(consumerSuite))
}
func scenario(t *testing.T, native string, wires ...string) string {
	t.Helper()
	frames := make([]map[string]any, 0, len(wires))
	for i, wire := range wires {
		direction, data := wire[:1], wire[1:]
		frame := map[string]any{"ordinal": i + 1, "channel": "protocol"}
		if native == "codex" {
			frame["epoch"] = 1
			if direction == ">" {
				frame["direction"] = "c2s"
			} else {
				frame["direction"] = "s2c"
			}
			if direction == "!" {
				frame["disconnect"] = true
			} else {
				frame["wire"] = json.RawMessage(data)
			}
		} else {
			if direction == ">" {
				frame["direction"] = "client"
			} else {
				frame["direction"] = "server"
			}
			if direction == "!" {
				frame["direction"] = "disconnect"
			} else {
				frame["data"] = json.RawMessage(data)
			}
		}
		frames = append(frames, frame)
	}
	b, err := json.Marshal(map[string]any{"version": 1, "provenance": "synthetic", "frames": frames})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), native+".json")
	require.NoError(t, os.WriteFile(path, b, 0600))
	return path
}

var codexInit = []string{
	`>{"id":91,"method":"initialize","params":{"clientInfo":{"name":"consumer","version":"1"}}}`,
	`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
	`>{"method":"initialized"}`,
}
var claudeInit = []string{
	`>{"type":"control_request","request_id":"init","request":{"subtype":"initialize","hooks":null,"forwardSubagentText":true}}`,
	`<{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{}}}`,
}

func (s *consumerSuite) TestCodex() {
	for _, mode := range []string{"question-and-interrupt", "disconnect", "mismatch"} {
		s.Run(mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames := append([]string(nil), codexInit...)
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
			path := scenario(t, "codex", frames...)
			client, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: os.Getenv("SDK_MOCK_CODEX_BIN"), Args: []string{"app-server"}, Env: []string{"CODEX_MOCK_SCENARIO=" + path}, Initialize: codextypes.InitializeParams{ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"}}})
			require.NoError(t, err)
			defer client.Close()
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
			select {
			case request := <-client.Requests():
				require.Equal(t, "item/tool/requestUserInput", request.Method())
				require.NoError(t, request.Handle().Answer(ctx, json.RawMessage(`{"answers":{"format":{"answers":["json"]}}}`)))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.NoError(t, <-result)
			canceled, stop := context.WithCancel(ctx)
			stop()
			_, err = client.InterruptTurn(canceled, codextypes.TurnInterruptParams{ThreadID: "root", TurnID: "turn"})
			require.ErrorIs(t, err, context.Canceled)
			_, err = client.InterruptTurn(ctx, codextypes.TurnInterruptParams{ThreadID: "root", TurnID: "turn"})
			require.NoError(t, err)
			require.NoError(t, client.Close())
			assert.Zero(t, client.Diagnostics().StderrBytes)
		})
	}
}
func (s *consumerSuite) TestClaude() {
	for _, mode := range []string{"permission-and-interrupt", "disconnect", "mismatch"} {
		s.Run(mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames := append([]string(nil), claudeInit...)
			frames = append(frames, `>{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`)
			if mode == "permission-and-interrupt" {
				frames = append(frames,
					`<{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]},"tool_use_id":"tool"}}`,
					`>{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{"answers":{"format":"json"}}}}}`,
					`<{"type":"result","subtype":"success","session_id":"synthetic","is_error":false,"result":"answered"}`,
					`>{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`,
					`<{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`)
			} else if mode == "disconnect" {
				frames = append(frames, `!`)
			} else {
				frames[len(frames)-1] = `>{"type":"user","message":{"role":"user","content":"different"},"parent_tool_use_id":null,"session_id":""}`
			}
			path := scenario(t, "claude", frames...)
			permissions := make(chan *claude.PermissionRequest, 1)
			client, err := claude.Open(ctx, claude.Options{CLIPath: os.Getenv("SDK_MOCK_CLAUDE_BIN"), Env: []string{"CLAUDE_MOCK_SCENARIO=" + path}, PermissionHandler: func(p *claude.PermissionRequest) { permissions <- p }})
			require.NoError(t, err)
			defer client.Abort()
			require.NoError(t, client.Send(ctx, "hello"))
			if mode != "permission-and-interrupt" {
				for {
					_, err := client.Receive(ctx)
					if err != nil {
						require.NotErrorIs(t, err, context.DeadlineExceeded)
						require.NoError(t, ctx.Err())
						closeErr := client.Close()
						var nativeErr *claude.Error
						require.ErrorAs(t, closeErr, &nativeErr)
						require.NotNil(t, nativeErr.ExitCode)
						wantCode := 1
						if mode == "disconnect" {
							wantCode = 4
						}
						assert.Equal(t, wantCode, *nativeErr.ExitCode)
						break
					}
				}
				return
			}
			select {
			case permission := <-permissions:
				require.NoError(t, permission.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage(`{"answers":{"format":"json"}}`)}))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for {
				event, err := client.Receive(ctx)
				require.NoError(t, err)
				if event, ok := event.(claude.Event); ok && event.Result != nil {
					assert.Equal(t, "answered", event.Result.Result)
					break
				}
			}
			canceled, stop := context.WithCancel(ctx)
			stop()
			require.ErrorIs(t, client.Interrupt(canceled), context.Canceled)
			require.NoError(t, client.Interrupt(ctx))
			require.NoError(t, client.Close())
		})
	}
}

// Check actual process completion independently of SDK shutdown, which can kill
// its child. EOF, extra input and intentional faults have distinct exit status.
func (s *consumerSuite) TestCommandExit() {
	for _, native := range []string{"codex", "claude"} {
		for _, mode := range []string{"complete", "extra", "disconnect"} {
			s.Run(native+"/"+mode, func() {
				t := s.T()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				frames := append([]string(nil), codexInit...)
				envName := "CODEX_MOCK_SCENARIO"
				bin := os.Getenv("SDK_MOCK_CODEX_BIN")
				args := []string{"app-server"}
				if native == "claude" {
					frames = append([]string(nil), claudeInit...)
					envName = "CLAUDE_MOCK_SCENARIO"
					bin = os.Getenv("SDK_MOCK_CLAUDE_BIN")
					args = nil
				}
				var input bytes.Buffer
				for _, frame := range frames {
					if frame[0] == '>' {
						input.WriteString(frame[1:] + "\n")
					}
				}
				if mode == "extra" {
					input.WriteString("{}\n")
				}
				if mode == "disconnect" {
					frames = append(frames, "!")
				}
				path := scenario(t, native, frames...)
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Env = []string{envName + "=" + path}
				cmd.Stdin = &input
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				require.NoError(t, ctx.Err())
				if mode == "complete" {
					require.NoError(t, err)
					assert.Empty(t, stderr.String())
				} else {
					var exit *exec.ExitError
					require.True(t, errors.As(err, &exit))
					want := 1
					if mode == "disconnect" {
						want = 4
					}
					assert.Equal(t, want, exit.ExitCode())
					assert.NotEmpty(t, stderr.String())
				}
				decoder := json.NewDecoder(&stdout)
				var response map[string]any
				require.NoError(t, decoder.Decode(&response))
				assert.NotEmpty(t, response)
			})
		}
	}
}

// The mock waits for real question/control answers after writing the entire burst.
// Until those replies arrive the SDK consumer deliberately reads no events.
func (s *consumerSuite) TestSlowConsumerBurst() {
	const count = 2048
	for _, native := range []string{"codex", "claude"} {
		s.Run(native, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if native == "codex" {
				frames := append([]string(nil), codexInit...)
				frames = append(frames, `>{"id":92,"method":"consumer/burst","params":{}}`)
				for i := 0; i < count; i++ {
					frames = append(frames, fmt.Sprintf(`<{"method":"fixture/burst","params":{"threadId":"root","index":%d}}`, i))
				}
				frames = append(frames,
					`<{"id":"question","method":"item/tool/requestUserInput","params":{"threadId":"root","turnId":"turn","itemId":"item","questions":[]}}`,
					`>{"id":"question","result":{"answers":{}}}`,
					`<{"id":92,"result":{}}`,
					`>{"id":93,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`,
					`<{"id":93,"result":{}}`,
					`>{"id":94,"method":"consumer/release","params":{}}`, `!`)
				path := scenario(t, native, frames...)
				client, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: os.Getenv("SDK_MOCK_CODEX_BIN"), Args: []string{"app-server"}, Env: []string{"CODEX_MOCK_SCENARIO=" + path}, Initialize: codextypes.InitializeParams{ClientInfo: codextypes.ClientInfo{Name: "consumer", Version: "1"}}})
				require.NoError(t, err)
				defer client.Close()
				sub, err := client.Subscribe(ctx, "root", codextypes.SubscriptionOptions{EventQueue: 1})
				require.NoError(t, err)
				defer sub.Abort()
				response := make(chan error, 1)
				go func() { response <- client.Call(ctx, "consumer/burst", map[string]any{}, nil) }()
				select {
				case request := <-client.Requests():
					require.NoError(t, request.Handle().Answer(ctx, json.RawMessage(`{"answers":{}}`)))
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				require.NoError(t, <-response)
				_, err = client.InterruptTurn(ctx, codextypes.TurnInterruptParams{ThreadID: "root", TurnID: "turn"})
				require.NoError(t, err)
				require.Error(t, client.Call(ctx, "consumer/release", map[string]any{}, nil))
				select {
				case <-sub.Done():
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				require.Error(t, sub.Err()) // terminal transport error is visible before draining
				seen := 0
				startup := 0
				for {
					select {
					case event, ok := <-sub.Events():
						if !ok {
							require.Equal(t, count, seen)
							require.Equal(t, 1, startup, "initial derived observation must survive the burst")
							return
						}
						if state, ok := event.Data.(*codextypes.ObservationState); ok && state.Reason == "best-effort-auto-attach-unverified" {
							require.Zero(t, seen, "initial derived observation must precede native events")
							startup++
						}
						if event.Method != "fixture/burst" {
							continue
						}
						var payload struct{ Index int }
						require.NoError(t, json.Unmarshal(event.Params, &payload))
						require.Equal(t, seen, payload.Index)
						seen++
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			frames := append([]string(nil), claudeInit...)
			frames = append(frames, `>{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}`)
			for i := 0; i < count; i++ {
				frames = append(frames, fmt.Sprintf(`<{"type":"fixture_burst","session_id":"synthetic","index":%d}`, i))
			}
			frames = append(frames,
				`<{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]},"tool_use_id":"tool"}}`,
				`>{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow","updatedInput":{}}}}`,
				`>{"type":"control_request","request_id":"interrupt","request":{"subtype":"interrupt"}}`,
				`<{"type":"control_response","response":{"subtype":"success","request_id":"interrupt","response":{}}}`,
				`>{"type":"user","message":{"role":"user","content":"release"},"parent_tool_use_id":null,"session_id":""}`, `!`)
			path := scenario(t, native, frames...)
			permissions := make(chan *claude.PermissionRequest, 1)
			client, err := claude.Open(ctx, claude.Options{CLIPath: os.Getenv("SDK_MOCK_CLAUDE_BIN"), Env: []string{"CLAUDE_MOCK_SCENARIO=" + path}, Limits: claude.Limits{EventBuffer: 1}, PermissionHandler: func(p *claude.PermissionRequest) { permissions <- p }})
			require.NoError(t, err)
			defer client.Abort()
			require.NoError(t, client.Send(ctx, "hello"))
			select {
			case p := <-permissions:
				require.NoError(t, p.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage(`{}`)}))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.NoError(t, client.Interrupt(ctx))
			// The final input releases the fault only after the control ack is observed.
			err = client.Send(ctx, "release")
			require.NotErrorIs(t, err, context.DeadlineExceeded)
			select {
			case <-client.Done():
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Error(t, client.Err())
			canceled, stop := context.WithCancel(ctx)
			stop()
			_, err = client.Receive(canceled)
			require.ErrorIs(t, err, context.Canceled)
			seen := 0
			startup := 0
			for {
				event, err := client.Receive(ctx)
				if err != nil {
					require.NotErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, count, seen)
					require.Equal(t, 1, startup, "initial derived observation must survive the burst")
					var terminal *claude.Error
					require.ErrorAs(t, client.Close(), &terminal)
					require.NotNil(t, terminal.ExitCode)
					require.Equal(t, 4, *terminal.ExitCode)
					break
				}
				if state, ok := event.(claude.ObservationState); ok && state.Reason == "forwarding-not-confirmed" {
					require.Zero(t, seen, "initial derived observation must precede native events")
					startup++
				}
				value, ok := event.(claude.Event)
				if !ok || value.Type != "fixture_burst" {
					continue
				}
				var payload struct{ Index int }
				require.NoError(t, json.Unmarshal(value.Raw, &payload))
				require.Equal(t, seen, payload.Index)
				seen++
			}
		})
	}
}
