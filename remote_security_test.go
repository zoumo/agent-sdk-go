package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

// These peers are independently authored, external-package fixtures. They use
// only loopback sockets and synthetic credentials, with no Codex process/model.
const securityToken = "synthetic-security-secret.ABC_123~+/=="
const securityInitResult = `{"userAgent":"security-peer/only","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`

type securityEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func securityOptions(endpoint string) codex.RemoteOptions {
	return codex.RemoteOptions{
		URL: endpoint, BearerToken: securityToken,
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "security-test", Version: "1"}},
		Limits:     codex.Limits{CallTimeout: 2 * time.Second, WriteTimeout: time.Second, ShutdownTimeout: time.Second},
	}
}

func securityEndpoint(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func securityRead(ctx context.Context, ws *websocket.Conn) (securityEnvelope, error) {
	kind, raw, err := ws.Read(ctx)
	if err != nil {
		return securityEnvelope{}, err
	}
	var envelope securityEnvelope
	if kind != websocket.MessageText || bytes.HasSuffix(raw, []byte("\n")) || !json.Valid(raw) {
		return envelope, errors.New("client must send one JSON text envelope without a JSONL delimiter")
	}
	var members map[string]json.RawMessage
	if err = json.Unmarshal(raw, &members); err != nil || members == nil {
		return envelope, errors.New("client frame must be an object")
	}
	if _, exists := members["jsonrpc"]; exists {
		return envelope, errors.New("client must use native envelopes without jsonrpc")
	}
	err = json.Unmarshal(raw, &envelope)
	return envelope, err
}

func securityResponse(id json.RawMessage, result string) []byte {
	return []byte(fmt.Sprintf(`{"id":%s,"result":%s}`, id, result))
}

// securityServer performs a native initialize handshake, then runs the supplied
// independent wire script. Every handler has a finite lifetime even on failure.
func securityServer(t *testing.T, secure bool, script func(context.Context, *websocket.Conn, *http.Request) error) (*httptest.Server, codex.RemoteOptions) {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			t.Errorf("accept independent peer: %v", err)
			return
		}
		defer ws.CloseNow()
		envelope, err := securityRead(ctx, ws)
		if err != nil || envelope.Method != "initialize" || len(envelope.ID) == 0 {
			t.Errorf("expected initialize: envelope=%+v error=%v", envelope, err)
			return
		}
		if err = ws.Write(ctx, websocket.MessageText, securityResponse(envelope.ID, securityInitResult)); err != nil {
			t.Errorf("reply to initialize: %v", err)
			return
		}
		envelope, err = securityRead(ctx, ws)
		if err != nil || envelope.Method != "initialized" || len(envelope.ID) != 0 {
			t.Errorf("expected initialized notification: envelope=%+v error=%v", envelope, err)
			return
		}
		if err = script(ctx, ws, r); err != nil {
			t.Errorf("independent peer: %v", err)
		}
	}))
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	var config *tls.Config
	if secure {
		s.StartTLS()
		roots := x509.NewCertPool()
		roots.AddCert(s.Certificate())
		config = &tls.Config{RootCAs: roots}
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	options := securityOptions(securityEndpoint(s))
	options.TLSConfig = config
	return s, options
}

func securityEcho(ctx context.Context, ws *websocket.Conn, _ *http.Request) error {
	for {
		envelope, err := securityRead(ctx, ws)
		if err != nil {
			return nil // Client cleanup or cancellation closes the socket.
		}
		if err = ws.Write(ctx, websocket.MessageText, securityResponse(envelope.ID, `{"ok":true}`)); err != nil {
			return err
		}
	}
}

func securityOpen(t *testing.T, options codex.RemoteOptions) *codex.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := codex.NewRemote(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close remote: %v", err)
		}
	})
	return c
}

func securityNoSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(rendered, securityToken) || strings.Contains(rendered, url.QueryEscape(securityToken)) {
			t.Error("public error disclosed synthetic credential")
		}
	}
}

func TestRemoteSecurityExactBearerAndNoOrigin(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", secure), func(t *testing.T) {
			seen := make(chan *http.Request, 1)
			_, options := securityServer(t, secure, func(ctx context.Context, ws *websocket.Conn, r *http.Request) error {
				seen <- r.Clone(context.Background())
				return securityEcho(ctx, ws, r)
			})
			options.URL += "/app-server"
			c := securityOpen(t, options)
			if err := c.Call(context.Background(), "synthetic/echo", nil, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case r := <-seen:
				if got := r.Header.Values("Authorization"); !reflect.DeepEqual(got, []string{"Bearer " + securityToken}) {
					t.Error("Authorization must contain exactly the supplied bearer token once")
				}
				if _, present := r.Header["Origin"]; present {
					t.Error("non-browser client must not send Origin")
				}
				if r.URL.Path != "/app-server" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" {
					t.Error("authentication leaked outside the explicit Authorization header")
				}
				if r.Header.Get("Sec-WebSocket-Extensions") != "" {
					t.Error("bounded protocol transport must not negotiate compression")
				}
				if secure && (r.TLS == nil || r.TLS.Version < tls.VersionTLS12) {
					t.Error("secure transport negotiated less than TLS 1.2")
				}
			case <-time.After(time.Second):
				t.Fatal("peer did not observe completed initialization")
			}
		})
	}
}

