package codex_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
)

// A malformed later frame must not erase a complete response or notification
// that preceded it in the same socket read. Stream semantics cannot depend on
// whether TCP/TLS happened to coalesce several WebSocket frames into one read.
func TestRemoteValidPrefixSurvivesMalformedFrame(t *testing.T) {
	reportPeerError := collectTransportPeerErrors(t)

	malformed := []struct {
		name  string
		frame []byte
	}{
		{"masked", []byte{0x81, 0x80, 0, 0, 0, 0}},
		{"nonminimal-126", []byte{0x81, 126, 0, 0}},
		{"nonminimal-127", []byte{0x81, 127, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"high-bit-length", []byte{0x81, 127, 0x80, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, secure := range []bool{false, true} {
		for _, bad := range malformed {
			t.Run(fmt.Sprintf("tls=%v/%s", secure, bad.name), func(t *testing.T) {
				s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capture := &securityCaptureWriter{ResponseWriter: w}
					ws, err := websocket.Accept(capture, r, nil)
					if err != nil {
						reportPeerError("%v", err)
						return
					}
					defer ws.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
					defer cancel()
					initialize, err := securityRead(ctx, ws)
					if err != nil || initialize.Method != "initialize" {
						reportPeerError("expected initialize: %v", err)
						return
					}
					if err = ws.Write(ctx, websocket.MessageText, securityResponse(initialize.ID, securityInitResult)); err != nil {
						reportPeerError("%v", err)
						return
					}
					initialized, err := securityRead(ctx, ws)
					if err != nil || initialized.Method != "initialized" {
						reportPeerError("expected initialized: %v", err)
						return
					}
					start, err := securityRead(ctx, ws)
					if err != nil || start.Method != "thread/start" {
						reportPeerError("expected Session start: %v", err)
						return
					}
					if err = ws.Write(ctx, websocket.MessageText, securityResponse(start.ID, startResult)); err != nil {
						reportPeerError("%v", err)
						return
					}
					call, err := securityRead(ctx, ws)
					if err != nil || call.Method != "synthetic/settle-before-invalid" {
						reportPeerError("expected test call: %v", err)
						return
					}
					response := securityResponse(call.ID, `"settled"`)
					notification := []byte(`{"method":"synthetic/valid-prefix","params":{"threadId":"native-thread-42","value":1}}`)
					if len(response) >= 126 || len(notification) >= 126 {
						reportPeerError("fixture requires canonical short frame payloads")
						return
					}
					frames := append([]byte{0x81, byte(len(response))}, response...)
					frames = append(frames, 0x81, byte(len(notification)))
					frames = append(frames, notification...)
					frames = append(frames, bad.frame...)
					_ = capture.conn.SetWriteDeadline(time.Now().Add(time.Second))
					if _, err = capture.conn.Write(frames); err != nil {
						reportPeerError("%v", err)
						return
					}
					_, _, _ = ws.Read(ctx)
				}))
				if secure {
					s.StartTLS()
				} else {
					s.Start()
				}
				t.Cleanup(s.Close)
				options := securityOptions(securityEndpoint(s))
				if secure {
					roots := x509.NewCertPool()
					roots.AddCert(s.Certificate())
					options.TLSConfig = &tls.Config{RootCAs: roots}
				}
				c := securityOpen(t, options)
				sub, err := codex.Open(context.Background(), codex.SessionOptions{Client: c})
				require.NoError(t, err)
				var result string
				{
					err = c.Call(context.Background(), "synthetic/settle-before-invalid", nil, &result)
					require.NoError(t, err)
					require.False(t, result != "settled", "valid response was lost before later framing failure: result=%q error=%v", result, err)
				}
				select {
				case <-c.Done():
				case <-time.After(time.Second):
					require.FailNow(t, "malformed later frame did not terminate the connection")
				}
				require.False(t, !errors.Is(c.Err(), codex.ErrProtocol) || !errors.Is(sub.Err(), codex.ErrProtocol), "terminal protocol cause lost: client=%v subscription=%v", c.Err(), sub.Err())
				received, err := sub.Receive(context.Background())
				require.NoError(t, err)
				event := received.Event
				require.NotNil(t, event)
				require.Equal(t, "synthetic/valid-prefix", event.Method)
				require.True(t, json.Valid(event.Params))
				require.Equal(t, `{"threadId":"native-thread-42","value":1}`, string(event.Params))
				_, err = sub.Receive(context.Background())
				require.ErrorIs(t, err, codex.ErrProtocol)

			})
		}
	}
}
