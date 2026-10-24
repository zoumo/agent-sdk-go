// SPDX-License-Identifier: Apache-2.0

package claude

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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

// All fixtures are local re-executions of this test binary under a replacement
// environment and temporary HOME. No installed CLI or account is ever used.
func TestMain(m *testing.M) {
	if os.Getenv(transcript.PeerEnvironment) == "1" {
		if err := transcript.Peer(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		os.Exit(0)
	}
	if mode := os.Getenv("CLAUDE_GO_FIXTURE"); mode != "" {
		runFixture(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureEmit(value any) {
	value = fixtureNativeFrame(value)
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		os.Exit(4)
	}
}
func runFixture(mode string) {
	if mode == "lifecycle_descendant" {
		runLifecycleDescendant()
		return
	}
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
	case "session_load_malformed":
		fmt.Fprintln(os.Stdout, `{ "type":"control_response","response":{"subtype":"success","request_id":"\ud800"}} `)
		io.Copy(io.Discard, reader)
		return
	case "session_load_pending":
		runLoadPendingFixture(reader, init["request_id"])
		return
	case "session_load_metadata", "session_load_foreign_metadata", "session_load_conversation":
		f, e := os.OpenFile(os.Getenv("CLAUDE_GO_HISTORY_FILE"), os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(12)
		}
		for _, kind := range []string{"atis-latch", "mode"} {
			id := os.Getenv("CLAUDE_GO_SESSION_ID")
			if mode == "session_load_foreign_metadata" {
				id = "foreign-session"
			}
			if mode == "session_load_conversation" {
				kind = "user"
			}
			b, _ := json.Marshal(map[string]any{"type": kind, "sessionId": id})
			if _, e = f.Write(append(b, '\n')); e != nil {
				os.Exit(13)
			}
		}
		if f.Close() != nil {
			os.Exit(14)
		}
	case "session_load_bad_id", "session_load_bad_id_empty_parent":
		frame := map[string]any{"type": "system", "subtype": "init", "session_id": "wrong-native-id"}
		if mode == "session_load_bad_id_empty_parent" {
			frame["parent_tool_use_id"] = ""
		}
		fixtureEmit(frame)
	case "tree_early":
		request, _ := init["request"].(map[string]any)
		if request["forwardSubagentText"] != true {
			os.Exit(14)
		}
		fmt.Fprintln(os.Stdout, earlyTreeFrame)
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
		fixtureEmit(map[string]any{"type": "unexpected-before-initialize"})
		fmt.Fprint(os.Stderr, "initialize rejected")
		fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": init["request_id"], "error": "fixture refused initialization"}})
		io.Copy(io.Discard, reader)
		return
	case "init_wrong":
		fixtureEmit(map[string]any{"type": "unexpected-before-initialize"})
		fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "not-the-request", "response": map[string]any{}}})
		io.Copy(io.Discard, reader)
		return
	}
	fixtureEmit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": init["request_id"], "response": map[string]any{"commands": []any{}}}})
	if mode == "tree" {
		runTreeFixture(reader)
		return
	}
	if strings.HasPrefix(mode, "overload_") {
		runOverloadFixture(mode, reader)
		return
	}
	if strings.HasPrefix(mode, "lifecycle_") {
		runLifecycleFixture(mode, reader)
		return
	}
	if strings.HasPrefix(mode, "permission_") {
		runPermissionFixture(mode, reader)
		return
	}
	if strings.HasPrefix(mode, "session_") {
		runSessionFixture(mode, reader)
		return
	}
	if mode == "input_config" {
		runInputConfigFixture(reader)
		return
	}
	if mode == "blocked_write" || mode == "closed_stdin" {
		if mode == "closed_stdin" {
			os.Stdin.Close()
		}
		fixtureEmit(map[string]any{"type": "ready"})
		if mode == "blocked_write" {
			prefix := make([]byte, 64)
			if _, err := io.ReadFull(reader, prefix); err != nil {
				os.Exit(15)
			}
			fixtureEmit(map[string]any{"type": "writer_prefix", "prefix": string(prefix)})
		}
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
		if message["type"] == "control_request" {
			id, _ := message["request_id"].(string)
			sessionReply(id, "")
			continue
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
			fmt.Fprintln(os.Stdout, `{"type":"assistant","session_id":"s","message":{"content":[]},"fixture_kind":"accepted","seq":1}`)
			fmt.Fprintln(os.Stdout, `{"type":`)
			return
		case "truncated":
			fmt.Fprintln(os.Stdout, `{"type":"assistant","session_id":"s","message":{"content":[]},"fixture_kind":"accepted","seq":1}`)
			fmt.Fprint(os.Stdout, `{"type":"future"}`)
			return
		case "invalid_utf8":
			os.Stdout.Write([]byte{'{', '"', 't', 'y', 'p', 'e', '"', ':', '"', 0xff, '"', '}', '\n'})
			return
		case "invalid_type":
			fmt.Fprintln(os.Stdout, `{"type":42}`)
			return
		case "oversize":
			fmt.Fprintln(os.Stdout, `{"type":"assistant","session_id":"s","message":{"content":[]},"fixture_kind":"accepted","seq":1}`)
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
			fmt.Fprintln(os.Stdout, `{"type":"result","session_id":"s","subtype":"success","result":"last"}`)
			fmt.Fprint(os.Stderr, strings.Repeat("x", 128)+"tail-marker")
			os.Exit(17)
		case "eof":
			fmt.Fprintln(os.Stdout, `{"type":"assistant","session_id":"s","message":{"content":[]},"fixture_kind":"accepted"}`)
			return
		case "permissions":
			fixtureEmit(map[string]any{"type": "control_request", "request_id": "permission-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{"path": "test"}, "tool_use_id": "t1"}})
			fixtureEmit(map[string]any{"type": "control_request", "request_id": "unsupported-2", "request": map[string]any{"subtype": "future_control"}})
		case "fragmented":
			value := "start " + strings.Repeat("雪 ", 14000) + " end\nnext"
			data, _ := json.Marshal(map[string]any{"type": "user", "session_id": "s", "message": map[string]any{"role": "user", "content": value}})
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

func fixtureOptions(t *testing.T, mode string) Options {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	return Options{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "CLAUDE_GO_FIXTURE=" + mode, "GORACE=atexit_sleep_ms=0"}, InitializeTimeout: 3 * time.Second, ShutdownTimeout: 600 * time.Millisecond}
}
func openFixture(t *testing.T, mode string, configure ...func(*Options)) *interaction {
	t.Helper()
	opts := fixtureOptions(t, mode)
	for _, fn := range configure {
		fn(&opts)
	}
	c, err := openTestInteraction(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	return c
}
func receive(t *testing.T, c *interaction) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	event, err := nextNative(c, ctx)
	require.NoError(t, err)
	return event
}
func send(t *testing.T, c *interaction, text string) {
	t.Helper()
	{
		_, err := c.session.Submit(context.Background(), Input{Text: text})
		require.NoError(t, err)
	}
}
func awaitDone(t *testing.T, c *interaction) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		require.FailNow(t, "verification failed", "Done did not close")
	}
}

func (s *clientProtocolSuite) TestOpenUsesLiteralArgumentsAndMatchedInitialize() {
	t := s.T()
	t.Run("default temp directory", func(t *testing.T) {
		testOpenUsesLiteralArgumentsAndMatchedInitialize(t, fixtureOptions(t, "echo"))
	})
	t.Run("symlink work directory", func(t *testing.T) {
		opts := fixtureOptions(t, "echo")
		alias := filepath.Join(t.TempDir(), "workdir")
		{
			err := os.Symlink(opts.WorkDir, alias)
			require.NoError(t, err)
		}
		opts.WorkDir = alias
		opts.Env[0] = "HOME=" + alias
		testOpenUsesLiteralArgumentsAndMatchedInitialize(t, opts)
	})
}

func testOpenUsesLiteralArgumentsAndMatchedInitialize(t *testing.T, opts Options) {
	t.Helper()
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	opts.Model = "--odd model; $(not-a-shell)"
	c, err := openTestInteraction(context.Background(), opts)
	require.NoError(t, err)
	defer c.Abort()
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var captured struct {
		Argv      []string
		Init      map[string]json.RawMessage
		CWD, Home string
	}
	{
		err := json.Unmarshal(data, &captured)
		require.NoError(t, err)
	}
	want := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages", "--model=" + opts.Model}
	require.Truef(t, reflect.DeepEqual(captured.Argv, want), "argv = %#v", captured.Argv)
	// Getwd may resolve symlink aliases (including macOS /var -> /private/var).
	// Require the requested directory's identity, while keeping HOME literal.
	cwdInfo, err := os.Stat(captured.CWD)
	require.NoError(t, err)
	workDirInfo, err := os.Stat(opts.WorkDir)
	require.NoError(t, err)
	require.Falsef(t, !cwdInfo.IsDir() || !workDirInfo.IsDir() || !os.SameFile(cwdInfo, workDirInfo), "cwd does not identify requested WorkDir: %s", data)
	require.EqualValuesf(t, opts.WorkDir, captured.Home, "HOME not preserved literally: %s", data)
	require.Falsef(t, string(captured.Init["type"]) != `"control_request"` || len(captured.Init["request_id"]) < 3, "initialize envelope: %s", data)
	var request map[string]any
	json.Unmarshal(captured.Init["request"], &request)
	require.Falsef(t, request["subtype"] != "initialize" || request["hooks"] != nil || request["forwardSubagentText"] != true, "initialize = %#v", request)
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
	{
		err := json.Unmarshal(e.Raw, &echo)
		require.NoError(t, err)
	}
	require.Falsef(t, echo.Wire.Type != "user" || echo.Wire.Message.Role != "user" || echo.Wire.Message.Content != text || string(echo.Wire.Parent) != "null", "send frame: %s", e.Raw)
}

func (s *clientProtocolSuite) TestTypedViewsAndLosslessUnknowns() {
	t := s.T()
	c := s.client
	send(t, c, "first")
	events := make([]Event, 5)
	for i := range events {
		events[i] = receive(t, c)
	}
	want := []string{"system", "assistant", "user", "stream_event", "result"}
	for i, event := range events {
		require.EqualValuesf(t, want[i], eventKind(event), "event %d: %s", i, eventKind(event))
	}
	require.Falsef(t, events[0].System.SessionID != "native-123" || !bytes.HasPrefix(events[0].Raw, []byte(" {")) || !bytes.Contains(events[0].Raw, []byte(`"new_field"`)), "system/raw: %s", events[0].Raw)
	a := events[1].Assistant
	require.Falsef(t, a.Content.Blocks[0].Text.Text != "hello\nworld" || a.Content.Blocks[1].Thinking.Signature != "sig" || a.Content.Blocks[2].ToolUse.Name != "AskUserQuestion", "assistant = %#v", a)
	unknown := a.Content.Blocks[3]
	require.Falsef(t, unknown.Type != "future_block" || string(unknown.Raw) != `{"type":"future_block","extra":[1,2]}`, "lost block: %s", unknown.Raw)
	require.True(t, bytes.Contains(a.Raw, []byte(`"new_nested":true`)), "nested data lost")
	toolResult := events[2].User.Content.Blocks[0].ToolResult
	require.Falsef(t, toolResult.ToolUseID != "t1" || string(toolResult.Content) != `[{"type":"text","text":"answer"}]`, "tool result = %#v", toolResult)
	require.False(t, events[3].Stream.Delta.Type != "content_block_delta" || *events[3].Stream.Delta.Index != 0, "missing delta view")
	require.False(t, events[4].Result.Result != "done" || events[4].Result.NumTurns != 1, "missing result view")
	require.Eventually(t, func() bool { return len(c.session.Unattributed().Records) == 1 }, 3*time.Second, time.Millisecond)
	unknownEvent := c.session.Unattributed().Records[0].Event
	require.Equal(t, `{"type":"future_event","nested":{"x":[true,1,"raw"]}}`, string(unknownEvent.Raw))
	parsed, err := ParseEvent(unknownEvent.Raw)
	require.NoError(t, err)
	require.Equal(t, "future_event", parsed.Type)
	// A result and an assistant AskUserQuestion block cause no implicit close or
	// fabricated tool reply: a second user frame is still accepted by the peer.
	send(t, c, "second")
	require.EqualValues(t, "system", eventKind(receive(t, c)), "second turn missing")
}

func (s *clientProtocolSuite) TestFragmentedUnicodeAndLargeFrame() {
	t := s.T()
	c := s.client
	send(t, c, "go")
	event := receive(t, c)
	want := "start " + strings.Repeat("雪 ", 14000) + " end\nnext"
	require.False(t, event.User == nil || event.User.Content.Text == nil || *event.User.Content.Text != want, "chunked content changed")
}

// Per-case startup failure deadlines keep their original native table harness.
func TestStartupFailuresAreBounded(t *testing.T) {
	for _, mode := range []string{"init_hang", "init_wrong", "init_error", "init_disconnect"} {
		t.Run(mode, func(t *testing.T) {
			opts := fixtureOptions(t, mode)
			opts.InitializeTimeout = 100 * time.Millisecond
			start := time.Now()
			c, err := openTestInteraction(context.Background(), opts)
			require.Falsef(t, c != nil || err == nil, "Open = %v, %v", c, err)
			require.False(t, time.Since(start) > 3*time.Second, "startup cleanup was not bounded")
			if mode == "init_hang" || mode == "init_wrong" {
				require.ErrorIsf(t, err, context.DeadlineExceeded, "timeout lost: %v", err)
			}
			if mode == "init_error" {
				var control *ControlError
				require.Falsef(t, !errors.As(err, &control) || control.Message != "fixture refused initialization", "control error lost: %v", err)
			}
		})
	}
}

func (s *clientProtocolSuite) TestMalformedFramesRetainAcceptedEvents() {
	t := s.T()
	for _, mode := range []string{"malformed", "truncated", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, mode, func(o *Options) { o.Limits.FrameBytes = 1024 })
			send(t, c, "go")
			e := receive(t, c)
			require.EqualValuesf(t, "accepted", eventKind(e), "first = %s", eventKind(e))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := nextNative(c, ctx)
			target := ErrProtocol
			if mode == "oversize" {
				target = ErrResourceLimit
			}
			require.ErrorIsf(t, err, target, "terminal = %v", err)
			awaitDone(t, c)
		})
	}
	c := openFixture(t, "invalid_type")
	send(t, c, "go")
	_, err := nextNative(c, context.Background())
	require.ErrorIsf(t, err, ErrProtocol, "invalid type = %v", err)
}

