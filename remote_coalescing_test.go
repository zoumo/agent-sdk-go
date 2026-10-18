package codex_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zoumo/codex-sdk-go/types"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	codex "github.com/zoumo/codex-sdk-go"
)

// A malformed later frame must not erase a complete response or notification
// that preceded it in the same socket read. Stream semantics cannot depend on
// whether TCP/TLS happened to coalesce several WebSocket frames into one read.
func TestRemoteValidPrefixSurvivesMalformedFrame(t *testing.T) {
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
						t.Error(err)
						return
					}
					defer ws.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
					defer cancel()
					initialize, err := securityRead(ctx, ws)
					if err != nil || initialize.Method != "initialize" {
						t.Errorf("expected initialize: %v", err)
						return
					}
					if err = ws.Write(ctx, websocket.MessageText, securityResponse(initialize.ID, securityInitResult)); err != nil {
						t.Error(err)
						return
					}
					initialized, err := securityRead(ctx, ws)
					if err != nil || initialized.Method != "initialized" {
						t.Errorf("expected initialized: %v", err)
						return
					}
					call, err := securityRead(ctx, ws)
					if err != nil || call.Method != "synthetic/settle-before-invalid" {
						t.Errorf("expected test call: %v", err)
						return
					}
					response := securityResponse(call.ID, `"settled"`)
					notification := []byte(`{"method":"synthetic/valid-prefix","params":{"value":1}}`)
					if len(response) >= 126 || len(notification) >= 126 {
						t.Error("fixture requires canonical short frame payloads")
						return
					}
					frames := append([]byte{0x81, byte(len(response))}, response...)
					frames = append(frames, 0x81, byte(len(notification)))
					frames = append(frames, notification...)
					frames = append(frames, bad.frame...)
					_ = capture.conn.SetWriteDeadline(time.Now().Add(time.Second))
					if _, err = capture.conn.Write(frames); err != nil {
						t.Error(err)
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
				sub, err := c.Subscribe(context.Background(), "native-thread-42", types.SubscriptionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				nextTree(t, sub) // initial incomplete observation
				var result string
				if err = c.Call(context.Background(), "synthetic/settle-before-invalid", nil, &result); err != nil || result != "settled" {
					t.Fatalf("valid response was lost before later framing failure: result=%q error=%v", result, err)
				}
				select {
				case <-c.Done():
				case <-time.After(time.Second):
					t.Fatal("malformed later frame did not terminate the connection")
				}
				if !errors.Is(c.Err(), codex.ErrProtocol) || !errors.Is(sub.Err(), codex.ErrProtocol) {
					t.Fatalf("terminal protocol cause lost: client=%v subscription=%v", c.Err(), sub.Err())
				}
				event, ok := <-sub.Events()
				if !ok || event.Method != "synthetic/valid-prefix" || !json.Valid(event.Params) || string(event.Params) != `{"value":1}` {
					t.Fatalf("valid notification preceding failure was lost: %+v open=%v", event, ok)
				}
				if gap := nextTree(t, sub); gap.Reason != "permanent-gap" {
					t.Fatalf("missing gap: %+v", gap)
				}
				if _, ok = <-sub.Events(); ok {
					t.Fatal("malformed frame produced an additional event")
				}
			})
		}
	}
}
