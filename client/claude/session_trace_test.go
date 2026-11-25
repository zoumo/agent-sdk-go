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
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const promptParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// Synthetic peer evidence covers local control/input ordering only. It does
// not claim that a provider consumes the settings or links any exported span.
func openPromptTracePeer(t *testing.T, env, outcomes []string, failWrite bool) (*Session, <-chan json.RawMessage) {
	t.Helper()
	reader, output := io.Pipe()
	input, writer := io.Pipe()
	frames := make(chan json.RawMessage, 64)
	finished := make(chan error, 1)
	go func() {
		defer output.Close()
		applies := 0
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			raw := append(json.RawMessage(nil), scanner.Bytes()...)
			var frame struct {
				Type    string
				ID      string `json:"request_id"`
				Request struct{ Subtype string }
			}
			if err := json.Unmarshal(raw, &frame); err != nil {
				finished <- err
				return
			}
			if frame.Request.Subtype != "initialize" {
				frames <- raw
			}
			if frame.Type == "user" {
				continue
			}
			response := `{}`
			if frame.Request.Subtype == "get_settings" {
				response = `{"applied":{"ultracode":true}}`
			}
			result := "success"
			if frame.Request.Subtype == "apply_flag_settings" {
				outcome := "success"
				if applies < len(outcomes) {
					outcome = outcomes[applies]
				}
				applies++
				if outcome == "timeout" {
					continue
				}
				if outcome == "reject" {
					result = "error"
				}
			}
			reply := map[string]any{"subtype": result, "request_id": frame.ID}
			if result == "error" {
				reply["error"] = "synthetic rejection"
			} else {
				reply["response"] = json.RawMessage(response)
			}
			if err := json.NewEncoder(output).Encode(map[string]any{"type": "control_response", "response": reply}); err != nil {
				finished <- err
				return
			}
		}
		finished <- scanner.Err()
	}()
	var outbound io.WriteCloser = writer
	if failWrite {
		outbound = &failPromptTraceWriter{PipeWriter: writer}
	}
	var s *Session
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
		_ = output.Close()
		_ = writer.Close()
		select {
		case err := <-finished:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("synthetic trace peer did not stop")
		}
		_ = input.Close()
	})
	var err error
	s, err = OpenStreams(t.Context(), SessionOptions{Env: env}, StreamTransport{Reader: reader, Writer: outbound})
	require.NoError(t, err)
	return s, frames
}

type failPromptTraceWriter struct {
	*io.PipeWriter
	failed bool
}

func (w *failPromptTraceWriter) Write(p []byte) (int, error) {
	if !w.failed && bytes.Contains(p, []byte(`"apply_flag_settings"`)) {
		w.failed = true
		return 0, errors.New("synthetic control write failure")
	}
	return w.PipeWriter.Write(p)
}

type promptTraceWriteSignal struct {
	io.WriteCloser
	applyDone chan struct{}
	once      sync.Once
}

func (w *promptTraceWriteSignal) Write(p []byte) (int, error) {
	n, err := w.WriteCloser.Write(p)
	if bytes.Contains(p, []byte(`"apply_flag_settings"`)) {
		w.once.Do(func() { close(w.applyDone) })
	}
	return n, err
}

func takePromptTraceFrame(t *testing.T, frames <-chan json.RawMessage) json.RawMessage {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("missing synthetic frame")
		return nil
	}
}

func assertPromptTraceApply(t *testing.T, frame json.RawMessage, id string, trace TraceContext) {
	t.Helper()
	want := fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":"apply_flag_settings","settings":{"env":{"TRACEPARENT":%q,"TRACESTATE":%q}}}}`, id, trace.Traceparent, trace.Tracestate)
	assert.JSONEq(t, want, string(frame))
	assert.Less(t, bytes.Index(frame, []byte(`"TRACEPARENT"`)), bytes.Index(frame, []byte(`"TRACESTATE"`)), "stable variable order")
}

func assertPromptTraceUser(t *testing.T, frames <-chan json.RawMessage, text string) {
	t.Helper()
	assert.JSONEq(t, fmt.Sprintf(`{"type":"user","session_id":"","parent_tool_use_id":null,"message":{"role":"user","content":%q}}`, text), string(takePromptTraceFrame(t, frames)))
}

func submitPromptTrace(t *testing.T, s *Session, trace *TraceContext, text string) {
	t.Helper()
	ctx := t.Context()
	if trace != nil {
		ctx = WithTraceContext(ctx, *trace)
	}
	receipt, err := s.Submit(ctx, Input{Text: text})
	require.NoError(t, err)
	require.True(t, receipt.Written)
	require.NoError(t, s.Err())
}