func (s *clientProtocolSuite) TestSlowConsumerDrainsDynamicBacklogAfterClose() {
	t := s.T()
	c := openFixture(t, "overflow", func(o *Options) { o.Limits.EventBuffer = 2 })
	send(t, c, "go")
	require.NoError(t, c.Interrupt(context.Background()))
	// Explicit close waits for the reader, never for Receive to drain the burst.
	require.NoError(t, c.Close())
	awaitDone(t, c)
	require.NoError(t, c.Err())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.session.Receive(canceled)
	require.ErrorIs(t, err, context.Canceled)
	for i := 0; i < 20; i++ {
		event := receive(t, c)
		var v struct{ Seq int }
		require.NoError(t, json.Unmarshal(event.Raw, &v))
		require.EqualValues(t, i, v.Seq)
	}
	_, err = nextNative(c, context.Background())
	require.ErrorIs(t, err, io.EOF)
}

func (s *clientProtocolSuite) TestExitErrorFollowsLastEventWithBoundedDiagnostics() {
	t := s.T()
	c := openFixture(t, "exit_error", func(o *Options) { o.Limits.StderrBytes = 32 })
	send(t, c, "go")
	{
		event := receive(t, c)
		require.False(t, event.Result == nil || event.Result.Result != "last", "last result lost")
	}
	_, err := nextNative(c, context.Background())
	require.Error(t, err, "exit error lost")
	err = c.session.Close()
	var detail *Error
	require.Falsef(t, !errors.As(err, &detail) || detail.ExitCode == nil || *detail.ExitCode != 17 || len(detail.Stderr) > 32 || !strings.HasSuffix(detail.Stderr, "tail-marker"), "error diagnostics = %#v", detail)
}