func TestRemoteSecurityTLSVerificationAndConfigIsolation(t *testing.T) {
	s, options := securityServer(t, true, securityEcho)
	untrusted := options
	untrusted.TLSConfig = nil
	if c, err := codex.NewRemote(context.Background(), untrusted); err == nil || c != nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatal("self-signed peer was accepted without explicit trust")
	} else {
		securityNoSecret(t, err)
		if !errors.Is(err, codex.ErrRemoteTransport) {
			t.Fatalf("TLS rejection classification: %v", err)
		}
	}
	roots := options.TLSConfig.RootCAs
	beforeRoots := roots.Clone()
	options.TLSConfig.MinVersion = tls.VersionTLS10
	options.TLSConfig.NextProtos = []string{"http/1.1"}
	options.TLSConfig.ServerName = s.Certificate().DNSNames[0]
	config := options.TLSConfig
	before := config.Clone()
	c := securityOpen(t, options)
	if err := c.Call(context.Background(), "synthetic/echo", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, before) || config.RootCAs != roots || !roots.Equal(beforeRoots) {
		t.Fatal("NewRemote mutated caller-owned TLS configuration or root pool")
	}
}

func TestRemoteSecurityRejectsInvalidInputBeforeDial(t *testing.T) {
	var requests, connections atomic.Int64
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected dial", http.StatusBadRequest)
	}))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	s.Start()
	t.Cleanup(s.Close)
	base := securityOptions(securityEndpoint(s))
	cases := []struct {
		name   string
		change func(*codex.RemoteOptions)
	}{
		{"missing-url", func(o *codex.RemoteOptions) { o.URL = "" }},
		{"http-scheme", func(o *codex.RemoteOptions) { o.URL = s.URL }},
		{"missing-host", func(o *codex.RemoteOptions) { o.URL = "ws:///app-server" }},
		{"opaque-url", func(o *codex.RemoteOptions) { o.URL = "ws:opaque" }},
		{"bad-escape", func(o *codex.RemoteOptions) { o.URL += "/%xx" }},
		{"bad-port", func(o *codex.RemoteOptions) { o.URL = "ws://127.0.0.1:bad" }},
		{"nonloopback-plaintext", func(o *codex.RemoteOptions) { o.URL = strings.Replace(o.URL, "127.0.0.1", "0.0.0.0", 1) }},
		{"userinfo", func(o *codex.RemoteOptions) {
			o.URL = strings.Replace(o.URL, "://", "://"+url.UserPassword("synthetic-user", securityToken).String()+"@", 1)
		}},
		{"query", func(o *codex.RemoteOptions) { o.URL += "?token=" + url.QueryEscape(securityToken) }},
		{"empty-query", func(o *codex.RemoteOptions) { o.URL += "?" }},
		{"fragment", func(o *codex.RemoteOptions) { o.URL += "#" + securityToken }},
		{"empty-fragment", func(o *codex.RemoteOptions) { o.URL += "#" }},
		{"token-path", func(o *codex.RemoteOptions) { o.URL += "/" + securityToken }},
		{"escaped-token-path", func(o *codex.RemoteOptions) { o.URL += "/" + url.PathEscape(securityToken) }},
		{"skip-tls-verification-ws", func(o *codex.RemoteOptions) { o.TLSConfig = &tls.Config{InsecureSkipVerify: true} }},
		{"skip-tls-verification-wss", func(o *codex.RemoteOptions) {
			o.URL = strings.Replace(o.URL, "ws:", "wss:", 1)
			o.TLSConfig = &tls.Config{InsecureSkipVerify: true}
		}},
	}
	if strconv.IntSize == 64 {
		cases = append(cases, struct {
			name   string
			change func(*codex.RemoteOptions)
		}{"overflowing-frame-limit", func(o *codex.RemoteOptions) {
			large := int64(math.MaxInt64)
			o.Limits.MaxFrameBytes = int(large)
		}})
	}
	for _, token := range []string{"", "=", "===", "hello world", "abc\r\nInjected: true", "abc\t", "abc:def", "abc=def", "synthetic-é", "Bearer value"} {
		token := token
		cases = append(cases, struct {
			name   string
			change func(*codex.RemoteOptions)
		}{fmt.Sprintf("invalid-token-%q", token), func(o *codex.RemoteOptions) { o.BearerToken = token }})
	}
	// Every configurable bound rejects negatives before a socket is opened.
	for _, field := range []string{"MaxFrameBytes", "OutboundQueue", "PendingCalls", "Subscriptions", "EventQueue", "DiagnosticBytes", "ActiveRequests", "RequestQueue", "RequestTimeout", "CallTimeout", "WriteTimeout", "ShutdownTimeout"} {
		field := field
		cases = append(cases, struct {
			name   string
			change func(*codex.RemoteOptions)
		}{"negative-" + field, func(o *codex.RemoteOptions) { reflect.ValueOf(&o.Limits).Elem().FieldByName(field).SetInt(-1) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := base
			tc.change(&options)
			c, err := codex.NewRemote(context.Background(), options)
			if c != nil {
				_ = c.Close()
				t.Fatal("invalid options created a client")
			}
			securityNoSecret(t, err)
			if requests.Load() != 0 || connections.Load() != 0 {
				t.Fatal("invalid options dialed the peer")
			}
		})
	}
	defaults := reflect.ValueOf(codex.DefaultLimits())
	for i := 0; i < defaults.NumField(); i++ {
		if defaults.Field(i).Int() <= 0 {
			t.Errorf("default %s is unbounded", defaults.Type().Field(i).Name)
		}
	}
}

