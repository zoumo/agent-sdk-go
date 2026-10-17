package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/protocol"
)

const initResult = `{"userAgent":"fake/only","codexHome":"/synthetic-home","platformFamily":"unix","platformOs":"linux","future":null}`
const threadJSON = `{"id":"native-thread-42","sessionId":"native-session-7","cliVersion":"0.160.1","createdAt":9007199254740993,"updatedAt":2,"cwd":"/synthetic","ephemeral":true,"modelProvider":"mock","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[],"future":{"large":9007199254740993}}`
const startResult = `{"thread":` + threadJSON + `,"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"fake","modelProvider":"mock","sandbox":{"type":"readOnly"}}`

type fakeEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// TestFakeChild is an independently authored app-server peer. It never executes
// Codex or calls a model. Each parent selects its exact mode and temporary files.
func TestFakeChild(t *testing.T) {
	mode := os.Getenv("CODEX_SDK_HELPER")
	if mode == "" {
		return
	}
	if mode == "hold-pipe" {
		time.Sleep(time.Second)
		os.Exit(0)
	}
	_ = os.WriteFile(os.Getenv("CODEX_SDK_PID"), []byte(fmt.Sprint(os.Getpid())), 0600)
	transcript, err := os.Create(os.Getenv("CODEX_SDK_TRACE"))
	if err != nil {
		os.Exit(80)
	}
	defer transcript.Close()
	cwd, _ := os.Getwd()
	processData, _ := json.Marshal(map[string]any{"args": os.Args[1:], "cwd": cwd, "literal": os.Getenv("CODEX_SDK_LITERAL"), "inherited": os.Getenv("CODEX_SDK_PARENT_ONLY")})
	_ = os.WriteFile(os.Getenv("CODEX_SDK_TRACE")+".process", processData, 0600)
	if mode == "exit-before-init" {
		os.Exit(7)
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 32<<20)
	send := func(id json.RawMessage, result string) {
		fmt.Fprintf(os.Stdout, "{\"id\":%s,\"result\":%s}\n", id, result)
	}
	var lateID json.RawMessage
	turns := new(turnPeer)
	threads := new(resumePeer)
	requests := &requestPeer{transcript: transcript}
	for scan.Scan() {
		raw := append([]byte(nil), scan.Bytes()...)
		_, _ = transcript.Write(append(raw, '\n'))
		_ = transcript.Sync()
		var m fakeEnvelope
		if json.Unmarshal(raw, &m) != nil {
			os.Exit(81)
		}
		if requests.handle(mode, m, send) || threads.handle(mode, m, send) || turns.handle(mode, m, send) {
			continue
		}
		switch m.Method {
		case "initialize":
			if mode == "init-fail" {
				fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":-32600,\"message\":\"no\",\"data\":null}}\n", m.ID)
				continue
			}
			if mode == "bad-init" {
				send(m.ID, `{"userAgent":"incomplete"}`)
				continue
			}
			send(m.ID, initResult)
		case "initialized":
			if mode == "prefix-blocked" {
				buf := make([]byte, 1)
				if _, err := os.Stdin.Read(buf); err != nil {
					os.Exit(85)
				}
				_ = os.WriteFile(os.Getenv("CODEX_SDK_TRACE")+".dispatch", []byte("received"), 0600)
				time.Sleep(time.Hour)
				os.Exit(86)
			}
			if mode == "blocked" || mode == "ignore-eof" {
				time.Sleep(time.Hour)
				os.Exit(82)
			}
			if mode == "exit-open-pipe" {
				child := exec.Command(os.Args[0], "-test.run=^TestFakeChild$")
				child.Env = append(os.Environ(), "CODEX_SDK_HELPER=hold-pipe")
				child.Stdout = os.Stdout
				child.Stderr = os.Stderr
				if child.Start() != nil {
					os.Exit(83)
				}
				os.Exit(8)
			}
		case "thread/start":
			send(m.ID, startResult)
		case "thread/read":
			send(m.ID, `{"thread":`+threadJSON+`}`)
		case "thread/resume":
			var params protocol.ThreadResumeParams
			if json.Unmarshal(m.Params, &params) != nil {
				os.Exit(87)
			}
			send(m.ID, resumeResponseJSON(params.ThreadID))
		case "thread/unsubscribe":
			send(m.ID, `{"status":"unsubscribed"}`)
		case "echo":
			send(m.ID, string(m.Params))
		case "rpc-error":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":-32001,\"message\":\"failed after work\",\"data\":{\"large\":9007199254740993}}}\n", m.ID)
		case "collision":
			fmt.Fprintf(os.Stdout, "{\"id\":%q,\"result\":\"wrong\"}\n", string(m.ID))
			fmt.Fprintln(os.Stdout, `{"id":9007199254740993,"result":"unmatched-large"}`)
			send(m.ID, `"right"`)
		case "defer":
			lateID = append([]byte(nil), m.ID...)
		case "after-late":
			send(lateID, `"stale"`)
			send(m.ID, `"fresh"`)
		case "exit":
			os.Exit(9)
		case "invalid-id":
			fmt.Fprintln(os.Stdout, `{"id":1.5,"result":"invalid"}`)
		case "null-id":
			fmt.Fprintln(os.Stdout, `{"id":null,"result":null}`)
		case "duplicate-id":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"id\":%s,\"result\":null}\n", m.ID, m.ID)
		case "partial":
			fmt.Fprint(os.Stdout, `{"id":`)
			os.Exit(9)
		case "array":
			fmt.Fprintln(os.Stdout, `[]`)
		case "trailing":
			fmt.Fprintln(os.Stdout, `{} {}`)
		case "blank":
			fmt.Fprintln(os.Stdout)
		case "both":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"result\":null,\"error\":{\"code\":0,\"message\":\"bad\"}}\n", m.ID)
		case "bad-error":
			fmt.Fprintf(os.Stdout, "{\"id\":%s,\"error\":{\"code\":null,\"message\":\"bad\"}}\n", m.ID)
		case "oversized":
			fmt.Fprintln(os.Stdout, strings.Repeat("x", 10000))
		case "reverse":
			fmt.Fprintln(os.Stdout, `{"id":"server:1","method":"unsupported/server","params":{}}`)
		case "stderr":
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("secret-bearer-value\n"), 100000))
			send(m.ID, `{}`)
		case "notification":
			fmt.Fprintln(os.Stdout, `{"method":"future/unknown","params":{"x":null}}`)
			send(m.ID, `{}`)
		case "wait": // Parent cancellation or Close releases this call locally.
		default:
			os.Exit(84)
		}
	}
	os.Exit(0)
}

