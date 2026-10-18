// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bufio"
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

// These original public-boundary scenarios use the official Python SDK's
// test_transport.py (literal resume argv), test_streaming_client.py (interrupt
// control shape), test_query.py (pending controls fail on reader loss), and
// test_session_resume.py (isolated configuration and fresh resume setup) as
// behavioral references. No CLI history, account, or model is exercised.
func runSessionFixture(mode string, reader *bufio.Reader) {
	var transcript *os.File
	if path := os.Getenv("CLAUDE_GO_TRANSCRIPT"); path != "" {
		var err error
		transcript, err = os.Create(path)
		if err != nil {
			os.Exit(9)
		}
		defer transcript.Close()
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if transcript != nil {
			if _, err := transcript.Write(line); err != nil {
				os.Exit(10)
			}
		}
		var wire struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Message   struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &wire) != nil {
			os.Exit(11)
		}
		if wire.Type == "control_request" {
			fixtureEmit(map[string]any{"type": "control_seen", "wire": json.RawMessage(line)})
			if mode == "session_immediate" {
				sessionReply(wire.RequestID, "")
			}
			continue
		}
		if mode == "session_resume" {
			fixtureEmit(map[string]any{"type": "echo", "wire": json.RawMessage(line)})
			fixtureEmit(map[string]any{"type": "system", "subtype": "init", "session_id": os.Getenv("CLAUDE_GO_SESSION_ID"), "future": "preserved"})
			continue
		}
		command, value, _ := strings.Cut(wire.Message.Content, ":")
		switch command {
		case "ack":
			sessionReply(value, "")
			fixtureEmit(map[string]any{"type": "barrier"})
		case "error":
			sessionReply(value, "fixture rejected interrupt")
			fixtureEmit(map[string]any{"type": "barrier"})
		case "result":
			fixtureEmit(map[string]any{"type": "result", "subtype": "success", "session_id": value, "result": "turn ended", "terminal_reason": "aborted_streaming", "future": "kept"})
		case "system":
			fixtureEmit(map[string]any{"type": "system", "subtype": "init", "session_id": value, "future": "kept"})
		case "unknown":
			fixtureEmit(map[string]any{"type": "future_event", "session_id": value})
		case "assistant":
			fixtureEmit(map[string]any{"type": "assistant", "session_id": value, "message": map[string]any{"role": "assistant", "content": "hello"}})
		case "disconnect":
			return
		case "malformed", "malformed_exit":
			fmt.Fprintln(os.Stdout, `{"type":`)
			// Keep the process alive so this scenario deterministically tests
			// decoding failure. malformed_exit also races exit against decode.
			if command == "malformed" {
				io.Copy(io.Discard, reader)
			}
			return
		case "close_stdin":
			os.Stdin.Close()
			fixtureEmit(map[string]any{"type": "ready"})
			time.Sleep(time.Hour)
			return
		default:
			fixtureEmit(map[string]any{"type": "echo", "wire": json.RawMessage(line)})
		}
	}
}

func sessionReply(id, message string) {
	response := map[string]any{"subtype": "success", "request_id": id, "response": map[string]any{}}
	if message != "" {
		response["subtype"], response["error"] = "error", message
		delete(response, "response")
	}
	fixtureEmit(map[string]any{"type": "control_response", "response": response})
}

func interruptAsync(c *claude.Client, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() { result <- c.Interrupt(ctx) }()
	return result
}

func awaitInterrupt(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("Interrupt did not return")
		return nil
	}
}

func receiveInterrupt(t *testing.T, c *claude.Client) string {
	t.Helper()
	event := receive(t, c)
	if event.Type != "control_seen" {
		t.Fatalf("expected control_seen, got %s: %s", event.Type, event.Raw)
	}
	var observed struct {
		Wire map[string]json.RawMessage `json:"wire"`
	}
	if err := json.Unmarshal(event.Raw, &observed); err != nil {
		t.Fatal(err)
	}
	var id, typ string
	var request map[string]any
	if err := json.Unmarshal(observed.Wire["request_id"], &id); err != nil || id == "" {
		t.Fatalf("missing request ID: %s", event.Raw)
	}
	if err := json.Unmarshal(observed.Wire["type"], &typ); err != nil || typ != "control_request" {
		t.Fatalf("incorrect control type: %s", event.Raw)
	}
	if err := json.Unmarshal(observed.Wire["request"], &request); err != nil || !reflect.DeepEqual(request, map[string]any{"subtype": "interrupt"}) || len(observed.Wire) != 3 {
		t.Fatalf("incorrect interrupt envelope: %s", event.Raw)
	}
	return id
}