func TestRemoteSecurityRedirectsNeverForwardCredentials(t *testing.T) {
	for _, crossOrigin := range []bool{false, true} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			t.Run(fmt.Sprintf("cross-origin=%v/status=%d", crossOrigin, status), func(t *testing.T) {
				var initial, target atomic.Int64
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target.Add(1)
					http.Error(w, "redirect followed", 400)
				}))
				t.Cleanup(destination.Close)
				var source *httptest.Server
				source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/redirect-target" {
						target.Add(1)
						http.Error(w, "redirect followed", 400)
						return
					}
					initial.Add(1)
					if r.Header.Get("Authorization") != "Bearer "+securityToken {
						t.Error("initial peer did not receive explicit authentication")
					}
					location := "/redirect-target"
					if crossOrigin {
						location = destination.URL + location
					}
					w.Header().Set("Location", location)
					w.WriteHeader(status)
				}))
				t.Cleanup(source.Close)
				c, err := codex.NewRemote(context.Background(), securityOptions(securityEndpoint(source)))
				if c != nil {
					_ = c.Close()
					t.Fatal("redirect unexpectedly created a client")
				}
				securityNoSecret(t, err)
				if !errors.Is(err, codex.ErrRemoteTransport) || initial.Load() != 1 || target.Load() != 0 {
					t.Fatalf("redirect must fail after one original request: initial=%d target=%d error=%v", initial.Load(), target.Load(), err)
				}
			})
		}
	}
}

func TestRemoteSecurityReflectedHandshakeErrorsAreSafe(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusSwitchingProtocols} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reflected := r.Header.Get("Authorization")
				w.Header().Set("WWW-Authenticate", reflected)
				w.Header().Set("X-Reflected-Token", reflected)
				if status == http.StatusSwitchingProtocols {
					w.Header().Set("Upgrade", "websocket")
					w.Header().Set("Connection", "Upgrade")
					w.Header().Set("Sec-WebSocket-Accept", reflected)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, reflected)
			}))
			t.Cleanup(s.Close)
			c, err := codex.NewRemote(context.Background(), securityOptions(securityEndpoint(s)))
			if c != nil {
				_ = c.Close()
				t.Fatal("invalid handshake accepted")
			}
			securityNoSecret(t, err)
			if !errors.Is(err, codex.ErrRemoteTransport) {
				t.Fatalf("unexpected classification: %v", err)
			}
		})
	}
}

func TestRemoteSecurityReflectedCloseReasonIsSafe(t *testing.T) {
	_, options := securityServer(t, false, func(ctx context.Context, ws *websocket.Conn, r *http.Request) error {
		if _, err := securityRead(ctx, ws); err != nil {
			return err
		}
		_ = ws.Close(websocket.StatusPolicyViolation, r.Header.Get("Authorization"))
		return nil
	})
	c := securityOpen(t, options)
	err := c.Call(context.Background(), "synthetic/reflect-close", nil, nil)
	securityNoSecret(t, err)
	if !errors.Is(err, codex.ErrRemoteTransport) {
		t.Fatalf("unexpected close classification: %v", err)
	}
	securityNoSecret(t, c.Err())
	if strings.Contains(fmt.Sprintf("%+v", c.Diagnostics()), securityToken) {
		t.Fatal("diagnostics disclosed close reason credential")
	}
}