type fixture struct {
	c          *codex.Client
	trace, pid string
}

func startFake(t *testing.T, mode string, limits codex.Limits) fixture {
	t.Helper()
	f, err := openFake(t, mode, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.c.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func openFake(t *testing.T, mode string, limits codex.Limits) (fixture, error) {
	t.Helper()
	if remoteVariant(t) != "" {
		return openRemoteFake(t, mode, limits)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := fixture{trace: filepath.Join(dir, "trace.jsonl"), pid: filepath.Join(dir, "pid")}
	env := append(os.Environ(), "CODEX_SDK_HELPER="+mode, "CODEX_SDK_TRACE="+f.trace, "CODEX_SDK_PID="+f.pid)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f.c, err = codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"-test.run=^TestFakeChild$"}, Env: env, Initialize: protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "contract-test", Version: "1"}}, Limits: limits})
	return f, err
}
func trace(t *testing.T, path string) []fakeEnvelope {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []fakeEnvelope
	scan := bufio.NewScanner(bytes.NewReader(b))
	scan.Buffer(make([]byte, 4096), 32<<20)
	for scan.Scan() {
		var m fakeEnvelope
		if err = json.Unmarshal(scan.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}
func waitTrace(t *testing.T, path, method string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		for _, m := range trace(t, path) {
			if m.Method == method {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("did not receive %s", method)
		case <-tick.C:
		}
	}
}
func callError(t *testing.T, err error, state codex.DispatchState, cause error) {
	t.Helper()
	var ce *codex.CallError
	if !errors.As(err, &ce) || ce.Dispatch != state {
		t.Fatalf("got %v, want CallError %s", err, state)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("got %v, want cause %v", err, cause)
	}
}

func TestOwnedStdioLifecycle(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	if f.c.ServerInfo().UserAgent != "fake/only" {
		t.Fatal("server info")
	}
	r, err := f.c.StartThread(context.Background(), protocol.ThreadStartParams{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Thread.ID != "native-thread-42" || r.Thread.CreatedAt != 9007199254740993 {
		t.Fatalf("native data changed: %+v", r.Thread)
	}
	got, err := f.c.ReadThread(context.Background(), protocol.ThreadReadParams{ThreadID: r.Thread.ID, IncludeTurns: protocol.Some(true)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Thread.ID != r.Thread.ID {
		t.Fatal("thread ID changed")
	}
	if !got.Thread.ProjectID.IsNull() {
		t.Fatal("required null was lost")
	}
	if string(got.Thread.UnknownFields["future"]) != `{"large":9007199254740993}` {
		t.Fatal("unknown field changed")
	}
	if err = f.c.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.c.Close(); err != nil {
		t.Fatal(err)
	}
	frames := trace(t, f.trace)
	want := []string{"initialize", "initialized", "thread/start", "thread/read"}
	if len(frames) != len(want) {
		t.Fatalf("unexpected calls: %+v", frames)
	}
	for i, m := range frames {
		if m.Method != want[i] {
			t.Fatalf("order %v", frames)
		}
	}
	b, _ := os.ReadFile(f.trace)
	if bytes.Contains(b, []byte(`"jsonrpc"`)) {
		t.Fatal("wrong JSON-RPC dialect")
	}
	var read map[string]any
	_ = json.Unmarshal(frames[3].Params, &read)
	if read["threadId"] != "native-thread-42" || read["includeTurns"] != true {
		t.Fatal(string(frames[3].Params))
	}
	assertReaped(t, f.pid)
}
func assertReaped(t *testing.T, pidPath string) {
	t.Helper()
	// Remote fixtures own their own peers; the SDK must not reap them.
	if pidPath == "" {
		return
	}
	if runtime.GOOS == "windows" {
		return
	}
	b, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	_, _ = fmt.Sscan(string(b), &pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err = p.Signal(syscall.Signal(0)); err == nil {
		t.Fatalf("owned child %d still exists", pid)
	}
}
func TestIDExactness(t *testing.T) {
	for _, wire := range []string{`1`, `"1"`, `9007199254740993`, `9223372036854775807`, `-9223372036854775808`, `"雪/with\\escape"`, `""`} {
		id, err := codex.ParseID([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(id)
		if err != nil || string(got) != wire {
			t.Fatalf("ID round trip %s -> %s: %v", wire, got, err)
		}
	}
	invalid := codex.StringID(string([]byte{0xff}))
	if invalid.Valid() {
		t.Fatal("invalid UTF-8 string ID considered valid")
	}
	if _, err := json.Marshal(invalid); !errors.Is(err, codex.ErrProtocol) {
		t.Fatal("invalid UTF-8 ID silently changed")
	}
	if codex.IntegerID(1) == codex.StringID("1") {
		t.Fatal("ID collision")
	}
	for _, wire := range []string{`null`, `1.0`, `1e0`, `true`, `{}`, `[]`, `9223372036854775808`, `01`, `"\ud800"`, `"\udc00"`, string([]byte{34, 255, 34}), ``} {
		if _, err := codex.ParseID([]byte(wire)); err == nil {
			t.Fatalf("accepted invalid ID %q", wire)
		}
	}
	f := startFake(t, "normal", codex.Limits{})
	var got string
	if err := f.c.Call(context.Background(), "collision", nil, &got); err != nil || got != "right" {
		t.Fatalf("collision %q %v", got, err)
	}
	if f.c.Diagnostics().UnmatchedResponses != 2 {
		t.Fatal(f.c.Diagnostics())
	}
}
func TestCancellationAndLateResponse(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	callError(t, f.c.Call(canceled, "not-sent", nil, nil), codex.NotSent, context.Canceled)
	ctx, cancel := context.WithCancel(context.Background())
	errch := make(chan error, 1)
	go func() { errch <- f.c.Call(ctx, "defer", nil, nil) }()
	waitTrace(t, f.trace, "defer")
	// A second response proves the serialized writer settled the first frame.
	responseBarrier(t, f)
	cancel()
	callError(t, <-errch, codex.OutcomeUnknown, context.Canceled)
	var got string
	if err := f.c.Call(context.Background(), "after-late", nil, &got); err != nil || got != "fresh" {
		t.Fatalf("late %q %v", got, err)
	}
	if f.c.Diagnostics().UnmatchedResponses != 1 {
		t.Fatal(f.c.Diagnostics())
	}
	for _, m := range trace(t, f.trace) {
		if m.Method == "not-sent" {
			t.Fatal("canceled request sent")
		}
	}
}
func TestRPCErrorNoReplay(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	err := f.c.Call(context.Background(), "rpc-error", nil, nil)
	callError(t, err, codex.Responded, nil)
	var re *codex.RPCError
	if !errors.As(err, &re) || re.Code != -32001 || string(re.Data) != `{"large":9007199254740993}` || len(re.Raw) == 0 {
		t.Fatal(err)
	}
	_ = f.c.Close()
	n := 0
	for _, m := range trace(t, f.trace) {
		if m.Method == "rpc-error" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("replayed %d times", n)
	}
}
func TestConnectionLossIsUnknown(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	err := f.c.Call(context.Background(), "exit", nil, nil)
	callError(t, err, codex.OutcomeUnknown, io.EOF)
	_ = f.c.Close()
	assertReaped(t, f.pid)
}
func TestMalformedFrames(t *testing.T) {
	for _, method := range []string{"invalid-id", "null-id", "duplicate-id", "partial", "array", "trailing", "blank", "both", "bad-error", "oversized"} {
		t.Run(method, func(t *testing.T) {
			f := startFake(t, "normal", codex.Limits{MaxFrameBytes: 4096})
			err := f.c.Call(context.Background(), method, nil, nil)
			cause := codex.ErrProtocol
			callError(t, err, codex.OutcomeUnknown, cause)
			if method == "oversized" && !errors.Is(err, codex.ErrFrameTooLarge) {
				t.Fatal(err)
			}
		})
	}
}
func TestInitializeFailures(t *testing.T) {
	for _, mode := range []string{"init-fail", "bad-init", "exit-before-init"} {
		t.Run(mode, func(t *testing.T) {
			f, err := openFake(t, mode, codex.Limits{ShutdownTimeout: 200 * time.Millisecond})
			if err == nil {
				_ = f.c.Close()
				t.Fatal("initialize unexpectedly succeeded")
			}
			if f.c != nil {
				t.Fatal("failed constructor exposed client")
			}
			assertReaped(t, f.pid)
		})
	}
}
func TestPendingAdmissionLimit(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{PendingCalls: 1})
	errch := make(chan error, 1)
	go func() { errch <- f.c.Call(context.Background(), "wait", nil, nil) }()
	waitTrace(t, f.trace, "wait")
	callError(t, f.c.Call(context.Background(), "not-sent", nil, nil), codex.NotSent, codex.ErrLimit)
	_ = f.c.Close()
	callError(t, <-errch, codex.OutcomeUnknown, codex.ErrClosed)
}
func TestStderrAndUnknownNotifications(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{DiagnosticBytes: 33})
	if err := f.c.Call(context.Background(), "stderr", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.c.Call(context.Background(), "notification", nil, nil); err != nil {
		t.Fatal(err)
	}
	d := f.c.Diagnostics()
	if d.StderrBytes == 0 || len(d.Tail) > 33 || strings.Contains(d.Tail, "secret") || d.Notifications != 1 {
		t.Fatalf("unsafe diagnostics %+v", d)
	}
}
func TestBlockedWriteAndConcurrentClose(t *testing.T) {
	f := startFake(t, "blocked", codex.Limits{WriteTimeout: 80 * time.Millisecond, ShutdownTimeout: 200 * time.Millisecond})
	err := f.c.Call(context.Background(), "echo", strings.Repeat("x", 2<<20), nil)
	callError(t, err, codex.OutcomeUnknown, codex.ErrWriteTimeout)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := f.c.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	assertReaped(t, f.pid)
}
func TestCloseKillsOnlyOwnedChild(t *testing.T) {
	f := startFake(t, "ignore-eof", codex.Limits{ShutdownTimeout: 200 * time.Millisecond})
	start := time.Now()
	_ = f.c.Close()
	if time.Since(start) > time.Second {
		t.Fatal("Close exceeded bound")
	}
	assertReaped(t, f.pid)
}
func TestOwnedChildExitWithOpenOutput(t *testing.T) {
	f, err := openFake(t, "exit-open-pipe", codex.Limits{ShutdownTimeout: 400 * time.Millisecond})
	if err == nil {
		defer f.c.Close()
		select {
		case <-f.c.Done():
		case <-time.After(time.Second):
			t.Fatal("retained child pipe masked exit")
		}
	}
	assertReaped(t, f.pid)
}
func TestOutgoingFrameLimitAndDefaultCallTimeout(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{MaxFrameBytes: 4096, CallTimeout: 50 * time.Millisecond})
	callError(t, f.c.Call(context.Background(), "echo", strings.Repeat("x", 8192), nil), codex.NotSent, codex.ErrFrameTooLarge)
	callError(t, f.c.Call(context.Background(), "wait", nil, nil), codex.OutcomeUnknown, context.DeadlineExceeded)
}

func TestOutboundAdmissionIsBounded(t *testing.T) {
	f := startFake(t, "blocked", codex.Limits{OutboundQueue: 1, PendingCalls: 128, WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 20)
	start := make(chan struct{})
	for range 20 {
		go func() { <-start; errs <- f.c.Call(ctx, "echo", strings.Repeat("x", 1<<20), nil) }()
	}
	close(start)
	// With one active writer and one queue slot, at least 18 calls must be
	// rejected before writing. No consumer or peer timing is used as evidence.
	for range 18 {
		select {
		case err := <-errs:
			callError(t, err, codex.NotSent, codex.ErrLimit)
		case <-time.After(2 * time.Second):
			t.Fatal("unbounded admission")
		}
	}
	cancel()
	_ = f.c.Close()
	for range 2 {
		select {
		case <-errs:
		case <-time.After(time.Second):
			t.Fatal("pending writer survived close")
		}
	}
	assertReaped(t, f.pid)
}

func TestCancelBlockedWrite(t *testing.T) {
	f := startFake(t, "prefix-blocked", codex.Limits{WriteTimeout: 5 * time.Second, ShutdownTimeout: 200 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- f.c.Call(ctx, "echo", strings.Repeat("x", 2<<20), nil) }()
	// The peer acknowledges an actual request byte, rather than assuming a timer
	// implies dispatch. It then refuses to drain the rest of the frame.
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(f.trace + ".dispatch"); err == nil {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("peer never received request byte")
		case <-tick.C:
		}
	}
	cancel()
	callError(t, <-result, codex.OutcomeUnknown, context.Canceled)
	select {
	case <-f.c.Done():
	case <-time.After(time.Second):
		t.Fatal("canceled write retained connection")
	}
	_ = f.c.Close()
	assertReaped(t, f.pid)
}

func TestInvalidOptionsAndClosedCall(t *testing.T) {
	if _, err := codex.NewStdio(context.Background(), codex.StdioOptions{}); err == nil {
		t.Fatal("empty executable accepted")
	}
	if _, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: "unused", Limits: codex.Limits{OutboundQueue: -1}}); err == nil {
		t.Fatal("negative limit accepted")
	}
	f := startFake(t, "normal", codex.Limits{})
	_ = f.c.Close()
	callError(t, f.c.Call(context.Background(), "echo", nil, nil), codex.NotSent, codex.ErrClosed)
}

func TestExplicitCommandEnvironmentAndDirectory(t *testing.T) {
	t.Setenv("CODEX_SDK_PARENT_ONLY", "must-not-leak")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace")
	pidPath := filepath.Join(dir, "pid")
	args := []string{"-test.run=^TestFakeChild$", "--", "spaces and ; $(not-a-shell)"}
	c, err := codex.NewStdio(context.Background(), codex.StdioOptions{Executable: exe, Args: args, Dir: dir, Env: []string{"CODEX_SDK_HELPER=normal", "CODEX_SDK_TRACE=" + tracePath, "CODEX_SDK_PID=" + pidPath, "CODEX_SDK_LITERAL=literal-$HOME"}, Initialize: protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "explicit", Version: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := os.ReadFile(tracePath + ".process")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Args                    []string
		Cwd, Literal, Inherited string
	}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(args)
	actual, _ := json.Marshal(got.Args)
	if !bytes.Equal(expected, actual) || got.Cwd != dir || got.Literal != "literal-$HOME" || got.Inherited != "" {
		t.Fatalf("command was transformed: %s", raw)
	}
}
