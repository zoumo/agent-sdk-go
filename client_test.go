// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// All fixtures are local re-executions of this test binary under a replacement
// environment and temporary HOME. No installed CLI or account is ever used.
func TestMain(m *testing.M) {
	if mode := os.Getenv("CLAUDE_GO_FIXTURE"); mode != "" {
		runFixture(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureEmit(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		os.Exit(4)
	}
}
func runFixture(mode string) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		os.Exit(5)
	}
	var init map[string]any
	if json.Unmarshal(line, &init) != nil {
		os.Exit(6)
	}
	if path := os.Getenv("CLAUDE_GO_CAPTURE"); path != "" {
		cwd, _ := os.Getwd()
		data, _ := json.Marshal(map[string]any{"argv": os.Args[1:], "init": init, "cwd": cwd, "home": os.Getenv("HOME"), "pid": os.Getpid()})
		if os.WriteFile(path, data, 0600) != nil {
			os.Exit(7)
		}
	}
	switch mode {
	case "session_identity_at_initialize":
		fixtureEmit(map[string]any{"type": "system", "subtype": "init", "session_id": "native-at-initialize"})
	case "init_stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("diagnostic", 20000))
	case "init_hang":
		time.Sleep(time.Hour)
		return
	case "init_disconnect":
		fmt.Fprint(os.Stderr, "startup disconnected")
		return
	case "init_error":
		fmt.Fprint(os.Stderr, "initialize rejected")
		fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": init["request_id"], "error": "fixture refused initialization"}})
		io.Copy(io.Discard, reader)
		return
	case "init_wrong":
		fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "not-the-request", "response": map[string]any{}}})
		io.Copy(io.Discard, reader)
		return
	}
	fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": init["request_id"], "response": map[string]any{"commands": []any{}}}})
	if strings.HasPrefix(mode, "permission_") {
		runPermissionFixture(mode, reader)
		return
	}
	if strings.HasPrefix(mode, "session_") {
		runSessionFixture(mode, reader)
		return
	}
	if mode == "blocked_write" || mode == "closed_stdin" {
		if mode == "closed_stdin" {
			os.Stdin.Close()
		}
		fixtureEmit(map[string]any{"type": "ready"})
		time.Sleep(time.Hour)
		return
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if mode == "ignore_eof" {
				time.Sleep(time.Hour)
			}
			return
		}
		var message map[string]any
		if json.Unmarshal(line, &message) != nil {
			os.Exit(8)
		}
		if message["type"] == "control_response" {
			fixtureEmit(map[string]any{"type": "reply_seen", "wire": message})
			continue
		}
		switch mode {
		case "frames":
			frames := []string{
				` {"type":"system","subtype":"init","session_id":"native-123","model":"fixture","tools":["Read"],"new_field":{"preserved":true}} `,
				`{"type":"assistant","session_id":"native-123","message":{"id":"m1","role":"assistant","model":"fixture","content":[{"type":"text","text":"hello\nworld"},{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"tool_use","id":"t1","name":"AskUserQuestion","input":{"questions":[],"future":42}},{"type":"future_block","extra":[1,2]}],"new_nested":true}}`,
				`{"type":"user","session_id":"native-123","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"answer"}],"is_error":false,"extra":"keep"}]}}`,
				`{"type":"stream_event","session_id":"native-123","uuid":"delta1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"delta"},"future":true}}`,
				`{"type":"result","subtype":"success","session_id":"native-123","result":"done","is_error":false,"num_turns":1,"duration_ms":4,"duration_api_ms":2,"usage":{"output_tokens":3},"new_result":true}`,
				`{"type":"future_event","nested":{"x":[true,1,"raw"]}}`,
			}
			for _, frame := range frames {
				fmt.Fprintln(os.Stdout, frame)
			}
		case "malformed":
			fmt.Fprintln(os.Stdout, `{"type":"accepted","seq":1}`)
			fmt.Fprintln(os.Stdout, `{"type":`)
			return
		case "truncated":
			fmt.Fprintln(os.Stdout, `{"type":"accepted","seq":1}`)
			fmt.Fprint(os.Stdout, `{"type":"future"}`)
			return
		case "invalid_utf8":
			os.Stdout.Write([]byte{'{', '"', 't', 'y', 'p', 'e', '"', ':', '"', 0xff, '"', '}', '\n'})
			return
		case "invalid_type":
			fmt.Fprintln(os.Stdout, `{"type":42}`)
			return
		case "oversize":
			fmt.Fprintln(os.Stdout, `{"type":"accepted","seq":1}`)
			fmt.Fprintf(os.Stdout, "{\"type\":\"large\",\"text\":%q}\n", strings.Repeat("x", 2048))
			return
		case "oversize_without_newline":
			fmt.Fprint(os.Stdout, strings.Repeat("x", 1025))
			time.Sleep(time.Hour)
			return
		case "overflow":
			for i := 0; i < 20; i++ {
				fixtureEmit(map[string]any{"type": "sequence", "seq": i})
			}
		case "exit_error":
			fmt.Fprintln(os.Stdout, `{"type":"result","subtype":"success","result":"last"}`)
			fmt.Fprint(os.Stderr, strings.Repeat("x", 128)+"tail-marker")
			os.Exit(17)
		case "eof":
			fmt.Fprintln(os.Stdout, `{"type":"accepted"}`)
			return
		case "permissions":
			fixtureEmit(map[string]any{"type": "control_request", "request_id": "permission-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{"path": "test"}, "tool_use_id": "t1"}})
			fixtureEmit(map[string]any{"type": "control_request", "request_id": "unsupported-2", "request": map[string]any{"subtype": "future_control"}})
		case "fragmented":
			value := "start " + strings.Repeat("雪 ", 14000) + " end\nnext"
			data, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": value}})
			for i := 0; i < len(data); {
				end := min(i+137, len(data))
				os.Stdout.Write(data[i:end])
				i = end
			}
			os.Stdout.Write([]byte{'\n'})
		default:
			fixtureEmit(map[string]any{"type": "echo", "wire": message})
		}
	}
}

