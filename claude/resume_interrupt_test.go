// SPDX-License-Identifier: Apache-2.0

package claude

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

	"github.com/stretchr/testify/require"
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
		if mode == "session_resume" || mode == "session_load" {
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

func interruptAsync(c *Client, ctx context.Context) <-chan error {
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
		require.FailNow(t, "verification failed", "Interrupt did not return")
		return nil
	}
}

func receiveInterrupt(t *testing.T, c *Client) string {
	t.Helper()
	event := receive(t, c)
	require.EqualValuesf(t, "control_seen", event.Type, "expected control_seen, got %s: %s", event.Type, event.Raw)
	var observed struct {
		Wire map[string]json.RawMessage `json:"wire"`
	}
	{
		err := json.Unmarshal(event.Raw, &observed)
		require.NoError(t, err)
	}
	var id, typ string
	var request map[string]any
	{
		err := json.Unmarshal(observed.Wire["request_id"], &id)
		require.Falsef(t, err != nil || id == "", "missing request ID: %s", event.Raw)
	}
	{
		err := json.Unmarshal(observed.Wire["type"], &typ)
		require.Falsef(t, err != nil || typ != "control_request", "incorrect control type: %s", event.Raw)
	}
	{
		err := json.Unmarshal(observed.Wire["request"], &request)
		require.Falsef(t, err != nil || !reflect.DeepEqual(request, map[string]any{"subtype": "interrupt"}) || len(observed.Wire) != 3, "incorrect interrupt envelope: %s", event.Raw)
	}
	return id
}

func sessionCommand(t *testing.T, c *Client, command, wantType string) Event {
	t.Helper()
	send(t, c, command)
	event := receive(t, c)
	require.EqualValuesf(t, wantType, event.Type, "%s: got %s, want %s: %s", command, event.Type, wantType, event.Raw)
	return event
}

func assertSessionID(t *testing.T, c *Client, want string, known bool) {
	t.Helper()
	{
		got, ok := c.SessionID()
		require.Falsef(t, got != want || ok != known, "SessionID = (%q, %t), want (%q, %t)", got, ok, want, known)
	}
}

func (s *sessionControlSuite) TestSessionIdentityDistinguishesRequestAndObservation() {
	t := s.T()
	c := openFixture(t, "session_controls", func(opts *Options) { opts.ResumeID = "requested-native" })
	require.EqualValues(t, "requested-native", c.RequestedResumeID(), "requested resume ID was changed")
	assertSessionID(t, c, "", false)
	sessionCommand(t, c, "unknown:untrusted-future", "future_event")
	sessionCommand(t, c, "assistant:untrusted-assistant", "assistant")
	assertSessionID(t, c, "", false)
	for _, kind := range []string{"system", "result"} {
		event := sessionCommand(t, c, kind+":observed-"+kind, kind)
		assertSessionID(t, c, "observed-"+kind, true)
		require.Falsef(t, event.SessionID != "observed-"+kind || !strings.Contains(string(event.Raw), `"future":"kept"`), "native identity or raw data was lost: %s", event.Raw)
		require.False(t, kind == "system" && event.System.SessionID != event.SessionID || kind == "result" && event.Result.SessionID != event.SessionID, "typed native session ID was lost")
		sessionCommand(t, c, kind+":", kind)
		assertSessionID(t, c, "observed-"+kind, true)
	}
	{
		err := c.Close()
		require.NoError(t, err)
	}
	assertSessionID(t, c, "observed-result", true)
}

func (s *sessionControlSuite) TestSessionIdentityObservedBeforeReceiveAndWithoutSystemInit() {
	t := s.T()
	c := s.client
	// The system frame precedes initialize's acknowledgment. Identity does not
	// depend on the application having called Receive yet.
	assertSessionID(t, c, "native-at-initialize", true)
	{
		event := receive(t, c)
		require.Falsef(t, event.System == nil || event.System.SessionID != "native-at-initialize", "initial system event was consumed internally: %s", event.Raw)
	}
	resultOnly := openFixture(t, "session_controls")
	assertSessionID(t, resultOnly, "", false)
	sessionCommand(t, resultOnly, "result:native-result-only", "result")
	assertSessionID(t, resultOnly, "native-result-only", true)
}