func (s *clientProtocolSuite) TestUnexpectedEOFAfterAcceptedEvent() {
	t := s.T()
	c := s.client
	send(t, c, "go")
	receive(t, c)
	_, err := nextNative(c, context.Background())
	require.ErrorIsf(t, err, ErrUnexpectedEOF, "EOF = %v", err)
}

func (s *clientProtocolSuite) TestPermissionWaitsAndUnknownControlErrors() {
	t := s.T()
	c := s.client
	send(t, c, "go")
	requests := 0
	var permission *PermissionRequest
	replies := map[string]map[string]any{}
	for len(replies) < 2 {
		var e Event
		if permission == nil {
			delivery := receivePermission(t, c)
			e, permission = delivery.Event, delivery.Permission
			require.NoError(t, permission.Context().Err())
			require.NoError(t, permission.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny}))
		} else {
			e = receive(t, c)
		}
		if e.Control != nil {
			requests++
			continue
		}
		var echo struct {
			Wire struct{ Response map[string]any }
		}
		{
			err := json.Unmarshal(e.Raw, &echo)
			require.NoError(t, err)
		}
		id, _ := echo.Wire.Response["request_id"].(string)
		replies[id] = echo.Wire.Response
	}
	require.EqualValuesf(t, 2, requests, "observed requests = %d", requests)
	deny := replies["permission-1"]
	require.Falsef(t, deny["subtype"] != "success" || deny["response"].(map[string]any)["behavior"] != "deny", "permission = %#v", deny)
	require.Falsef(t, replies["unsupported-2"]["subtype"] != "error" || !strings.Contains(replies["unsupported-2"]["error"].(string), "future_control"), "unsupported = %#v", replies)
}