func fixtureOptions(t *testing.T, mode string) claude.Options {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return claude.Options{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "CLAUDE_GO_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, InitializeTimeout: 3 * time.Second, ShutdownTimeout: 600 * time.Millisecond}
}
func openFixture(t *testing.T, mode string, configure ...func(*claude.Options)) *claude.Client {
	t.Helper()
	opts := fixtureOptions(t, mode)
	for _, fn := range configure {
		fn(&opts)
	}
	c, err := claude.Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func receive(t *testing.T, c *claude.Client) claude.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	event, err := c.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func send(t *testing.T, c *claude.Client, text string) {
	t.Helper()
	if err := c.Send(context.Background(), text); err != nil {
		t.Fatal(err)
	}
}
func awaitDone(t *testing.T, c *claude.Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done did not close")
	}
}

func TestOpenUsesLiteralArgumentsAndMatchedInitialize(t *testing.T) {
	opts := fixtureOptions(t, "echo")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	opts.Model = "--odd model; $(not-a-shell)"
	c, err := claude.Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Argv      []string
		Init      map[string]json.RawMessage
		CWD, Home string
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	want := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages", "--model=" + opts.Model}
	if !reflect.DeepEqual(captured.Argv, want) {
		t.Fatalf("argv = %#v", captured.Argv)
	}
	if captured.CWD != opts.WorkDir || captured.Home != opts.WorkDir {
		t.Fatalf("environment not isolated: %s", data)
	}
	if string(captured.Init["type"]) != `"control_request"` || len(captured.Init["request_id"]) < 3 {
		t.Fatalf("initialize envelope: %s", data)
	}
	var request map[string]any
	json.Unmarshal(captured.Init["request"], &request)
	if request["subtype"] != "initialize" || request["hooks"] != nil {
		t.Fatalf("initialize = %#v", request)
	}
	text := "a literal\nmessage with \"quotes\" and 雪"
	send(t, c, text)
	e := receive(t, c)
	var echo struct {
		Wire struct {
			Type    string
			Message struct{ Role, Content string }
			Parent  json.RawMessage `json:"parent_tool_use_id"`
			Session string          `json:"session_id"`
		}
	}
	if err := json.Unmarshal(e.Raw, &echo); err != nil {
		t.Fatal(err)
	}
	if echo.Wire.Type != "user" || echo.Wire.Message.Role != "user" || echo.Wire.Message.Content != text || string(echo.Wire.Parent) != "null" {
		t.Fatalf("send frame: %s", e.Raw)
	}
}

func TestTypedViewsAndLosslessUnknowns(t *testing.T) {
	c := openFixture(t, "frames")
	send(t, c, "first")
	events := make([]claude.Event, 6)
	for i := range events {
		events[i] = receive(t, c)
	}
	want := []string{"system", "assistant", "user", "stream_event", "result", "future_event"}
	for i, event := range events {
		if event.Type != want[i] {
			t.Fatalf("event %d: %s", i, event.Type)
		}
	}
	if events[0].System.SessionID != "native-123" || !bytes.HasPrefix(events[0].Raw, []byte(" {")) || !bytes.Contains(events[0].Raw, []byte(`"new_field"`)) {
		t.Fatalf("system/raw: %s", events[0].Raw)
	}
	a := events[1].Assistant
	if a.Content.Blocks[0].Text.Text != "hello\nworld" || a.Content.Blocks[1].Thinking.Signature != "sig" || a.Content.Blocks[2].ToolUse.Name != "AskUserQuestion" {
		t.Fatalf("assistant = %#v", a)
	}
	unknown := a.Content.Blocks[3]
	if unknown.Type != "future_block" || string(unknown.Raw) != `{"type":"future_block","extra":[1,2]}` {
		t.Fatalf("lost block: %s", unknown.Raw)
	}
	if !bytes.Contains(a.Raw, []byte(`"new_nested":true`)) {
		t.Fatal("nested data lost")
	}
	toolResult := events[2].User.Content.Blocks[0].ToolResult
	if toolResult.ToolUseID != "t1" || string(toolResult.Content) != `[{"type":"text","text":"answer"}]` {
		t.Fatalf("tool result = %#v", toolResult)
	}
	if events[3].Stream.Delta.Type != "content_block_delta" || *events[3].Stream.Delta.Index != 0 {
		t.Fatal("missing delta view")
	}
	if events[4].Result.Result != "done" || events[4].Result.NumTurns != 1 {
		t.Fatal("missing result view")
	}
	if string(events[5].Raw) != `{"type":"future_event","nested":{"x":[true,1,"raw"]}}` {
		t.Fatal("unknown event changed")
	}
	// A result and an assistant AskUserQuestion block cause no implicit close or
	// fabricated tool reply: a second user frame is still accepted by the peer.
	send(t, c, "second")
	if receive(t, c).Type != "system" {
		t.Fatal("second turn missing")
	}
}

func TestFragmentedUnicodeAndLargeFrame(t *testing.T) {
	c := openFixture(t, "fragmented")
	send(t, c, "go")
	event := receive(t, c)
	want := "start " + strings.Repeat("雪 ", 14000) + " end\nnext"
	if event.User == nil || event.User.Content.Text == nil || *event.User.Content.Text != want {
		t.Fatal("chunked content changed")
	}
}

func TestStartupFailuresAreBounded(t *testing.T) {
	for _, mode := range []string{"init_hang", "init_wrong", "init_error", "init_disconnect"} {
		t.Run(mode, func(t *testing.T) {
			opts := fixtureOptions(t, mode)
			opts.InitializeTimeout = 100 * time.Millisecond
			start := time.Now()
			c, err := claude.Open(context.Background(), opts)
			if c != nil || err == nil {
				t.Fatalf("Open = %v, %v", c, err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("startup cleanup was not bounded")
			}
			if mode == "init_hang" || mode == "init_wrong" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout lost: %v", err)
				}
			}
			if mode == "init_error" {
				var control *claude.ControlError
				if !errors.As(err, &control) || control.Message != "fixture refused initialization" {
					t.Fatalf("control error lost: %v", err)
				}
			}
		})
	}
}