func sessionCommand(t *testing.T, c *claude.Client, command, wantType string) claude.Event {
	t.Helper()
	send(t, c, command)
	event := receive(t, c)
	if event.Type != wantType {
		t.Fatalf("%s: got %s, want %s: %s", command, event.Type, wantType, event.Raw)
	}
	return event
}

func assertSessionID(t *testing.T, c *claude.Client, want string, known bool) {
	t.Helper()
	if got, ok := c.SessionID(); got != want || ok != known {
		t.Fatalf("SessionID = (%q, %t), want (%q, %t)", got, ok, want, known)
	}
}

func TestSessionIdentityDistinguishesRequestAndObservation(t *testing.T) {
	c := openFixture(t, "session_controls", func(opts *claude.Options) { opts.ResumeID = "requested-native" })
	if c.RequestedResumeID() != "requested-native" {
		t.Fatal("requested resume ID was changed")
	}
	assertSessionID(t, c, "", false)
	sessionCommand(t, c, "unknown:untrusted-future", "future_event")
	sessionCommand(t, c, "assistant:untrusted-assistant", "assistant")
	assertSessionID(t, c, "", false)
	for _, kind := range []string{"system", "result"} {
		event := sessionCommand(t, c, kind+":observed-"+kind, kind)
		assertSessionID(t, c, "observed-"+kind, true)
		if event.SessionID != "observed-"+kind || !strings.Contains(string(event.Raw), `"future":"kept"`) {
			t.Fatalf("native identity or raw data was lost: %s", event.Raw)
		}
		if kind == "system" && event.System.SessionID != event.SessionID || kind == "result" && event.Result.SessionID != event.SessionID {
			t.Fatal("typed native session ID was lost")
		}
		sessionCommand(t, c, kind+":", kind)
		assertSessionID(t, c, "observed-"+kind, true)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	assertSessionID(t, c, "observed-result", true)
}

func TestSessionIdentityObservedBeforeReceiveAndWithoutSystemInit(t *testing.T) {
	c := openFixture(t, "session_identity_at_initialize")
	// The system frame precedes initialize's acknowledgment. Identity does not
	// depend on the application having called Receive yet.
	assertSessionID(t, c, "native-at-initialize", true)
	if event := receive(t, c); event.System == nil || event.System.SessionID != "native-at-initialize" {
		t.Fatalf("initial system event was consumed internally: %s", event.Raw)
	}
	resultOnly := openFixture(t, "session_controls")
	assertSessionID(t, resultOnly, "", false)
	sessionCommand(t, resultOnly, "result:native-result-only", "result")
	assertSessionID(t, resultOnly, "native-result-only", true)
}

func TestResumeUsesFreshProcessLiteralArgAndNoReplay(t *testing.T) {
	firstOptions := fixtureOptions(t, "session_resume")
	firstOptions.Env = append(firstOptions.Env, "CLAUDE_GO_SESSION_ID=native-before")
	first, err := claude.Open(context.Background(), firstOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if first.RequestedResumeID() != "" {
		t.Fatal("fresh session has requested identity")
	}
	assertSessionID(t, first, "", false)
	send(t, first, "old prompt that must never replay")
	_ = receive(t, first) // Echo of this explicit Send.
	_ = receive(t, first) // Native system frame.
	id, ok := first.SessionID()
	if !ok || id != "native-before" {
		t.Fatal("first native ID was not captured")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Each Open is an independent subprocess. Only an explicit Send may appear
	// on the second process's stdin after initialize.
	for _, requested := range []string{id, "--evil option; $(not-a-shell) 雪"} {
		t.Run(requested, func(t *testing.T) {
			opts := fixtureOptions(t, "session_resume")
			capture := filepath.Join(opts.WorkDir, "arguments.json")
			transcript := filepath.Join(opts.WorkDir, "stdin.ndjson")
			opts.ResumeID = requested
			opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture, "CLAUDE_GO_TRANSCRIPT="+transcript, "CLAUDE_GO_SESSION_ID=native-after")
			resumed, err := claude.Open(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = resumed.Close() })
			assertSessionID(t, resumed, "", false)
			if resumed.RequestedResumeID() != requested {
				t.Fatal("resume request was changed")
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var recorded struct{ Argv []string }
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, arg := range recorded.Argv {
				if arg == "--resume="+requested {
					count++
				}
				if arg == "--resume" || arg == requested {
					t.Fatalf("resume value became separate argv: %#v", recorded.Argv)
				}
			}
			if count != 1 {
				t.Fatalf("literal resume option count = %d: %#v", count, recorded.Argv)
			}
			send(t, resumed, "new prompt")
			_ = receive(t, resumed)
			_ = receive(t, resumed)
			assertSessionID(t, resumed, "native-after", true)
			if err := resumed.Close(); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			var messages []map[string]any
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				var wire map[string]any
				if err := json.Unmarshal(scanner.Bytes(), &wire); err != nil {
					t.Fatal(err)
				}
				messages = append(messages, wire)
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0]["type"] != "user" || messages[0]["message"].(map[string]any)["content"] != "new prompt" {
				t.Fatalf("unexpected replay or extra stdin writes: %s", data)
			}
		})
	}
}

func TestInterruptCorrelatesOutOfOrderAndUnknownResponses(t *testing.T) {
	c := openFixture(t, "session_controls")
	first := interruptAsync(c, context.Background())
	firstID := receiveInterrupt(t, c)
	second := interruptAsync(c, context.Background())
	secondID := receiveInterrupt(t, c)
	if firstID == secondID {
		t.Fatal("request IDs were reused")
	}
	sessionCommand(t, c, "error:unknown-id", "barrier")
	sessionCommand(t, c, "error:"+secondID, "barrier")
	var controlErr *claude.ControlError
	if err := awaitInterrupt(t, second); !errors.As(err, &controlErr) || controlErr.RequestID != secondID || controlErr.Subtype != "interrupt" || controlErr.Message != "fixture rejected interrupt" {
		t.Fatalf("wrong second response: %v", err)
	}
	sessionCommand(t, c, "error:"+secondID, "barrier") // Duplicate/stale response.
	sessionCommand(t, c, "ack:"+firstID, "barrier")
	if err := awaitInterrupt(t, first); err != nil {
		t.Fatalf("first request received an unrelated error: %v", err)
	}
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
}

func TestInterruptAcknowledgmentIsSeparateFromTurnResult(t *testing.T) {
	c := openFixture(t, "session_controls")
	pending := interruptAsync(c, context.Background())
	id := receiveInterrupt(t, c)
	result := sessionCommand(t, c, "result:native-turn", "result")
	if result.Result == nil || result.Result.Result != "turn ended" || result.Result.TerminalReason != "aborted_streaming" {
		t.Fatal("turn result was not delivered")
	}
	// A result alone cannot complete this request. Its matching error must win.
	sessionCommand(t, c, "error:"+id, "barrier")
	var controlErr *claude.ControlError
	if err := awaitInterrupt(t, pending); !errors.As(err, &controlErr) {
		t.Fatalf("turn result resolved the interrupt: %v", err)
	}
	pending = interruptAsync(c, context.Background())
	id = receiveInterrupt(t, c)
	sessionCommand(t, c, "ack:"+id, "barrier")
	if err := awaitInterrupt(t, pending); err != nil {
		t.Fatal(err)
	}
	// The acknowledged interrupt does not close stdin or discard later results.
	sessionCommand(t, c, "result:later-turn", "result")
	assertSessionID(t, c, "later-turn", true)
}

func TestInterruptImmediateAcknowledgments(t *testing.T) {
	c := openFixture(t, "session_immediate")
	for range 32 {
		pending := interruptAsync(c, context.Background())
		receiveInterrupt(t, c)
		if err := awaitInterrupt(t, pending); err != nil {
			t.Fatalf("immediate response missed its waiter: %v", err)
		}
	}
}

func TestInterruptCancellationAndStaleResponse(t *testing.T) {
	c := openFixture(t, "session_controls", func(opts *claude.Options) { opts.Limits.PendingControls = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	first := interruptAsync(c, ctx)
	id := receiveInterrupt(t, c)
	// A later complete Send crosses the writer gate, so cancellation below is
	// known to occur during the response wait rather than the original OS write.
	sessionCommand(t, c, "echo", "echo")
	cancel()
	if err := awaitInterrupt(t, first); !errors.Is(err, context.Canceled) || !errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("canceled written request must have unknown outcome: %v", err)
	}
	second := interruptAsync(c, context.Background())
	secondID := receiveInterrupt(t, c) // Also proves cancellation freed capacity.
	sessionCommand(t, c, "error:"+id, "barrier")
	sessionCommand(t, c, "ack:"+secondID, "barrier")
	if err := awaitInterrupt(t, second); err != nil {
		t.Fatalf("stale response affected a new control: %v", err)
	}
	if err := c.Err(); err != nil {
		t.Fatalf("canceling the response wait terminated the connection: %v", err)
	}
}

func TestInterruptTimeoutAndPreCanceledContext(t *testing.T) {
	c := openFixture(t, "session_controls", func(opts *claude.Options) { opts.Limits.PendingControls = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Interrupt(ctx); !errors.Is(err, context.Canceled) || errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("pre-canceled interrupt: %v", err)
	}
	// A deterministic stdin barrier proves a canceled call emitted no control.
	sessionCommand(t, c, "echo", "echo")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pending := interruptAsync(c, ctx)
	id := receiveInterrupt(t, c)
	if err := awaitInterrupt(t, pending); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("timed-out written interrupt: %v", err)
	}
	sessionCommand(t, c, "ack:"+id, "barrier")
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
}

func TestInterruptPendingLimitRejectsWithoutWriting(t *testing.T) {
	c := openFixture(t, "session_controls", func(opts *claude.Options) { opts.Limits.PendingControls = 1 })
	pending := interruptAsync(c, context.Background())
	id := receiveInterrupt(t, c)
	if err := c.Interrupt(context.Background()); !errors.Is(err, claude.ErrResourceLimit) || errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("limit rejection: %v", err)
	}
	sessionCommand(t, c, "echo", "echo")
	sessionCommand(t, c, "ack:"+id, "barrier")
	if err := awaitInterrupt(t, pending); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptWaitersWakeOnTermination(t *testing.T) {
	for _, mode := range []string{"close", "disconnect", "malformed", "malformed_exit"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, "session_controls")
			var pending []<-chan error
			for range 3 {
				pending = append(pending, interruptAsync(c, context.Background()))
				receiveInterrupt(t, c)
			}
			want := claude.ErrClosed
			switch mode {
			case "close":
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				want = claude.ErrUnexpectedEOF
				send(t, c, mode)
			case "malformed", "malformed_exit":
				want = claude.ErrProtocol
				send(t, c, mode)
			}
			for _, result := range pending {
				err := awaitInterrupt(t, result)
				matches := errors.Is(err, want)
				if mode == "malformed_exit" {
					// Process termination can wake waiters before the final stdout
					// decode adds the more specific error. Cleanup must retain it.
					matches = matches || errors.Is(err, claude.ErrUnexpectedEOF)
				}
				if !matches || !errors.Is(err, claude.ErrUnknownOutcome) {
					t.Fatalf("pending interrupt on %s: %v", mode, err)
				}
			}
			awaitDone(t, c)
			if mode != "close" {
				if err := c.Close(); !errors.Is(err, want) || !errors.Is(c.Err(), want) {
					t.Fatalf("final diagnostic lost after %s: Close = %v, Err = %v", mode, err, c.Err())
				}
			}
			if err := c.Interrupt(context.Background()); !errors.Is(err, claude.ErrClosed) || errors.Is(err, claude.ErrUnknownOutcome) {
				t.Fatalf("interrupt after termination: %v", err)
			}
		})
	}
}