func TestRemoteSecurityReflectedNativeRPCErrorsAreSafe(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, phase := range []string{"initialize", "call"} {
			for _, encoding := range []string{"literal", "unicode-escaped", "url-encoded", "duplicate-values-literal", "duplicate-values-unicode", "duplicate-keys-unicode", "no-secret"} {
				t.Run(fmt.Sprintf("tls=%v/%s/%s", secure, phase, encoding), func(t *testing.T) {
					reflection := securityToken
					if encoding == "url-encoded" {
						reflection = url.QueryEscape(reflection)
					} else if encoding == "no-secret" {
						reflection = "public synthetic information"
					}
					quoted, err := json.Marshal("peer reflected Bearer " + reflection)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(encoding, "unicode") {
						quoted = bytes.ReplaceAll(quoted, []byte("synthetic"), []byte(`\u0073ynthetic`))
					}
					// Includes an unknown future field, nested arrays, and integers whose
					// exact values would be damaged by a float64 decode/re-encode pass.
					rpcJSON := fmt.Sprintf(`{"code":-32001,"message":%s,"data":{"nested":[{"echo":%s}],"safeLarge":9007199254740993,"safeText":"retain me"},"future":{"echo":%s},"safeMax":9223372036854775807}`, quoted, quoted, quoted)
					if strings.HasPrefix(encoding, "duplicate-") {
						duplicates := fmt.Sprintf(`{ "x": %s, "x": "safe" }`, quoted)
						if encoding == "duplicate-keys-unicode" {
							duplicates = fmt.Sprintf(`{ %s: "first", %s: "last" }`, quoted, quoted)
						}
						// The only secrets are in duplicate-bearing opaque objects. A
						// map decoder must not discard a shadowed value before checking it.
						rpcJSON = fmt.Sprintf(`{"code":-32001,"message":"safe message","data":{"duplicates":%s,"safeLarge":9007199254740993,"safeText":"retain me"},"future":%s,"safeMax":9223372036854775807}`, duplicates, duplicates)
					}
					respond := func(ctx context.Context, ws *websocket.Conn, id json.RawMessage) error {
						return ws.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":%s,"error":%s}`, id, rpcJSON)))
					}
					var options codex.RemoteOptions
					if phase == "call" {
						_, options = securityServer(t, secure, func(ctx context.Context, ws *websocket.Conn, r *http.Request) error {
							envelope, err := securityRead(ctx, ws)
							if err != nil {
								return err
							}
							if err = respond(ctx, ws, envelope.ID); err != nil {
								return err
							}
							return securityEcho(ctx, ws, r)
						})
					} else {
						s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							ws, err := websocket.Accept(w, r, nil)
							if err != nil {
								t.Error(err)
								return
							}
							defer ws.CloseNow()
							ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
							defer cancel()
							envelope, err := securityRead(ctx, ws)
							if err != nil || envelope.Method != "initialize" {
								t.Errorf("expected initialize: %v", err)
								return
							}
							if err = respond(ctx, ws, envelope.ID); err != nil {
								t.Error(err)
								return
							}
							if _, _, err = ws.Read(ctx); err == nil {
								t.Error("client sent another envelope after failed initialization")
							}
						}))
						if secure {
							s.StartTLS()
						} else {
							s.Start()
						}
						t.Cleanup(s.Close)
						options = securityOptions(securityEndpoint(s))
						if secure {
							roots := x509.NewCertPool()
							roots.AddCert(s.Certificate())
							options.TLSConfig = &tls.Config{RootCAs: roots}
						}
					}
					var c *codex.Client
					if phase == "call" {
						c = securityOpen(t, options)
						err = c.Call(context.Background(), "synthetic/reflected-error", nil, nil)
					} else {
						c, err = codex.NewRemote(context.Background(), options)
						if c != nil {
							_ = c.Close()
							t.Fatal("failed initialize unexpectedly produced a client")
						}
					}
					securityNoSecret(t, err)
					var rpc *codex.RPCError
					var call *codex.CallError
					if !errors.As(err, &rpc) || !errors.As(err, &call) || call.Dispatch != codex.Responded || rpc.Code != -32001 {
						t.Fatalf("redaction lost native RPC error or dispatch evidence: %v", err)
					}
					wantID := codex.IntegerID(1)
					if phase == "call" {
						wantID = codex.IntegerID(2)
					}
					if call.ID != wantID {
						t.Fatalf("native RPC error lost exact correlation: %v", call.ID)
					}
					for _, raw := range []json.RawMessage{rpc.Data, rpc.Raw} {
						if !json.Valid(raw) {
							t.Fatalf("redaction corrupted JSON: %s", raw)
						}
						// Scan every lexical token, including shadowed duplicate members.
						// A map decode would miss secrets hidden in overwritten values.
						decoder := json.NewDecoder(bytes.NewReader(raw))
						decoder.UseNumber()
						for {
							value, err := decoder.Token()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Fatal(err)
							}
							visible := fmt.Sprintf("%v", value)
							if strings.Contains(visible, securityToken) || strings.Contains(visible, url.QueryEscape(securityToken)) {
								t.Error("RPCError public JSON still contains reflected bearer token")
							}
						}
					}
					if strings.Contains(rpc.Message, securityToken) || strings.Contains(rpc.Message, url.QueryEscape(securityToken)) {
						t.Error("RPCError.Message still contains reflected bearer token")
					}
					var data, raw map[string]json.RawMessage
					if err = json.Unmarshal(rpc.Data, &data); err != nil {
						t.Fatal(err)
					}
					if err = json.Unmarshal(rpc.Raw, &raw); err != nil {
						t.Fatal(err)
					}
					if string(data["safeLarge"]) != "9007199254740993" || string(data["safeText"]) != `"retain me"` || string(raw["safeMax"]) != "9223372036854775807" || len(raw["future"]) == 0 {
						t.Fatal("redaction damaged unrelated native fields or exact numeric values")
					}
					if strings.HasPrefix(encoding, "duplicate-") {
						for _, duplicateObject := range []json.RawMessage{data["duplicates"], raw["future"]} {
							decoder := json.NewDecoder(bytes.NewReader(duplicateObject))
							count := 0
							for {
								_, err := decoder.Token()
								if err == io.EOF {
									break
								}
								if err != nil {
									t.Fatal(err)
								}
								count++
							}
							if count != 6 { // Object delimiters plus both key/value pairs.
								t.Error("redaction collapsed duplicate native members")
							}
						}
					}
					if encoding == "no-secret" && string(rpc.Raw) != rpcJSON {
						t.Error("unaffected native error Raw bytes were modified")
					}
					if c != nil {
						if err = c.Call(context.Background(), "synthetic/after-rpc-error", nil, nil); err != nil {
							t.Fatalf("native error unnecessarily terminated connection: %v", err)
						}
					}
				})
			}
		}
	}
}