func TestMalformedFramesRetainAcceptedEvents(t *testing.T) {
	for _, mode := range []string{"malformed", "truncated", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, mode, func(o *claude.Options) { o.Limits.FrameBytes = 1024 })
			send(t, c, "go")
			e := receive(t, c)
			if e.Type != "accepted" {
				t.Fatalf("first = %s", e.Type)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := c.Receive(ctx)
			target := claude.ErrProtocol
			if mode == "oversize" {
				target = claude.ErrResourceLimit
			}
			if !errors.Is(err, target) {
				t.Fatalf("terminal = %v", err)
			}
			awaitDone(t, c)
		})
	}
	c := openFixture(t, "invalid_type")
	send(t, c, "go")
	_, err := c.Receive(context.Background())
	if !errors.Is(err, claude.ErrProtocol) {
		t.Fatalf("invalid type = %v", err)
	}
}

func TestOverflowIsTerminalAndDoneDoesNotNeedConsumer(t *testing.T) {
	c := openFixture(t, "overflow", func(o *claude.Options) { o.Limits.EventBuffer = 2 })
	send(t, c, "go")
	awaitDone(t, c)
	if !errors.Is(c.Err(), claude.ErrResourceLimit) {
		t.Fatalf("Err = %v", c.Err())
	}
	for i := 0; i < 2; i++ {
		event := receive(t, c)
		var v struct{ Seq int }
		json.Unmarshal(event.Raw, &v)
		if v.Seq != i {
			t.Fatalf("got %d want %d", v.Seq, i)
		}
	}
	_, err := c.Receive(context.Background())
	if !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatalf("Receive = %v", err)
	}
}