func (s *sessionControlSuite) TestResumeUsesFreshProcessLiteralArgAndNoReplay() {
	t := s.T()
	firstOptions := fixtureOptions(t, "session_resume")
	firstOptions.Env = append(firstOptions.Env, "CLAUDE_GO_SESSION_ID=native-before")
	first, err := NewClient(context.Background(), firstOptions)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Abort() })
	require.EqualValues(t, "", first.RequestedResumeID(), "fresh session has requested identity")
	assertSessionID(t, first, "", false)
	send(t, first, "old prompt that must never replay")
	_ = receive(t, first) // Echo of this explicit Send.
	_ = receive(t, first) // Native system frame.
	id, ok := first.SessionID()
	require.False(t, !ok || id != "native-before", "first native ID was not captured")
	{
		err := first.Close()
		require.NoError(t, err)
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
			resumed, err := NewClient(context.Background(), opts)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resumed.Abort() })
			assertSessionID(t, resumed, "", false)
			require.EqualValues(t, requested, resumed.RequestedResumeID(), "resume request was changed")
			data, err := os.ReadFile(capture)
			require.NoError(t, err)
			var recorded struct{ Argv []string }
			{
				err := json.Unmarshal(data, &recorded)
				require.NoError(t, err)
			}
			count := 0
			for _, arg := range recorded.Argv {
				if arg == "--resume="+requested {
					count++
				}
				require.Falsef(t, arg == "--resume" || arg == requested, "resume value became separate argv: %#v", recorded.Argv)
			}
			require.EqualValuesf(t, 1, count, "literal resume option count = %d: %#v", count, recorded.Argv)
			send(t, resumed, "new prompt")
			_ = receive(t, resumed)
			_ = receive(t, resumed)
			assertSessionID(t, resumed, "native-after", true)
			{
				err := resumed.Close()
				require.NoError(t, err)
			}
			data, err = os.ReadFile(transcript)
			require.NoError(t, err)
			var messages []map[string]any
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				var wire map[string]any
				{
					err := json.Unmarshal(scanner.Bytes(), &wire)
					require.NoError(t, err)
				}
				messages = append(messages, wire)
			}
			{
				err := scanner.Err()
				require.NoError(t, err)
			}
			require.Falsef(t, len(messages) != 1 || messages[0]["type"] != "user" || messages[0]["message"].(map[string]any)["content"] != "new prompt", "unexpected replay or extra stdin writes: %s", data)
		})
	}
}

func (s *sessionControlSuite) TestInterruptCorrelatesOutOfOrderAndUnknownResponses() {
	t := s.T()
	c := s.client
	first := interruptAsync(c, context.Background())
	firstID := receiveInterrupt(t, c)
	second := interruptAsync(c, context.Background())
	secondID := receiveInterrupt(t, c)
	require.NotEqualValues(t, secondID, firstID, "request IDs were reused")
	sessionCommand(t, c, "error:unknown-id", "barrier")
	sessionCommand(t, c, "error:"+secondID, "barrier")
	var controlErr *ControlError
	{
		err := awaitInterrupt(t, second)
		require.Falsef(t, !errors.As(err, &controlErr) || controlErr.RequestID != secondID || controlErr.Subtype != "interrupt" || controlErr.Message != "fixture rejected interrupt", "wrong second response: %v", err)
	}
	sessionCommand(t, c, "error:"+secondID, "barrier") // Duplicate/stale response.
	sessionCommand(t, c, "ack:"+firstID, "barrier")
	{
		err := awaitInterrupt(t, first)
		require.Nilf(t, err, "first request received an unrelated error: %v", err)
	}
	require.Nil(t, c.Err(), c.Err())
}

func (s *sessionControlSuite) TestInterruptAcknowledgmentIsSeparateFromTurnResult() {
	t := s.T()
	c := s.client
	pending := interruptAsync(c, context.Background())
	id := receiveInterrupt(t, c)
	result := sessionCommand(t, c, "result:native-turn", "result")
	require.False(t, result.Result == nil || result.Result.Result != "turn ended" || result.Result.TerminalReason != "aborted_streaming", "turn result was not delivered")
	// A result alone cannot complete this request. Its matching error must win.
	sessionCommand(t, c, "error:"+id, "barrier")
	var controlErr *ControlError
	{
		err := awaitInterrupt(t, pending)
		require.ErrorAsf(t, err, &controlErr, "turn result resolved the interrupt: %v", err)
	}
	pending = interruptAsync(c, context.Background())
	id = receiveInterrupt(t, c)
	sessionCommand(t, c, "ack:"+id, "barrier")
	{
		err := awaitInterrupt(t, pending)
		require.NoError(t, err)
	}
	// The acknowledged interrupt does not close stdin or discard later results.
	sessionCommand(t, c, "result:later-turn", "result")
	assertSessionID(t, c, "later-turn", true)
}

func (s *sessionControlSuite) TestInterruptImmediateAcknowledgments() {
	t := s.T()
	c := s.client
	for range 32 {
		pending := interruptAsync(c, context.Background())
		receiveInterrupt(t, c)
		{
			err := awaitInterrupt(t, pending)
			require.Nilf(t, err, "immediate response missed its waiter: %v", err)
		}
	}
}