func TestRemoteSecurityReflectedNumericDecodeErrorIsSafe(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", secure), func(t *testing.T) {
			// A legal all-numeric bearer token is also a syntactically legal JSON
			// number, but cannot fit the known protocol's int64 createdAt field.
			token := strings.Repeat("9", 400)
			_, options := securityServer(t, secure, func(ctx context.Context, ws *websocket.Conn, r *http.Request) error {
				envelope, err := securityRead(ctx, ws)
				if err != nil {
					return err
				}
				result := `{"thread":{"cliVersion":"synthetic","createdAt":` + token + `,"cwd":"/synthetic","ephemeral":true,"id":"synthetic-thread","modelProvider":"mock","preview":"","projectId":null,"sessionId":"synthetic-session","source":"appServer","status":{"type":"idle"},"turns":[],"updatedAt":1}}`
				if err = ws.Write(ctx, websocket.MessageText, securityResponse(envelope.ID, result)); err != nil {
					return err
				}
				return securityEcho(ctx, ws, r)
			})
			options.BearerToken = token
			c := securityOpen(t, options)
			_, err := c.ReadThread(context.Background(), types.ThreadReadParams{ThreadID: "synthetic-thread"})
			var call *codex.CallError
			if !errors.Is(err, codex.ErrProtocol) || !errors.As(err, &call) || call.Dispatch != codex.Responded {
				t.Fatalf("typed decode error lost protocol/response evidence: %v", err)
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(fmt.Sprintf("%+v", cause), token) {
					t.Error("public typed decode error or its cause disclosed reflected token")
				}
			}
			if err = c.Call(context.Background(), "synthetic/after-decode-error", nil, nil); err != nil {
				t.Fatalf("decode failure unnecessarily terminated connection: %v", err)
			}
		})
	}
}