func TestExitErrorFollowsLastEventWithBoundedDiagnostics(t *testing.T) {
	c := openFixture(t, "exit_error", func(o *claude.Options) { o.Limits.StderrBytes = 32 })
	send(t, c, "go")
	if event := receive(t, c); event.Result == nil || event.Result.Result != "last" {
		t.Fatal("last result lost")
	}
	_, err := c.Receive(context.Background())
	if err == nil {
		t.Fatal("exit error lost")
	}
	var detail *claude.Error
	if !errors.As(err, &detail) || detail.ExitCode == nil || *detail.ExitCode != 17 || len(detail.Stderr) > 32 || !strings.HasSuffix(detail.Stderr, "tail-marker") {
		t.Fatalf("error diagnostics = %#v", detail)
	}
}

func TestUnexpectedEOFAfterAcceptedEvent(t *testing.T) {
	c := openFixture(t, "eof")
	send(t, c, "go")
	receive(t, c)
	_, err := c.Receive(context.Background())
	if !errors.Is(err, claude.ErrUnexpectedEOF) {
		t.Fatalf("EOF = %v", err)
	}
}

func TestMissingPermissionHandlerDeniesAndUnknownControlErrors(t *testing.T) {
	c := openFixture(t, "permissions")
	send(t, c, "go")
	requests := 0
	replies := map[string]map[string]any{}
	for len(replies) < 2 {
		e := receive(t, c)
		if e.Control != nil {
			requests++
			continue
		}
		var echo struct {
			Wire struct{ Response map[string]any }
		}
		if err := json.Unmarshal(e.Raw, &echo); err != nil {
			t.Fatal(err)
		}
		id, _ := echo.Wire.Response["request_id"].(string)
		replies[id] = echo.Wire.Response
	}
	if requests != 2 {
		t.Fatalf("observed requests = %d", requests)
	}
	deny := replies["permission-1"]
	if deny["subtype"] != "success" || deny["response"].(map[string]any)["behavior"] != "deny" {
		t.Fatalf("permission = %#v", deny)
	}
	if replies["unsupported-2"]["subtype"] != "error" || !strings.Contains(replies["unsupported-2"]["error"].(string), "future_control") {
		t.Fatalf("unsupported = %#v", replies)
	}
}

func TestReceiveCancellationDoesNotCancelSession(t *testing.T) {
	c := openFixture(t, "echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive = %v", err)
	}
	select {
	case <-c.Done():
		t.Fatal("receive cancellation closed client")
	default:
	}
	send(t, c, "still usable")
	receive(t, c)
}

func TestSendCancellationBeforeWriteDoesNotClose(t *testing.T) {
	c := openFixture(t, "echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Send(ctx, "must not send"); !errors.Is(err, context.Canceled) || errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("Send = %v", err)
	}
	send(t, c, "only frame")
	e := receive(t, c)
	if !bytes.Contains(e.Raw, []byte("only frame")) {
		t.Fatalf("canceled frame was sent: %s", e.Raw)
	}
}

