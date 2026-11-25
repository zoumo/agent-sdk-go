package codex_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

type blockedRemotePeer struct {
	client                *codex.Client
	session               *codex.Session
	prefix, release, done chan struct{}
	releaseOnce           sync.Once
	requests              atomic.Int64
	answers               atomic.Int64
	lastAnswerID          atomic.Value
}

// This peer pauses after an actual message payload byte. Payloads exceed socket
// buffering; the SDK's write is released only by an explicit peer barrier, local
// context cancellation, its write deadline, or Close. No private SDK state used.
func openBlockedRemote(t *testing.T, limits codex.Limits, resolveID string) *blockedRemotePeer {
	reportPeerError := collectTransportPeerErrors(t)

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
			return ws.Write(context.Background(), websocket.MessageText, fmt.Appendf(nil, `{"id":%s,"result":%s}`, id, result))
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
		start, err := read()
		if err != nil || start.Method != "thread/start" {
			return
		}
		if send(start.ID, startResult) != nil {
			return
		}
		for i := range 4 {
			if ws.Write(context.Background(), websocket.MessageText, []byte(requestFrame(strconv.Itoa(100+i), "future/approval", `{"threadId":"native-thread-42"}`))) != nil {
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
		if resolveID != "" {
			frame := fmt.Sprintf(`{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":%s}}`, resolveID)
			if err := ws.Write(context.Background(), websocket.MessageText, []byte(frame)); err != nil {
				reportPeerError("native resolution: %v", err)
				return
			}
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
				p.lastAnswerID.Store(string(m.ID))
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
		require.FailNow(t, fmt.Sprint(err))
	}
	t.Cleanup(func() {
		p.releaseOnce.Do(func() { close(p.release) })
		_ = p.client.Close()
		server.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			reportPeerError("peer connection did not close")
		}
	})
	p.session, err = codex.Open(context.Background(), codex.SessionOptions{Client: p.client})
	require.NoError(t, err)
	err = p.client.Call(context.Background(), "gate", nil, nil)
	require.NoError(t, err)
	return p
}

func awaitPrefix(t *testing.T, p *blockedRemotePeer) {
	t.Helper()
	select {
	case <-p.prefix:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "remote peer never observed message byte")
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
							p := openBlockedRemote(t, codex.Limits{WriteTimeout: timeout, CallTimeout: 10 * time.Second}, "")
							request := nextOwnedRequest(t, fixture{c: p.client, session: p.session})
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
								{
									err := request.Handle().Cancel()
									require.ErrorIs(t, err, codex.ErrRequestAnswered, "admitted answer cancel = %v", err)
								}
							}
							cause := codex.ErrWriteTimeout
							switch stop {
							case "cancel":
								cause = context.Canceled
								cancel()
							case "close":
								cause = codex.ErrClosed
								{
									err := p.client.Close()
									require.NoError(t, err)
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
								require.FailNow(t, "blocked writer survived terminal action")
							}
							select {
							case <-p.client.Done():
							case <-time.After(time.Second):
								require.FailNow(t, "ambiguous blocked writer retained connection")
							}
							if operation == "answer" {
								answerError(t, request.Handle().Answer(context.Background(), "retry"), request, codex.NotSent, nil)
							}
							{
								err := p.client.Close()
								require.NoError(t, err)
							}
							p.releaseOnce.Do(func() { close(p.release) })
							<-p.done
							require.False(t, p.answers.Load() != 0 || p.requests.Load() != 0, "partial frame completed or replayed")
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
			p := openBlockedRemote(t, codex.Limits{OutboundQueue: 1, WriteTimeout: 15 * time.Second, CallTimeout: 20 * time.Second}, "")
			requests := make([]codex.Request, 4)
			for i := range requests {
				requests[i] = nextOwnedRequest(t, fixture{c: p.client, session: p.session})
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
					require.NoError(t, result.r.Handle().Err(), "pre-admission failure consumed handle")
					rejected = append(rejected, result.r)
				case <-time.After(2 * time.Second):
					require.FailNow(t, "full queue blocked admission")
				}
			}
			cancel()
			select {
			case result := <-results:
				answerError(t, result.err, result.r, codex.NotSent, context.Canceled)
				require.NoError(t, result.r.Handle().Err())
				rejected = append(rejected, result.r)
			case <-time.After(time.Second):
				require.FailNow(t, "queued answer did not cancel")
			}
			p.releaseOnce.Do(func() { close(p.release) })
			select {
			case err := <-writer:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				require.FailNow(t, "released writer did not complete")
			}
			for _, r := range rejected {
				{
					err := r.Handle().Answer(context.Background(), "explicit retry after admission failure")
					require.NoError(t, err)
				}
			}
			{
				err := p.client.Call(context.Background(), "barrier", nil, nil)
				require.NoError(t, err)
			}
			require.False(t, p.answers.Load() != 4 || p.requests.Load() != 2, "requests=%d answers=%d", p.requests.Load(), p.answers.Load())
			require.NoError(t, p.client.Err(), "prewrite cancellation terminated connection")
		})
	}
}