func (s *clientProtocolSuite) TestReceiveCancellationDoesNotCancelSession() {
	t := s.T()
	c := s.client
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		_, err := nextNative(c, ctx)
		require.ErrorIsf(t, err, context.Canceled, "Receive = %v", err)
	}
	select {
	case <-c.Done():
		require.FailNow(t, "verification failed", "receive cancellation closed client")
	default:
	}
	send(t, c, "still usable")
	receive(t, c)
}

func (s *clientProtocolSuite) TestSendCancellationBeforeWriteDoesNotClose() {
	t := s.T()
	c := s.client
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := c.Send(ctx, "must not send")
		require.Falsef(t, !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnknownOutcome), "Send = %v", err)
	}
	send(t, c, "only frame")
	e := receive(t, c)
	require.Truef(t, bytes.Contains(e.Raw, []byte("only frame")), "canceled frame was sent: %s", e.Raw)
}

// Keep the tight write-deadline matrix native: suite dispatch adds no value to
// this per-case process setup and the original timing boundaries stay intact.
func TestWriteFailuresAreReturnedDirectly(t *testing.T) {
	for _, mode := range []string{"closed_stdin", "blocked_write"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, mode)
			receive(t, c)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := c.session.Submit(ctx, Input{Text: strings.Repeat("x", 1<<20)}); result <- err }()
			if mode == "blocked_write" {
				prefixCtx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				prefix, err := c.session.Receive(prefixCtx)
				require.NoError(t, err)
				require.NotNil(t, prefix.Event)
				require.Equal(t, "writer_prefix", eventKind(*prefix.Event))
			}
			err := <-result
			var dispatch *DispatchError
			require.ErrorAs(t, err, &dispatch)
			require.Equal(t, OutcomeUnknown, dispatch.State)
			require.Falsef(t, err == nil || !errors.Is(err, ErrUnknownOutcome), "Send = %v", err)
			awaitDone(t, c)
			require.Falsef(t, mode == "blocked_write" && !errors.Is(err, context.DeadlineExceeded), "blocked Send = %v", err)
		})
	}
}