func TestPromptTraceFramesAndStartupRestore(t *testing.T) {
	for _, baseline := range []TraceContext{{}, {Traceparent: "startup-parent", Tracestate: "startup=state"}} {
		t.Run(baseline.Traceparent, func(t *testing.T) {
			s, frames := openPromptTracePeer(t, []string{"TRACEPARENT=old", "TRACEPARENT=" + baseline.Traceparent, "TRACESTATE=" + baseline.Tracestate}, nil, false)
			submitPromptTrace(t, s, nil, "plain")
			assertPromptTraceUser(t, frames, "plain")
			trace := TraceContext{Traceparent: promptParent, Tracestate: "vendor=value"}
			submitPromptTrace(t, s, &trace, "traced")
			assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_2", trace)
			assertPromptTraceUser(t, frames, "traced")
			submitPromptTrace(t, s, &trace, "unchanged")
			assertPromptTraceUser(t, frames, "unchanged")
			trace.Tracestate = ""
			submitPromptTrace(t, s, &trace, "clear state")
			assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_3", trace)
			assertPromptTraceUser(t, frames, "clear state")
			submitPromptTrace(t, s, nil, "restore")
			assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_4", baseline)
			assertPromptTraceUser(t, frames, "restore")
			submitPromptTrace(t, s, nil, "restored")
			assertPromptTraceUser(t, frames, "restored")
			assert.Empty(t, frames)
		})
	}
}

func TestPromptTraceZeroWriteTransportFailure(t *testing.T) {
	session, frames := openPromptTracePeer(t, nil, nil, true)
	receipt, err := session.Submit(WithTraceContext(t.Context(), TraceContext{Traceparent: promptParent}), Input{Text: "must not send"})
	require.Error(t, err)
	assert.False(t, receipt.Written)
	var transportError *Error
	require.ErrorAs(t, err, &transportError)
	assert.Equal(t, "write", transportError.Op)
	assert.True(t, transportError.OutcomeUnknown)
	assert.Error(t, session.Err())
	assert.Empty(t, frames)
}

func TestPromptTraceBestEffortRewritesUnknown(t *testing.T) {
	for _, outcome := range []string{"reject", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			for _, restore := range []bool{false, true} {
				t.Run(strconv.FormatBool(restore), func(t *testing.T) {
					s, frames := openPromptTracePeer(t, nil, []string{outcome}, false)
					trace := TraceContext{Traceparent: promptParent}
					submitPromptTrace(t, s, &trace, "first")
					assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_2", trace)
					assertPromptTraceUser(t, frames, "first")
					assert.False(t, s.traceKnown)
					target := trace
					next := &trace
					if restore {
						target, next = TraceContext{}, nil
					}
					submitPromptTrace(t, s, next, "retry target")
					assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_3", target)
					assertPromptTraceUser(t, frames, "retry target")
					assert.True(t, s.traceKnown)
					assert.Empty(t, frames)
				})
			}
		})
	}
}

func TestPromptTraceInvalidCarrierAndContext(t *testing.T) {
	s, frames := openPromptTracePeer(t, nil, nil, false)
	for _, trace := range []TraceContext{{Tracestate: "vendor=only"}, {Traceparent: "invalid"}, {Traceparent: promptParent, Tracestate: "bad"}, {Traceparent: promptParent + "\n"}} {
		submitPromptTrace(t, s, &trace, "invalid carrier")
		assertPromptTraceUser(t, frames, "invalid carrier")
	}
	first := TraceContext{Traceparent: promptParent}
	second := TraceContext{Traceparent: "invalid"}
	ctx, cancel := context.WithCancel(WithTraceContext(context.Background(), first))
	defer cancel()
	got, ok := TraceContextFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, first, got)
	got, ok = TraceContextFromContext(WithTraceContext(ctx, second))
	require.True(t, ok)
	assert.Equal(t, second, got, "helpers retain invalid carrier without validation")
	_, ok = TraceContextFromContext(nil) //nolint:staticcheck // verifies nil-context lookup without a carrier.
	assert.False(t, ok)
	assert.Empty(t, frames)
}

