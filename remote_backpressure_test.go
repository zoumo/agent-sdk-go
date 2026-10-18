package codex_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

type blockedRemotePeer struct {
	client                *codex.Client
	prefix, release, done chan struct{}
	releaseOnce           sync.Once
	requests              atomic.Int64
	answers               atomic.Int64
}

// This peer pauses after an actual message payload byte. Payloads exceed socket
// buffering; the SDK's write is released only by an explicit peer barrier, local
// context cancellation, its write deadline, or Close. No private SDK state used.
func openBlockedRemote(t *testing.T, limits codex.Limits) *blockedRemotePeer {
	t.Helper()
	p := &blockedRemotePeer{prefix: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer close(p.done)
		defer ws.CloseNow()
		ws.SetReadLimit(32 << 20)
		read := func() (fakeEnvelope, error) {
			_, raw, err := ws.Read(context.Background())
			var m fakeEnvelope
			if err == nil {
				err = json.Unmarshal(raw, &m)
			}
			return m, err
		}
		send := func(id json.RawMessage, result string) error {
			return ws.Write(context.Background(), websocket.MessageText, []byte(fmt.Sprintf(`{"id":%s,"result":%s}`, id, result)))
		}
		init, err := read()
		if err != nil || init.Method != "initialize" {
			return
		}
		if send(init.ID, initResult) != nil {
			return
		}
		initialized, err := read()
		if err != nil || initialized.Method != "initialized" {
			return
		}
		for i := 0; i < 4; i++ {
			if ws.Write(context.Background(), websocket.MessageText, []byte(requestFrame(fmt.Sprint(100+i), "future/approval", `{}`))) != nil {
				return
			}
		}
		gate, err := read()
		if err != nil || gate.Method != "gate" {
			return
		}
		if send(gate.ID, `{}`) != nil {
			return
		}
		_, reader, err := ws.Reader(context.Background())
		if err != nil {
			return
		}
		first := make([]byte, 1)
		if _, err = io.ReadFull(reader, first); err != nil {
			return
		}
		close(p.prefix)
		<-p.release
		rest, err := io.ReadAll(reader)
		if err != nil {
			return
		}
		var firstMessage fakeEnvelope
		if json.Unmarshal(append(first, rest...), &firstMessage) != nil {
			return
		}
		account := func(m fakeEnvelope) error {
			if m.Method == "" {
				p.answers.Add(1)
				return nil
			}
			p.requests.Add(1)
			return send(m.ID, `"drained"`)
		}
		if account(firstMessage) != nil {
			return
		}
		for {
			m, err := read()
			if err != nil {
				return
			}
			if account(m) != nil {
				return
			}
		}
	}))
	options := codex.RemoteOptions{BearerToken: "synthetic-block-token", Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "backpressure-test", Version: "1"}}, Limits: limits}
	options.Limits.MaxFrameBytes = 32 << 20
	if remoteVariant(t) == "wss" {
		server.StartTLS()
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		options.TLSConfig = &tls.Config{RootCAs: roots}
	} else {
		server.Start()
	}
	options.URL = "ws" + strings.TrimPrefix(server.URL, "http")
	var err error
	p.client, err = codex.NewRemote(context.Background(), options)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.releaseOnce.Do(func() { close(p.release) })
		_ = p.client.Close()
		server.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			t.Error("peer connection did not close")
		}
	})
	if err = p.client.Call(context.Background(), "gate", nil, nil); err != nil {
		t.Fatal(err)
	}
	return p
}
func awaitPrefix(t *testing.T, p *blockedRemotePeer) {
	t.Helper()
	select {
	case <-p.prefix:
	case <-time.After(5 * time.Second):
		t.Fatal("remote peer never observed message byte")
	}
}