func TestConcurrentSendPreservesFrames(t *testing.T) {
	c := openFixture(t, "echo")
	var wg sync.WaitGroup
	const count = 24
	workerErrors := make(chan error, count)
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerErrors <- c.Send(context.Background(), fmt.Sprintf("message-%d", i))
		}()
	}
	wg.Wait()
	close(workerErrors)
	for err := range workerErrors {
		require.NoError(t, err)
	}
	got := map[string]bool{}
	for range count {
		var echo struct {
			Wire struct{ Message struct{ Content string } }
		}
		require.NoError(t, json.Unmarshal(receive(t, c).Raw, &echo))
		got[echo.Wire.Message.Content] = true
	}
	require.EqualValuesf(t, count, len(got), "got %d unique frames", len(got))
}

func TestConcurrentCloseAndReceiveWake(t *testing.T) {
	c := openFixture(t, "echo")
	result := make(chan error, 1)
	go func() { _, err := nextNative(c, context.Background()); result <- err }()
	var wg sync.WaitGroup
	workerErrors := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerErrors <- c.Close()
		}()
	}
	wg.Wait()
	close(workerErrors)
	for err := range workerErrors {
		require.NoError(t, err)
	}
	{
		err := <-result
		require.ErrorIsf(t, err, io.EOF, "Receive after Close = %v", err)
	}
	{
		err := c.Send(context.Background(), "after")
		require.ErrorIsf(t, err, ErrClosed, "Send after Close = %v", err)
	}
	awaitDone(t, c)
}