func TestRemoteCloseLeavesListenerAvailable(t *testing.T) {
	reportPeerError := collectTransportPeerErrors(t)

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
				if ws.Write(context.Background(), websocket.MessageText, fmt.Appendf(nil, `{"id":%s,"result":%s}`, m.ID, initResult)) != nil {
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
			for range 2 {
				c, err := codex.NewRemote(context.Background(), options)
				require.NoError(t, err)
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						{
							err := c.Close()
							if err != nil {
								reportPeerError("%v", err)
							}
						}
					})
				}
				wg.Wait()
			}
			require.EqualValues(t, 2, connections.Load(), "unexpected connection count: %d", connections.Load())
		})
	}
}

// The peer has consumed a payload byte but holds the remainder beyond socket
// capacity. Native resolution must retain ambiguity, even when the same single
// response finishes after release. A zero-frame NotSent assertion is wrong here.
func TestRemoteNativeResolutionDuringAnswerWrite(t *testing.T) {
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			p := openBlockedRemote(t, codex.Limits{WriteTimeout: 15 * time.Second, CallTimeout: 20 * time.Second}, "100")
			r := sessionRequest(t, p.session).Request
			require.Equal(t, "100", r.ID().String())
			result := make(chan error, 1)
			go func() { result <- r.Handle().Answer(context.Background(), strings.Repeat("x", 16<<20)) }()
			awaitPrefix(t, p)
			select {
			case err := <-result:
				answerError(t, err, r, codex.OutcomeUnknown, codex.ErrRequestResolved)
			case <-time.After(3 * time.Second):
				require.FailNow(t, "native resolution did not wake blocked answer")
			}
			awaitAnswer(t, r.Handle(), codex.ErrRequestResolved)
			answerError(t, r.Handle().Answer(context.Background(), "retry"), r, codex.NotSent, codex.ErrRequestResolved)
			require.NoError(t, p.client.Err())
			p.releaseOnce.Do(func() { close(p.release) })
			var drained string
			require.NoError(t, p.client.Call(context.Background(), "echo", nil, &drained))
			require.Equal(t, "drained", drained)
			require.Equal(t, int64(1), p.answers.Load(), "one original response, no retry")
			require.Equal(t, "100", p.lastAnswerID.Load())
			require.ErrorIs(t, r.Handle().Err(), codex.ErrRequestResolved, "later write completion must not regrant authority")
		})
	}
}

// A response can settle a call while its WebSocket message is still blocked in
// the writer. The call context is then done, but it must not cancel that write.
func TestRemoteResponseBeforeWriteReturnSurvivesCancellation(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", secure), func(t *testing.T) {
			prefixSeen := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

			_, options := securityServer(t, secure, func(ctx context.Context, ws *websocket.Conn, _ *http.Request) error {
				ws.SetReadLimit(32 << 20)
				_, reader, err := ws.Reader(ctx)
				if err != nil {
					return err
				}
				prefix := make([]byte, 128)
				if _, err = io.ReadFull(reader, prefix); err != nil {
					return err
				}
				id, err := remotePrefixID(prefix)
				if err != nil {
					return err
				}
				writeErr := ws.Write(ctx, websocket.MessageText, securityResponse(id, `"early"`))
				if writeErr != nil {
					return writeErr
				}
				close(prefixSeen)
				<-release
				if _, err = io.ReadAll(reader); err != nil {
					return err
				}
				barrier, err := securityRead(ctx, ws)
				if err != nil {
					return err
				}
				if barrier.Method != "synthetic/barrier" {
					return fmt.Errorf("expected barrier, got %q", barrier.Method)
				}
				return ws.Write(ctx, websocket.MessageText, securityResponse(barrier.ID, `"connected"`))
			})
			options.Limits.MaxFrameBytes = 32 << 20
			options.Limits.WriteTimeout = 10 * time.Second
			c := securityOpen(t, options)

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				var value string
				err := c.Call(ctx, "synthetic/early", strings.Repeat("x", 16<<20), &value)
				if err == nil && value != "early" {
					err = fmt.Errorf("unexpected result %q", value)
				}
				result <- err
			}()
			select {
			case <-prefixSeen:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "remote peer never observed the blocked message")
			}
			select {
			case err := <-result:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "response did not settle the call before Write returned")
			}
			cancel()
			select {
			case <-c.Done():
				require.FailNow(t, "successful call cancellation closed the WebSocket", "err=%v", c.Err())
			case <-time.After(100 * time.Millisecond):
			}
			require.NoError(t, c.Err())

			releaseOnce.Do(func() { close(release) })
			var barrier string
			require.NoError(t, c.Call(context.Background(), "synthetic/barrier", nil, &barrier))
			require.Equal(t, "connected", barrier)
		})
	}
}

func remotePrefixID(prefix []byte) (json.RawMessage, error) {
	const key = `"id":`
	start := bytes.Index(prefix, []byte(key))
	if start < 0 {
		return nil, errors.New("message prefix has no request ID")
	}
	start += len(key)
	end := start
	for end < len(prefix) && (prefix[end] == '-' || prefix[end] >= '0' && prefix[end] <= '9') {
		end++
	}
	if end == start {
		return nil, errors.New("message prefix has no numeric request ID")
	}
	id := append(json.RawMessage(nil), prefix[start:end]...)
	if !json.Valid(id) {
		return nil, errors.New("message prefix has an invalid request ID")
	}
	return id, nil
}