func TestInterruptWriteFailureWakesOtherWaiters(t *testing.T) {
	c := openFixture(t, "session_controls")
	pending := interruptAsync(c, context.Background())
	receiveInterrupt(t, c)
	sessionCommand(t, c, "close_stdin", "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Interrupt(ctx); err == nil || !errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("closed stdin interrupt: %v", err)
	}
	if err := awaitInterrupt(t, pending); err == nil || !errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("other waiter did not observe write failure: %v", err)
	}
	awaitDone(t, c)
}

func TestInterruptResponseCancellationAndCloseRaces(t *testing.T) {
	for range 20 {
		c := openFixture(t, "session_controls")
		ctx, cancel := context.WithCancel(context.Background())
		pending := interruptAsync(c, ctx)
		id := receiveInterrupt(t, c)
		sessionCommand(t, c, "echo", "echo")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); <-start; _ = c.Send(context.Background(), "ack:"+id) }()
		go func() { defer wg.Done(); <-start; cancel() }()
		go func() { defer wg.Done(); <-start; _ = c.Close() }()
		close(start)
		err := awaitInterrupt(t, pending)
		if err != nil && !errors.Is(err, claude.ErrUnknownOutcome) {
			t.Fatalf("unexpected race outcome: %v", err)
		}
		wg.Wait()
		if err := c.Interrupt(context.Background()); !errors.Is(err, claude.ErrClosed) && !errors.Is(err, claude.ErrUnknownOutcome) {
			t.Fatalf("closed client accepted a new interrupt: %v", err)
		}
		for {
			_, err := nextNative(c, context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				// Close can overlap the fixture-release Send's OS write. That
				// separate write failure remains observable after draining.
				if !errors.Is(err, claude.ErrUnknownOutcome) {
					t.Fatal(err)
				}
				break
			}
		}
	}
}

func TestSessionIdentityConcurrentReaders(t *testing.T) {
	c := openFixture(t, "session_controls")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	defer func() { close(stop); wg.Wait() }()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c.SessionID()
					c.RequestedResumeID()
				}
			}
		}()
	}
	for i := range 20 {
		sessionCommand(t, c, fmt.Sprintf("system:id-%d", i), "system")
	}
	_ = c.Close()
	assertSessionID(t, c, "id-19", true)
}