func TestPromptTraceConcurrentSubmissionsAndEffortStaySerialized(t *testing.T) {
	s, frames := openPromptTracePeer(t, nil, nil, false)
	start := make(chan struct{})
	results := make(chan error, 9)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			<-start
			trace := TraceContext{Traceparent: promptParent, Tracestate: fmt.Sprintf("vendor=p%d", i)}
			_, err := s.Submit(WithTraceContext(t.Context(), trace), Input{Text: fmt.Sprintf("p%d", i)})
			results <- err
		})
	}
	wg.Go(func() { <-start; _, err := s.SetEffort(t.Context(), EffortChange{Level: new("xhigh")}); results <- err })
	close(start)
	wg.Wait()
	for range 9 {
		require.NoError(t, <-results)
	}
	// Each prompt's control/user pair is contiguous, and effort's own
	// get/apply pair cannot erase or split a trace update.
	for range 9 {
		first := takePromptTraceFrame(t, frames)
		var request struct {
			Request struct {
				Subtype  string
				Settings struct{ Env map[string]string }
			}
		}
		require.NoError(t, json.Unmarshal(first, &request))
		second := takePromptTraceFrame(t, frames)
		if request.Request.Subtype == "get_settings" {
			var effort struct {
				Request struct{ Settings map[string]any }
			}
			require.NoError(t, json.Unmarshal(second, &effort))
			assert.Equal(t, map[string]any{"effortLevel": "xhigh", "ultracode": true}, effort.Request.Settings)
			continue
		}
		require.Equal(t, "apply_flag_settings", request.Request.Subtype)
		assert.Equal(t, promptParent, request.Request.Settings.Env["TRACEPARENT"])
		var user struct {
			Type    string
			Message struct{ Content string }
		}
		require.NoError(t, json.Unmarshal(second, &user))
		assert.Equal(t, "user", user.Type)
		assert.Equal(t, "vendor="+user.Message.Content, request.Request.Settings.Env["TRACESTATE"])
		var settings struct {
			Request struct{ Settings map[string]any }
		}
		require.NoError(t, json.Unmarshal(first, &settings))
		assert.Len(t, settings.Request.Settings, 1, "trace must not overwrite unrelated flag settings")
	}
	assert.Empty(t, frames)
}

func TestPromptTraceOpenCapturesEffectiveStartupEnvironment(t *testing.T) {
	for _, inherit := range []bool{true, false} {
		t.Run(strconv.FormatBool(inherit), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wire.jsonl")
			t.Setenv("CLAUDE_GO_FIXTURE", "input_config")
			t.Setenv("CLAUDE_GO_TRANSCRIPT", path)
			t.Setenv("TRACEPARENT", "ambient-startup")
			t.Setenv("TRACESTATE", "ambient=state")
			o := SessionOptions(fixtureOptions(t, "input_config"))
			baseline := TraceContext{Traceparent: "ambient-startup", Tracestate: "ambient=state"}
			if inherit {
				o.Env = nil
			} else {
				o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path, "TRACEPARENT=explicit-startup")
				baseline = TraceContext{Traceparent: "explicit-startup"}
			}
			s, err := Open(t.Context(), o)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Abort() })
			t.Setenv("TRACEPARENT", "later-ambient")
			t.Setenv("TRACESTATE", "later=state")
			trace := TraceContext{Traceparent: promptParent}
			submitPromptTrace(t, s, &trace, "trace")
			submitPromptTrace(t, s, nil, "baseline")
			require.NoError(t, s.Close())
			frames := permissionTranscript(t, path)
			require.Len(t, frames, 4)
			assertPromptTraceApply(t, frames[0], "go_2", trace)
			assertPromptTraceApply(t, frames[2], "go_3", baseline)
		})
	}
}

func TestPromptTraceCancellationKeepsSessionUsable(t *testing.T) {
	s, frames := openPromptTracePeer(t, nil, []string{"timeout"}, false)
	writer := &promptTraceWriteSignal{
		WriteCloser: s.client.streamWriter,
		applyDone:   make(chan struct{}),
	}
	s.client.streamWriter = writer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	trace := TraceContext{Traceparent: promptParent}
	result := make(chan error, 1)
	go func() {
		_, err := s.Submit(WithTraceContext(ctx, trace), Input{Text: "canceled"})
		result <- err
	}()
	assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_2", trace)
	select {
	case <-writer.applyDone:
	case <-time.After(5 * time.Second):
		t.Fatal("settings frame write did not complete")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("canceled submission did not settle")
	}
	require.NoError(t, s.Err())
	submitPromptTrace(t, s, nil, "still usable")
	assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_3", TraceContext{})
	assertPromptTraceUser(t, frames, "still usable")
}

func TestPromptTraceStreamDoesNotInheritAmbientBaseline(t *testing.T) {
	t.Setenv("TRACEPARENT", "ambient-only")
	t.Setenv("TRACESTATE", "ambient=only")
	s, frames := openPromptTracePeer(t, nil, nil, false)
	trace := TraceContext{Traceparent: promptParent}
	submitPromptTrace(t, s, &trace, "trace")
	assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_2", trace)
	assertPromptTraceUser(t, frames, "trace")
	submitPromptTrace(t, s, nil, "empty baseline")
	assertPromptTraceApply(t, takePromptTraceFrame(t, frames), "go_3", TraceContext{})
	assertPromptTraceUser(t, frames, "empty baseline")
}