func (s *clientProtocolSuite) TestCloseRetainsBufferedEvents() {
	t := s.T()
	c := s.client
	send(t, c, "retained")
	require.NoError(t, c.Interrupt(context.Background()))
	// The fixture's EOF handling writes any in-flight echo before exiting; Close
	// drains stdout before it returns instead of exec.Wait dropping pipe bytes.
	{
		err := c.Close()
		require.NoError(t, err)
	}
	{
		e := receive(t, c)
		require.Truef(t, bytes.Contains(e.Raw, []byte("retained")), "lost echo: %s", e.Raw)
	}
	{
		_, err := nextNative(c, context.Background())
		require.ErrorIsf(t, err, io.EOF, "Receive = %v", err)
	}
}

func (s *clientProtocolSuite) TestLocalOversizeRejectedWithoutWriting() {
	t := s.T()
	c := openFixture(t, "echo", func(o *Options) { o.Limits.FrameBytes = 512 })
	{
		err := c.Send(context.Background(), strings.Repeat("x", 1024))
		require.ErrorIsf(t, err, ErrResourceLimit, "Send = %v", err)
	}
	send(t, c, "small")
	require.True(t, bytes.Contains(receive(t, c).Raw, []byte("small")), "connection unusable after local rejection")
}

func (s *clientProtocolSuite) TestStderrIsDrainedDuringInitialize() {
	t := s.T()
	c := openFixture(t, "init_stderr", func(o *Options) { o.Limits.StderrBytes = 16 })
	send(t, c, "ready")
	receive(t, c)
	{
		err := c.Close()
		require.NoError(t, err)
	}
}