func TestRemoteSecurityConstructionContext(t *testing.T) {
	t.Run("already-canceled-or-expired", func(t *testing.T) {
		var requests atomic.Int64
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
		t.Cleanup(s.Close)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer stop()
		for _, ctx := range []context.Context{nil, canceled, expired} {
			c, err := codex.NewRemote(ctx, securityOptions(securityEndpoint(s)))
			if c != nil || err == nil {
				t.Fatal("invalid construction context accepted")
			}
			if ctx != nil && !errors.Is(err, ctx.Err()) {
				t.Fatalf("context cause lost: %v", err)
			}
		}
		if requests.Load() != 0 {
			t.Fatal("pre-canceled context dialed the peer")
		}
	})
	for _, phase := range []string{"http-handshake", "initialize"} {
		for _, mode := range []string{"cancel", "deadline", "call-timeout"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				entered := make(chan struct{})
				left := make(chan struct{})
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(left)
					if phase == "http-handshake" {
						close(entered)
						select {
						case <-r.Context().Done():
						case <-time.After(3 * time.Second):
						}
						return
					}
					ws, err := websocket.Accept(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer ws.CloseNow()
					peerCtx, peerCancel := context.WithTimeout(r.Context(), 3*time.Second)
					defer peerCancel()
					envelope, err := securityRead(peerCtx, ws)
					if err != nil || envelope.Method != "initialize" {
						t.Errorf("stalled peer expected initialize: %v", err)
						return
					}
					close(entered)
					_, _, _ = ws.Read(peerCtx) // No initialization response is sent.
				}))
				t.Cleanup(s.Close)
				options := securityOptions(securityEndpoint(s))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := context.Canceled
				if mode == "deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
					defer cancel()
					want = context.DeadlineExceeded
				} else if mode == "call-timeout" {
					options.Limits.CallTimeout = 150 * time.Millisecond
					want = context.DeadlineExceeded
				} else {
					go func() {
						select {
						case <-entered:
							cancel()
						case <-ctx.Done():
						}
					}()
				}
				start := time.Now()
				c, err := codex.NewRemote(ctx, options)
				if c != nil {
					_ = c.Close()
					t.Fatal("stalled construction succeeded")
				}
				if !errors.Is(err, want) || time.Since(start) > 2*time.Second {
					t.Fatalf("construction was not promptly bounded by %v: %v", want, err)
				}
				select {
				case <-entered:
				default:
					t.Fatal("timeout did not exercise the intended network phase")
				}
				select {
				case <-left:
				case <-time.After(time.Second):
					t.Fatal("failed construction left a live peer connection")
				}
			})
		}
	}
	t.Run("cancel-after-construction", func(t *testing.T) {
		_, options := securityServer(t, false, securityEcho)
		ctx, cancel := context.WithCancel(context.Background())
		c, err := codex.NewRemote(ctx, options)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		for i := 0; i < 100; i++ {
			callCtx, callCancel := context.WithCancel(context.Background())
			err = c.Call(callCtx, "synthetic/still-open", nil, nil)
			callCancel()
			if err != nil {
				t.Fatalf("completed construction/call context closed the established connection at call %d: %v", i, err)
			}
		}
		if err = c.Call(context.Background(), "synthetic/after-all-cancellations", nil, nil); err != nil {
			t.Fatalf("canceling a completed call closed the shared transport: %v", err)
		}
	})
}

func TestRemoteSecurityStalledTLSHandshakeIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		close(accepted)
		_, _ = io.Copy(io.Discard, conn) // Accept TCP but never answer ClientHello.
	}()
	options := securityOptions("wss://" + listener.Addr().String())
	options.Limits.CallTimeout = 150 * time.Millisecond
	start := time.Now()
	c, err := codex.NewRemote(context.Background(), options)
	if c != nil || err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("stalled TLS was not bounded: %v", err)
	}
	securityNoSecret(t, err)
	if !errors.Is(err, codex.ErrRemoteTransport) {
		t.Fatalf("unexpected TLS timeout classification: %v", err)
	}
	select {
	case <-accepted:
	default:
		t.Fatal("test did not reach TLS handshake")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("TLS handshake failure did not close socket")
	}
}

