package pi_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

type sessionExchange struct {
	fields map[string]string
	data   string
	reject string
}

func sessionPeer(exchanges ...sessionExchange) func(peer) error {
	return func(p peer) error {
		for _, exchange := range exchanges {
			var got map[string]string
			if err := p.input.Decode(&got); err != nil {
				return err
			}
			id := got["id"]
			delete(got, "id")
			if id == "" || !maps.Equal(got, exchange.fields) {
				return fmt.Errorf("got id %q, fields %v, want %v", id, got, exchange.fields)
			}
			response := map[string]any{"type": "response", "id": id, "command": got["type"], "success": exchange.reject == ""}
			if exchange.reject != "" {
				response["error"] = exchange.reject
			}
			if exchange.data != "" {
				response["data"] = json.RawMessage(exchange.data)
			}
			if err := json.NewEncoder(p.output).Encode(response); err != nil {
				return err
			}
		}
		if c, err := p.receive(); !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected EOF, got command %q, error %v", c.Type, err)
		}
		return nil
	}
}

func TestForkMessages(t *testing.T) {
	fields := map[string]string{"type": "get_fork_messages"}
	tests := []struct {
		name string
		data string
		want []pi.ForkMessage
	}{
		{"native order", `{"messages":[{"entryId":"z","text":"first"},{"entryId":"a","text":""}]}`, []pi.ForkMessage{{EntryID: "z", Text: "first"}, {EntryID: "a", Text: ""}}},
		{"empty list", `{"messages":[]}`, []pi.ForkMessage{}},
		{"missing list", `{}`, nil},
		{"null list", `{"messages":null}`, nil},
		{"missing text", `{"messages":[{"entryId":"z"}]}`, nil},
		{"missing ID", `{"messages":[{"text":"first"}]}`, nil},
		{"wrong type", `{"messages":[{"entryId":7,"text":"first"}]}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := runPeer(t, sessionPeer(sessionExchange{fields: fields, data: tc.data}))
			id := s.ID()
			messages, err := s.ForkMessages(ctx)
			if tc.want == nil {
				var malformed *pi.DispatchError
				require.ErrorAs(t, err, &malformed)
				assert.True(t, malformed.Sent)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, messages)
			}
			assert.Equal(t, id, s.ID())
		})
	}
}

func TestLastAssistantText(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		want      *string
		malformed bool
	}{
		{"selected text", `{"text":"Pi selected this"}`, new("Pi selected this"), false},
		{"empty text", `{"text":""}`, new(""), false},
		{"null text", `{"text":null}`, nil, false},
		{"omitted text", `{}`, nil, false},
		{"wrong text type", `{"text":42}`, nil, true},
		{"null data", `null`, nil, true},
		{"missing data", "", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := runPeer(t, sessionPeer(sessionExchange{fields: map[string]string{"type": "get_last_assistant_text"}, data: tc.data}))
			text, err := s.LastAssistantText(ctx)
			if tc.malformed {
				var malformed *pi.DispatchError
				require.ErrorAs(t, err, &malformed)
				assert.True(t, malformed.Sent)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
		})
	}
}

func TestSetSessionName(t *testing.T) {
	s, ctx := runPeer(t, sessionPeer(
		sessionExchange{fields: map[string]string{"type": "set_session_name", "name": " My session "}},
		sessionExchange{fields: map[string]string{"type": "get_state"}, data: `{"sessionId":"native-1","sessionName":"My session"}`},
		sessionExchange{fields: map[string]string{"type": "set_session_name", "name": " "}, reject: "Session name cannot be empty"},
	))
	id := s.ID()
	require.NoError(t, s.SetSessionName(ctx, " My session "))
	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.JSONEq(t, `{"sessionId":"native-1","sessionName":"My session"}`, string(state.Raw))
	assert.Equal(t, id, s.ID())
	var rejected *pi.CommandError
	require.ErrorAs(t, s.SetSessionName(ctx, " "), &rejected)
	assert.Equal(t, "Session name cannot be empty", rejected.Message)
}

func TestExportHTML(t *testing.T) {
	defaultPath := map[string]string{"type": "export_html"}
	s, ctx := runPeer(t, sessionPeer(
		sessionExchange{fields: defaultPath, data: `{"path":"/pi/chosen.html"}`},
		sessionExchange{fields: map[string]string{"type": "export_html", "outputPath": "session.html"}, data: `{"path":"/pi/session.html"}`},
		sessionExchange{fields: map[string]string{"type": "export_html", "outputPath": "blocked/session.html"}, reject: "permission denied"},
		sessionExchange{fields: defaultPath, data: `{}`},
	))
	path, err := s.ExportHTML(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "/pi/chosen.html", path)
	path, err = s.ExportHTML(ctx, "session.html")
	require.NoError(t, err)
	assert.Equal(t, "/pi/session.html", path)
	_, err = s.ExportHTML(ctx, "blocked/session.html")
	var rejected *pi.CommandError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "permission denied", rejected.Message)
	_, err = s.ExportHTML(ctx, "")
	var malformed *pi.DispatchError
	require.ErrorAs(t, err, &malformed)
	assert.True(t, malformed.Sent)
	assert.ErrorContains(t, err, "export_html response data")
}