func TestWriteFailuresAreReturnedDirectly(t *testing.T) {
	for _, mode := range []string{"closed_stdin", "blocked_write"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, mode)
			receive(t, c)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := c.Send(ctx, strings.Repeat("x", 1<<20))
			if err == nil || !errors.Is(err, claude.ErrUnknownOutcome) {
				t.Fatalf("Send = %v", err)
			}
			awaitDone(t, c)
			if mode == "blocked_write" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked Send = %v", err)
			}
		})
	}
}

func TestConcurrentSendPreservesFrames(t *testing.T) {
	c := openFixture(t, "echo")
	var wg sync.WaitGroup
	const count = 24
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Send(context.Background(), fmt.Sprintf("message-%d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got := map[string]bool{}
	for range count {
		var echo struct {
			Wire struct{ Message struct{ Content string } }
		}
		json.Unmarshal(receive(t, c).Raw, &echo)
		got[echo.Wire.Message.Content] = true
	}
	if len(got) != count {
		t.Fatalf("got %d unique frames", len(got))
	}
}

func TestConcurrentCloseAndReceiveWake(t *testing.T) {
	c := openFixture(t, "echo")
	result := make(chan error, 1)
	go func() { _, err := c.Receive(context.Background()); result <- err }()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("Receive after Close = %v", err)
	}
	if err := c.Send(context.Background(), "after"); !errors.Is(err, claude.ErrClosed) {
		t.Fatalf("Send after Close = %v", err)
	}
	awaitDone(t, c)
}

func TestCloseRetainsBufferedEvents(t *testing.T) {
	c := openFixture(t, "echo")
	send(t, c, "retained")
	// The fixture's EOF handling writes any in-flight echo before exiting; Close
	// drains stdout before it returns instead of exec.Wait dropping pipe bytes.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if e := receive(t, c); !bytes.Contains(e.Raw, []byte("retained")) {
		t.Fatalf("lost echo: %s", e.Raw)
	}
	if _, err := c.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Receive = %v", err)
	}
}

func TestLocalOversizeRejectedWithoutWriting(t *testing.T) {
	c := openFixture(t, "echo", func(o *claude.Options) { o.Limits.FrameBytes = 512 })
	if err := c.Send(context.Background(), strings.Repeat("x", 1024)); !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatalf("Send = %v", err)
	}
	send(t, c, "small")
	if !bytes.Contains(receive(t, c).Raw, []byte("small")) {
		t.Fatal("connection unusable after local rejection")
	}
}

func TestStderrIsDrainedDuringInitialize(t *testing.T) {
	c := openFixture(t, "init_stderr", func(o *claude.Options) { o.Limits.StderrBytes = 16 })
	send(t, c, "ready")
	receive(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOversizeFrameFailsBeforeNewline(t *testing.T) {
	c := openFixture(t, "oversize_without_newline", func(o *claude.Options) { o.Limits.FrameBytes = 1024 })
	send(t, c, "go")
	awaitDone(t, c)
	if !errors.Is(c.Err(), claude.ErrResourceLimit) {
		t.Fatalf("Err = %v", c.Err())
	}
}

func TestInvalidUTF8IsProtocolError(t *testing.T) {
	c := openFixture(t, "invalid_utf8")
	send(t, c, "go")
	_, err := c.Receive(context.Background())
	if !errors.Is(err, claude.ErrProtocol) {
		t.Fatalf("Receive = %v", err)
	}
}

func TestCloseAfterDoneRetainsFinalProtocolError(t *testing.T) {
	c := openFixture(t, "truncated")
	send(t, c, "go")
	awaitDone(t, c)
	if err := c.Close(); !errors.Is(err, claude.ErrProtocol) {
		t.Fatalf("Close = %v", err)
	}
	if e := receive(t, c); e.Type != "accepted" {
		t.Fatal("accepted event lost")
	}
	_, err := c.Receive(context.Background())
	if !errors.Is(err, claude.ErrProtocol) {
		t.Fatalf("Receive = %v", err)
	}
}