func (s *sessionControlSuite) TestInterruptCancellationAndStaleResponse() {
	t := s.T()
	c := openFixture(t, "session_controls", func(opts *Options) { opts.Limits.PendingControls = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	first := interruptAsync(c, ctx)
	id := receiveInterrupt(t, c)
	// A later complete Send crosses the writer gate, so cancellation below is
	// known to occur during the response wait rather than the original OS write.
	sessionCommand(t, c, "echo", "echo")
	cancel()
	{
		err := awaitInterrupt(t, first)
		require.Falsef(t, !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnknownOutcome), "canceled written request must have unknown outcome: %v", err)
	}
	second := interruptAsync(c, context.Background())
	secondID := receiveInterrupt(t, c) // Also proves cancellation freed capacity.
	sessionCommand(t, c, "error:"+id, "barrier")
	sessionCommand(t, c, "ack:"+secondID, "barrier")
	{
		err := awaitInterrupt(t, second)
		require.Nilf(t, err, "stale response affected a new control: %v", err)
	}
	{
		err := c.Err()
		require.Nilf(t, err, "canceling the response wait terminated the connection: %v", err)
	}
}

func (s *sessionControlSuite) TestInterruptTimeoutAndPreCanceledContext() {
	t := s.T()
	c := openFixture(t, "session_controls", func(opts *Options) { opts.Limits.PendingControls = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := c.Interrupt(ctx)
		require.Falsef(t, !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnknownOutcome), "pre-canceled interrupt: %v", err)
	}
	// A deterministic stdin barrier proves a canceled call emitted no control.
	sessionCommand(t, c, "echo", "echo")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pending := interruptAsync(c, ctx)
	id := receiveInterrupt(t, c)
	{
		err := awaitInterrupt(t, pending)
		require.Falsef(t, !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrUnknownOutcome), "timed-out written interrupt: %v", err)
	}
	sessionCommand(t, c, "ack:"+id, "barrier")
	require.Nil(t, c.Err(), c.Err())
}

func (s *sessionControlSuite) TestInterruptPendingLimitRejectsWithoutWriting() {
	t := s.T()
	c := openFixture(t, "session_controls", func(opts *Options) { opts.Limits.PendingControls = 1 })
	pending := interruptAsync(c, context.Background())
	id := receiveInterrupt(t, c)
	{
		err := c.Interrupt(context.Background())
		require.Falsef(t, !errors.Is(err, ErrResourceLimit) || errors.Is(err, ErrUnknownOutcome), "limit rejection: %v", err)
	}
	sessionCommand(t, c, "echo", "echo")
	sessionCommand(t, c, "ack:"+id, "barrier")
	{
		err := awaitInterrupt(t, pending)
		require.NoError(t, err)
	}
}

func (s *sessionControlSuite) TestInterruptWaitersWakeOnTermination() {
	t := s.T()
	for _, mode := range []string{"close", "disconnect", "malformed", "malformed_exit"} {
		t.Run(mode, func(t *testing.T) {
			c := openFixture(t, "session_controls")
			var pending []<-chan error
			for range 3 {
				pending = append(pending, interruptAsync(c, context.Background()))
				receiveInterrupt(t, c)
			}
			want := ErrClosed
			switch mode {
			case "close":
				{
					err := c.Close()
					require.NoError(t, err)
				}
			case "disconnect":
				want = ErrUnexpectedEOF
				send(t, c, mode)
			case "malformed", "malformed_exit":
				want = ErrProtocol
				send(t, c, mode)
			}
			for _, result := range pending {
				err := awaitInterrupt(t, result)
				matches := errors.Is(err, want)
				if mode == "malformed_exit" {
					// Process termination can wake waiters before the final stdout
					// decode adds the more specific error. Cleanup must retain it.
					matches = matches || errors.Is(err, ErrUnexpectedEOF)
				}
				require.Falsef(t, !matches || !errors.Is(err, ErrUnknownOutcome), "pending interrupt on %s: %v", mode, err)
			}
			awaitDone(t, c)
			if mode != "close" {
				{
					err := c.Close()
					require.Falsef(t, !errors.Is(err, want) || !errors.Is(c.Err(), want), "final diagnostic lost after %s: Close = %v, Err = %v", mode, err, c.Err())
				}
			}
			{
				err := c.Interrupt(context.Background())
				require.Falsef(t, !errors.Is(err, ErrClosed) || errors.Is(err, ErrUnknownOutcome), "interrupt after termination: %v", err)
			}
		})
	}
}

func (s *sessionControlSuite) TestInterruptWriteFailureWakesOtherWaiters() {
	t := s.T()
	c := s.client
	pending := interruptAsync(c, context.Background())
	receiveInterrupt(t, c)
	sessionCommand(t, c, "close_stdin", "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	{
		err := c.Interrupt(ctx)
		require.Falsef(t, err == nil || !errors.Is(err, ErrUnknownOutcome), "closed stdin interrupt: %v", err)
	}
	{
		err := awaitInterrupt(t, pending)
		require.Falsef(t, err == nil || !errors.Is(err, ErrUnknownOutcome), "other waiter did not observe write failure: %v", err)
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
		require.Falsef(t, err != nil && !errors.Is(err, ErrUnknownOutcome), "unexpected race outcome: %v", err)
		wg.Wait()
		{
			err := c.Interrupt(context.Background())
			require.Falsef(t, !errors.Is(err, ErrClosed) && !errors.Is(err, ErrUnknownOutcome), "closed client accepted a new interrupt: %v", err)
		}
		for {
			_, err := nextNative(c, context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				// Close can overlap the fixture-release Send's OS write. That
				// separate write failure remains observable after draining.
				require.ErrorIs(t, err, ErrUnknownOutcome, err)
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