func TestRemoteSecurityMalformedUpgradeBodyCannotOutliveConstruction(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, mode := range []string{"cancel", "deadline", "call-timeout"} {
			t.Run(fmt.Sprintf("tls=%v/%s", secure, mode), func(t *testing.T) {
				entered := make(chan struct{})
				release := make(chan struct{})
				s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, buffer, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					// A rejected HTTP101 body is a live upgraded socket. The peer
					// deliberately keeps it open while the websocket dialer cleans up.
					_, err = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", r.Header.Get("Authorization"))
					if err == nil {
						err = buffer.Flush()
					}
					if err != nil {
						t.Error(err)
						return
					}
					close(entered)
					<-release
				}))
				if secure {
					s.StartTLS()
				} else {
					s.Start()
				}
				t.Cleanup(s.Close)
				defer close(release)
				options := securityOptions(securityEndpoint(s))
				if secure {
					roots := x509.NewCertPool()
					roots.AddCert(s.Certificate())
					options.TLSConfig = &tls.Config{RootCAs: roots}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := context.Canceled
				if mode == "call-timeout" {
					options.Limits.CallTimeout = 150 * time.Millisecond
					want = context.DeadlineExceeded
				} else if mode == "deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
					defer cancel()
					want = context.DeadlineExceeded
				} else {
					go func() {
						select {
						case <-entered:
							cancel()
						case <-ctx.Done():
						}
					}()
				}
				start := time.Now()
				c, err := codex.NewRemote(ctx, options)
				if c != nil {
					_ = c.Close()
					t.Fatal("malformed upgrade was accepted")
				}
				securityNoSecret(t, err)
				if !errors.Is(err, want) || !errors.Is(err, codex.ErrRemoteTransport) || time.Since(start) > time.Second {
					t.Fatalf("held-open rejected upgrade exceeded construction bound or lost cause: %v", err)
				}
				select {
				case <-entered:
				default:
					t.Fatal("test did not reach rejected upgrade-body cleanup")
				}
			})
		}
	}
}

type securityCaptureWriter struct {
	http.ResponseWriter
	conn net.Conn
}

func (w *securityCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := w.ResponseWriter.(http.Hijacker).Hijack()
	w.conn = conn
	return conn, buffer, err
}

func TestRemoteSecurityMalformedRawWebSocketFrames(t *testing.T) {
	// These payloads are valid native notifications after framing is decoded.
	// Rejection must therefore come from the wire header, not invalid JSON.
	notification := []byte(`{"method":"synthetic/raw-notification","params":{"value":1}}`)
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	masked := append([]byte{0x81, 0x80 | byte(len(notification))}, mask[:]...)
	for i, b := range notification {
		masked = append(masked, b^mask[i%len(mask)])
	}
	nonminimal126 := append([]byte{0x81, 126, 0, byte(len(notification))}, notification...)
	nonminimal127 := append([]byte{0x81, 127, 0, 0, 0, 0, 0, 0, 0, byte(len(notification))}, notification...)
	cases := []struct {
		name  string
		frame []byte
	}{
		{"reserved-opcode", []byte{0x83, 0x00}},
		{"unexpected-continuation", []byte{0x80, 0x00}},
		{"unnegotiated-rsv1", []byte{0xc1, 0x00}},
		{"reserved-rsv2", []byte{0xa1, 0x00}},
		{"fragmented-ping", []byte{0x09, 0x00}},
		{"oversized-ping", append([]byte{0x89, 126, 0, 126}, make([]byte, 126)...)},
		{"masked-server-frame", masked},
		{"nonminimal-126-length", nonminimal126},
		{"nonminimal-127-length", nonminimal127},
		{"invalid-utf8", []byte{0x81, 1, 0xff}},
		{"invalid-close-code", []byte{0x88, 2, 0, 1}},
		{"invalid-utf8-close-reason", []byte{0x88, 3, 3, 232, 0xff}},
		{"one-byte-close-payload", []byte{0x88, 1, 0}},
	}
	for _, secure := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("tls=%v/%s", secure, tc.name), func(t *testing.T) {
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
					envelope, err := securityRead(ctx, ws)
					if err != nil || envelope.Method != "initialize" {
						t.Errorf("expected initialize: %v", err)
						return
					}
					if err = ws.Write(ctx, websocket.MessageText, securityResponse(envelope.ID, securityInitResult)); err != nil {
						t.Error(err)
						return
					}
					envelope, err = securityRead(ctx, ws)
					if err != nil || envelope.Method != "initialized" {
						t.Errorf("expected initialized: %v", err)
						return
					}
					if _, err = securityRead(ctx, ws); err != nil {
						t.Error(err)
						return
					}
					// Use the captured server side of the real upgraded TCP/TLS
					// socket to inject frames a conforming library will not emit.
					_ = capture.conn.SetWriteDeadline(time.Now().Add(time.Second))
					if _, err = capture.conn.Write(tc.frame); err != nil {
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
				err := c.Call(context.Background(), "synthetic/invalid-wire", nil, nil)
				if !errors.Is(err, codex.ErrProtocol) {
					t.Fatalf("malformed raw frame must terminate as protocol failure: %v", err)
				}
				select {
				case <-c.Done():
				case <-time.After(time.Second):
					t.Fatal("malformed raw frame did not terminate the client")
				}
				if !errors.Is(c.Err(), codex.ErrProtocol) {
					t.Fatalf("terminal frame error lost protocol classification: %v", c.Err())
				}
			})
		}
	}
}

