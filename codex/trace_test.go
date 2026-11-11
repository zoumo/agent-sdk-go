package codex_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

const traceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestTraceContextHelpers(t *testing.T) {
	_, ok := codex.TraceContextFromContext(nil)
	assert.False(t, ok)
	_, ok = codex.TraceContextFromContext(context.Background())
	assert.False(t, ok)
	original := codex.TraceContext{Traceparent: types.Some(traceParent)}
	ctx := codex.WithTraceContext(context.Background(), original)
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	got, ok := codex.TraceContextFromContext(derived)
	require.True(t, ok)
	assert.Equal(t, original, got)
	invalid := types.W3cTraceContext{Tracestate: types.Some("invalid")}
	ctx = codex.WithTraceContext(derived, invalid)
	got, ok = codex.TraceContextFromContext(ctx)
	require.True(t, ok, "reading a carrier does not validate it")
	assert.Equal(t, invalid, got)
	got, _ = codex.TraceContextFromContext(derived)
	assert.Equal(t, original, got, "overrides do not change the parent context")
}

// This peer only acknowledges native requests and captures exact outbound
// envelopes. It does not simulate app-server span or steer behavior.
func newTracePeer(t *testing.T) (*codex.Client, <-chan map[string]json.RawMessage) {
	t.Helper()
	peer := newStreamPeer()
	frames := make(chan map[string]json.RawMessage, 64)
	finished := make(chan error, 1)
	go func() {
		err := func() error {
			reader := bufio.NewReader(peer.reader)
			for {
				frame, err := readCodexFrame(reader)
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
				var method string
				if err := json.Unmarshal(frame["method"], &method); err != nil {
					return err
				}
				frames <- frame
				result := `{}`
				switch method {
				case "initialize":
					result = initResult
				case "initialized":
					continue
				case "thread/start", "thread/resume":
					result = startResult
				case "turn/start":
					result = `{"turn":` + nativeTurn + `}`
				case "thread/unsubscribe":
					result = `{"status":"unsubscribed"}`
				}
				if _, err := fmt.Fprintf(peer.writer, "{\"id\":%s,\"result\":%s}\n", frame["id"], result); err != nil {
					return err
				}
			}
		}()
		_ = peer.writer.Close()
		peer.wait <- err
		finished <- err
	}()
	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "trace-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.reader.Close()
		_ = peer.writer.Close()
		select {
		case err := <-finished:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("trace peer did not stop")
		}
	})
	<-frames // initialize
	<-frames // initialized
	return client, frames
}

func TestSessionSubmitTraceEnvelope(t *testing.T) {
	client, frames := newTracePeer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	traced := codex.WithTraceContext(ctx, codex.TraceContext{Traceparent: types.Some(traceParent)})
	session, err := codex.Open(traced, codex.SessionOptions{Client: client})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	assert.NotContains(t, <-frames, "trace", "opening a Session never consumes prompt trace")

	for _, test := range []struct {
		name    string
		carrier *types.W3cTraceContext
		want    string
	}{
		{name: "valid", carrier: &types.W3cTraceContext{Traceparent: types.Some(traceParent), Tracestate: types.Some("vendor=one")}, want: `{"traceparent":"` + traceParent + `","tracestate":"vendor=one"}`},
		{name: "absent"},
		{name: "different", carrier: &types.W3cTraceContext{Traceparent: types.Some("00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01")}, want: `{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01"}`},
		{name: "null state", carrier: &types.W3cTraceContext{Traceparent: types.Some(traceParent), Tracestate: types.Null[string]()}, want: `{"traceparent":"` + traceParent + `"}`},
		{name: "empty state", carrier: &types.W3cTraceContext{Traceparent: types.Some(traceParent), Tracestate: types.Some("")}, want: `{"traceparent":"` + traceParent + `"}`},
		{name: "zero", carrier: &types.W3cTraceContext{}},
		{name: "null parent", carrier: &types.W3cTraceContext{Traceparent: types.Null[string]()}},
		{name: "empty parent", carrier: &types.W3cTraceContext{Traceparent: types.Some("")}},
		{name: "state only", carrier: &types.W3cTraceContext{Tracestate: types.Some("vendor=one")}},
		{name: "malformed", carrier: &types.W3cTraceContext{Traceparent: types.Some("invalid")}},
		{name: "zero trace id", carrier: &types.W3cTraceContext{Traceparent: types.Some("00-00000000000000000000000000000000-00f067aa0ba902b7-01")}},
		{name: "zero span id", carrier: &types.W3cTraceContext{Traceparent: types.Some("00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01")}},
		{name: "invalid state", carrier: &types.W3cTraceContext{Traceparent: types.Some(traceParent), Tracestate: types.Some("vendor=one,vendor=two")}},
		{name: "unknown fields excluded", carrier: &types.W3cTraceContext{Traceparent: types.Some(traceParent), UnknownFields: map[string]json.RawMessage{"future": json.RawMessage(`true`)}}, want: `{"traceparent":"` + traceParent + `"}`},
		{name: "absent again"},
	} {
		t.Run(test.name, func(t *testing.T) {
			submitCtx := ctx
			if test.carrier != nil {
				submitCtx = codex.WithTraceContext(ctx, *test.carrier)
			}
			response, err := session.Submit(submitCtx, codex.Input{Input: []types.UserInput{{Text: &types.TextUserInput{Text: "hello"}}}})
			require.NoError(t, err, "invalid trace must not reject input")
			assert.Equal(t, "native-turn-response", response.Response.Turn.ID)
			frame := <-frames
			assert.JSONEq(t, `"turn/start"`, string(frame["method"]))
			assert.JSONEq(t, `{"input":[{"type":"text","text":"hello"}],"threadId":"native-thread-42"}`, string(frame["params"]))
			if test.want == "" {
				assert.NotContains(t, frame, "trace")
				assert.Len(t, frame, 3)
			} else {
				assert.JSONEq(t, test.want, string(frame["trace"]))
				assert.Len(t, frame, 4)
			}
		})
	}
}

func TestRawCallsIgnorePromptTraceContext(t *testing.T) {
	client, frames := newTracePeer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx = codex.WithTraceContext(ctx, types.W3cTraceContext{Traceparent: types.Some(traceParent)})
	require.NoError(t, client.Call(ctx, "turn/start", json.RawMessage(`{"threadId":"native-thread-42","input":[]}`), nil))
	assert.NotContains(t, <-frames, "trace")
	_, err := client.StartThread(ctx, types.ThreadStartParams{})
	require.NoError(t, err)
	assert.NotContains(t, <-frames, "trace")
	_, err = client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: "native-thread-42"})
	require.NoError(t, err)
	assert.NotContains(t, <-frames, "trace")
}