func TestRemoteBlockedWritesAndAnswers(t *testing.T) {
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, operation := range []string{"call", "answer"} {
				t.Run(operation, func(t *testing.T) {
					for _, stop := range []string{"cancel", "timeout", "close"} {
						t.Run(stop, func(t *testing.T) {
							timeout := 5 * time.Second
							if stop == "timeout" {
								timeout = 500 * time.Millisecond
							}
							p := openBlockedRemote(t, codex.Limits{WriteTimeout: timeout, CallTimeout: 10 * time.Second})
							request := nextRequest(t, p.client)
							ctx, cancel := context.WithCancel(context.Background())
							defer cancel()
							result := make(chan error, 1)
							payload := strings.Repeat("x", 16<<20)
							if operation == "answer" {
								go func() { result <- request.Handle().Answer(ctx, payload) }()
							} else {
								go func() { result <- p.client.Call(ctx, "echo", payload, nil) }()
							}
							awaitPrefix(t, p)
							if operation == "answer" {
								if err := request.Handle().Cancel(); !errors.Is(err, codex.ErrRequestAnswered) {
									t.Fatalf("admitted answer cancel = %v", err)
								}
							}
							cause := codex.ErrWriteTimeout
							switch stop {
							case "cancel":
								cause = context.Canceled
								cancel()
							case "close":
								cause = codex.ErrClosed
								if err := p.client.Close(); err != nil {
									t.Fatal(err)
								}
							}
							select {
							case err := <-result:
								if operation == "answer" {
									answerError(t, err, request, codex.OutcomeUnknown, cause)
								} else {
									callError(t, err, codex.OutcomeUnknown, cause)
								}
							case <-time.After(3 * time.Second):
								t.Fatal("blocked writer survived terminal action")
							}
							select {
							case <-p.client.Done():
							case <-time.After(time.Second):
								t.Fatal("ambiguous blocked writer retained connection")
							}
							if operation == "answer" {
								answerError(t, request.Handle().Answer(context.Background(), "retry"), request, codex.NotSent, nil)
							}
							if err := p.client.Close(); err != nil {
								t.Fatal(err)
							}
							p.releaseOnce.Do(func() { close(p.release) })
							<-p.done
							if p.answers.Load() != 0 || p.requests.Load() != 0 {
								t.Fatal("partial frame completed or replayed")
							}
						})
					}
				})
			}
		})
	}
}

func TestRemoteQueuedAnswerAdmissionAndCancellation(t *testing.T) {
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			p := openBlockedRemote(t, codex.Limits{OutboundQueue: 1, WriteTimeout: 15 * time.Second, CallTimeout: 20 * time.Second})
			requests := make([]codex.Request, 4)
			for i := range requests {
				requests[i] = nextRequest(t, p.client)
			}
			writer := make(chan error, 1)
			go func() { writer <- p.client.Call(context.Background(), "echo", strings.Repeat("x", 16<<20), nil) }()
			awaitPrefix(t, p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				r   codex.Request
				err error
			}
			results := make(chan outcome, 4)
			for _, r := range requests {
				go func() { results <- outcome{r, r.Handle().Answer(ctx, "choice")} }()
			}
			rejected := make([]codex.Request, 0, 3)
			for range 3 {
				select {
				case result := <-results:
					answerError(t, result.err, result.r, codex.NotSent, codex.ErrLimit)
					if result.r.Handle().Err() != nil {
						t.Fatal("pre-admission failure consumed handle")
					}
					rejected = append(rejected, result.r)
				case <-time.After(2 * time.Second):
					t.Fatal("full queue blocked admission")
				}
			}
			cancel()
			select {
			case result := <-results:
				answerError(t, result.err, result.r, codex.NotSent, context.Canceled)
				answerError(t, result.r.Handle().Answer(context.Background(), "retry"), result.r, codex.NotSent, context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("queued answer did not cancel")
			}
			p.releaseOnce.Do(func() { close(p.release) })
			select {
			case err := <-writer:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("released writer did not complete")
			}
			for _, r := range rejected {
				if err := r.Handle().Answer(context.Background(), "explicit retry after admission failure"); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.client.Call(context.Background(), "barrier", nil, nil); err != nil {
				t.Fatal(err)
			}
			if p.answers.Load() != 3 || p.requests.Load() != 2 {
				t.Fatalf("requests=%d answers=%d", p.requests.Load(), p.answers.Load())
			}
			if p.client.Err() != nil {
				t.Fatal("prewrite cancellation terminated connection")
			}
		})
	}
}

func TestRemoteCloseLeavesListenerAvailable(t *testing.T) {
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			var connections atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer ws.CloseNow()
				connections.Add(1)
				_, raw, err := ws.Read(context.Background())
				if err != nil {
					return
				}
				var m fakeEnvelope
				if json.Unmarshal(raw, &m) != nil {
					return
				}
				if ws.Write(context.Background(), websocket.MessageText, []byte(fmt.Sprintf(`{"id":%s,"result":%s}`, m.ID, initResult))) != nil {
					return
				}
				for {
					if _, _, err := ws.Read(context.Background()); err != nil {
						return
					}
				}
			}))
			options := codex.RemoteOptions{BearerToken: "synthetic-reconnect-token", Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "independent-connections", Version: "1"}}}
			if transport == "wss" {
				server.StartTLS()
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				options.TLSConfig = &tls.Config{RootCAs: roots}
			} else {
				server.Start()
			}
			defer server.Close()
			options.URL = "ws" + strings.TrimPrefix(server.URL, "http")
			for i := 0; i < 2; i++ {
				c, err := codex.NewRemote(context.Background(), options)
				if err != nil {
					t.Fatal(err)
				}
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						if err := c.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				wg.Wait()
			}
			if connections.Load() != 2 {
				t.Fatalf("unexpected connection count: %d", connections.Load())
			}
		})
	}
}