func TestRemoteSecurityWebSocketMessageFraming(t *testing.T) {
	for _, mode := range []string{"pretty-json", "fragmented-with-ping", "exact-limit", "two-objects", "split-messages", "binary", "oversized", "fragmented-oversized", "one-over-limit"} {
		t.Run(mode, func(t *testing.T) {
			pingResult := make(chan error, 1)
			_, options := securityServer(t, false, func(ctx context.Context, ws *websocket.Conn, _ *http.Request) error {
				envelope, err := securityRead(ctx, ws)
				if err != nil {
					return err
				}
				payload := securityResponse(envelope.ID, `{"ok":true}`)
				switch mode {
				case "pretty-json":
					var pretty bytes.Buffer
					if err = json.Indent(&pretty, payload, "", "  "); err != nil {
						return err
					}
					err = ws.Write(ctx, websocket.MessageText, pretty.Bytes())
				case "fragmented-with-ping":
					// Writer.Write emits nonfinal frames; Close emits final continuation.
					// A ping between fragments proves control frames do not delimit JSON.
					closed := ws.CloseRead(ctx)
					writer, writeErr := ws.Writer(ctx, websocket.MessageText)
					if writeErr != nil {
						return writeErr
					}
					if _, err = writer.Write(payload[:len(payload)/2]); err != nil {
						return err
					}
					err = ws.Ping(ctx)
					pingResult <- err
					if err != nil {
						return err
					}
					if _, err = writer.Write(payload[len(payload)/2:]); err != nil {
						return err
					}
					if err = writer.Close(); err != nil {
						return err
					}
					<-closed.Done()
					return nil
				case "two-objects":
					err = ws.Write(ctx, websocket.MessageText, append(append(payload, '\n'), []byte(`{"method":"extra"}`)...))
				case "split-messages":
					if err = ws.Write(ctx, websocket.MessageText, payload[:len(payload)/2]); err == nil {
						_ = ws.Write(ctx, websocket.MessageText, payload[len(payload)/2:])
					}
				case "binary":
					err = ws.Write(ctx, websocket.MessageBinary, payload)
				case "exact-limit", "one-over-limit":
					size := 256
					if mode == "one-over-limit" {
						size++
					}
					empty := securityResponse(envelope.ID, `{"ok":true,"padding":""}`)
					payload = securityResponse(envelope.ID, `{"ok":true,"padding":"`+strings.Repeat("x", size-len(empty))+`"}`)
					if len(payload) != size {
						return errors.New("boundary fixture has wrong payload size")
					}
					err = ws.Write(ctx, websocket.MessageText, payload)
				case "oversized", "fragmented-oversized":
					payload = securityResponse(envelope.ID, `"`+strings.Repeat("x", 300)+`"`)
					if mode == "oversized" {
						err = ws.Write(ctx, websocket.MessageText, payload)
					} else {
						writer, writeErr := ws.Writer(ctx, websocket.MessageText)
						if writeErr != nil {
							return writeErr
						}
						// Each fragment is below the 256-byte bound; the message is not.
						_, err = writer.Write(payload[:160])
						if err == nil {
							_, err = writer.Write(payload[160:])
						}
						_ = writer.Close()
					}
				}
				if err != nil && (mode == "pretty-json" || mode == "fragmented-with-ping" || mode == "exact-limit") {
					return err
				}
				_, _, _ = ws.Read(ctx) // Wait for client cleanup; don't race a valid reply.
				return nil
			})
			options.Limits.MaxFrameBytes = 256
			c := securityOpen(t, options)
			var result struct {
				OK bool `json:"ok"`
			}
			err := c.Call(context.Background(), "synthetic/framing", nil, &result)
			valid := mode == "pretty-json" || mode == "fragmented-with-ping" || mode == "exact-limit"
			if valid {
				if err != nil || !result.OK {
					t.Fatalf("valid WebSocket message rejected: result=%+v error=%v", result, err)
				}
				if mode == "fragmented-with-ping" {
					select {
					case err := <-pingResult:
						if err != nil {
							t.Fatalf("ping was not answered with pong: %v", err)
						}
					case <-time.After(time.Second):
						t.Fatal("peer did not observe pong")
					}
				}
				return
			}
			if !errors.Is(err, codex.ErrProtocol) {
				t.Fatalf("invalid message was not a protocol failure: %v", err)
			}
			if (strings.Contains(mode, "oversized") || mode == "one-over-limit") && !errors.Is(err, codex.ErrFrameTooLarge) {
				t.Fatalf("message overflow lost size classification: %v", err)
			}
			select {
			case <-c.Done():
			case <-time.After(time.Second):
				t.Fatal("invalid message did not terminate the connection")
			}
			if !errors.Is(c.Err(), codex.ErrProtocol) {
				t.Fatalf("terminal error lost protocol classification: %v", c.Err())
			}
		})
	}
}