func (s *clientProtocolSuite) TestOversizeFrameFailsBeforeNewline() {
	t := s.T()
	c := openFixture(t, "oversize_without_newline", func(o *Options) { o.Limits.FrameBytes = 1024 })
	send(t, c, "go")
	awaitDone(t, c)
	require.ErrorIsf(t, c.Err(), ErrResourceLimit, "Err = %v", c.Err())
}

func (s *clientProtocolSuite) TestInvalidUTF8IsProtocolError() {
	t := s.T()
	c := s.client
	send(t, c, "go")
	_, err := nextNative(c, context.Background())
	require.ErrorIsf(t, err, ErrProtocol, "Receive = %v", err)
}

func (s *clientProtocolSuite) TestCloseAfterDoneRetainsFinalProtocolError() {
	t := s.T()
	c := s.client
	send(t, c, "go")
	awaitDone(t, c)
	{
		err := c.Close()
		require.ErrorIsf(t, err, ErrProtocol, "Close = %v", err)
	}
	{
		e := receive(t, c)
		require.EqualValues(t, "accepted", eventKind(e), "accepted event lost")
	}
	_, err := nextNative(c, context.Background())
	require.ErrorIsf(t, err, ErrProtocol, "Receive = %v", err)
}

// nextNative is deliberately test-only: native contract tests skip SDK records.
func nextNative(c *interaction, ctx context.Context) (Event, error) {
	for {
		r, err := c.session.Receive(ctx)
		if err != nil {
			return Event{}, err
		}
		if r.Event != nil {
			return *r.Event, nil
		}
		if r.Request != nil {
			return ParseEvent(r.Raw)
		}
	}
}

// Failed Open returns no client to drain or abort; accepted startup output must
// therefore be discarded before return, allowing the ChannX manager to exit.
func TestStartupFailureReleasesUnconsumedNativeQueue(t *testing.T) {
	managers := func() int {
		stacks := make([]byte, 1<<20)
		n := runtime.Stack(stacks, true)
		return bytes.Count(stacks[:n], []byte("github.com/zoumo/golib/channx.(*ChannX).process("))
	}
	before := managers()
	for range 3 {
		opts := fixtureOptions(t, "init_error")
		// This peer deterministically emits an event before its correlated error.
		// Use the usual startup budget: the assertion concerns teardown, not timing.
		opts.InitializeTimeout = 3 * time.Second
		c, err := openTestInteraction(context.Background(), opts)
		require.Nil(t, c)
		var control *ControlError
		require.ErrorAs(t, err, &control)
		require.Equal(t, "fixture refused initialization", control.Message)
	}
	require.Eventually(t, func() bool { return managers() <= before }, time.Second, 10*time.Millisecond,
		"failed initialization retained an undrainable ChannX manager")
}

// fixtureNativeFrame gives synthetic transport markers a reviewed native envelope.
// Tests inspect the actual wire Raw; fixture_kind is not SDK routing evidence.
func fixtureNativeFrame(value any) any {
	if os.Getenv("CLAUDE_GO_FIXTURE") == "tree" {
		return value
	}
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	kind, _ := m["type"].(string)
	switch kind {
	case "echo", "reply_seen", "snapshot", "ready", "sequence", "rejected", "barrier", "control_seen", "writer_ready", "writer_prefix", "writer_finished", "accepted-before-cancel", "uncooperative_ready", "tree_ready":
		m["fixture_kind"] = kind
		m["type"] = "assistant"
		if _, ok := m["session_id"]; !ok {
			m["session_id"] = "s"
		}
		m["message"] = map[string]any{"content": []any{}}
	}
	return m
}

func eventKind(e Event) string {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(e.Raw, &fields)
	if raw, ok := fields["fixture_kind"]; ok {
		var kind string
		_ = json.Unmarshal(raw, &kind)
		return kind
	}
	return e.Type
}

func openTestInteraction(ctx context.Context, o Options) (*interaction, error) {
	s, err := Open(ctx, SessionOptions(o))
	if err != nil {
		return nil, err
	}
	return s.client, nil
}
