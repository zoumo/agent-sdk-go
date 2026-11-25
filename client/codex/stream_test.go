package codex_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

type streamPeer struct {
	reader      *io.PipeReader
	writer      *io.PipeWriter
	writeEvents chan struct{}
	wait        chan error
	waitStarted chan struct{}
	transport   codex.StreamTransport
}

func newStreamPeer() *streamPeer {
	peerInput, sdkOutput := io.Pipe()
	sdkInput, peerOutput := io.Pipe()
	peer := &streamPeer{
		reader:      peerInput,
		writer:      peerOutput,
		writeEvents: make(chan struct{}, 32),
		wait:        make(chan error, 1),
		waitStarted: make(chan struct{}),
	}
	peer.transport = codex.StreamTransport{
		Reader: sdkInput,
		Writer: &observedWriteCloser{WriteCloser: sdkOutput, started: peer.writeEvents},
		Wait:   peer.waitForResult,
	}
	return peer
}

func (p *streamPeer) waitForResult(ctx context.Context) error {
	select {
	case <-p.waitStarted:
	default:
		close(p.waitStarted)
	}
	select {
	case err := <-p.wait:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestStreamClientCompletesAfterInputHalfClose(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexStream(peer, true)
	}()

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Args:       []string{"app-server", "--listen", "stdio://"},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close(); _ = peer.reader.Close(); _ = peer.writer.Close() })

	require.NoError(t, client.CloseInput())
	require.ErrorIs(t, client.CloseInput(), codex.ErrInputClosed)
	require.NoError(t, <-peerDone)
	peer.wait <- nil

	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream client did not finish after both directions closed")
	}
	require.NoError(t, client.Err())
	require.NoError(t, client.Close())
	require.Equal(t, uint64(1), client.Diagnostics().Notifications)
}

func TestStreamClientCloseInputPreservesCloseError(t *testing.T) {
	peer := newStreamPeer()
	closeErr := errors.New("synthetic writer close failure")
	writer := &closeErrorWriter{WriteCloser: peer.transport.Writer, err: closeErr}
	peer.transport.Writer = writer
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexStream(peer, true)
	}()

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close(); _ = peer.reader.Close(); _ = peer.writer.Close() })
	peer.wait <- nil

	require.ErrorIs(t, client.CloseInput(), closeErr)
	require.ErrorIs(t, client.CloseInput(), closeErr)
	require.EqualValues(t, 1, writer.calls.Load())
	require.NoError(t, <-peerDone)
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream client did not finish after a writer close error")
	}
	require.NoError(t, client.Err())
	require.NoError(t, client.Close())
	require.Equal(t, uint64(1), client.Diagnostics().Notifications)
}

func TestStreamSessionCloseToleratesCompletedInputHalfClose(t *testing.T) {
	peer := newStreamPeer()
	peerReady := make(chan struct{})
	inputEOF := make(chan struct{})
	finishPeer := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(peer.reader)
		initialize, err := readCodexFrame(reader)
		if err != nil {
			peerDone <- err
			return
		}
		if err := writeCodexResponse(peer.writer, initialize["id"]); err != nil {
			peerDone <- err
			return
		}
		if _, err := readCodexFrame(reader); err != nil {
			peerDone <- err
			return
		}
		start, err := readCodexFrame(reader)
		if err != nil {
			peerDone <- err
			return
		}
		if _, err := io.WriteString(peer.writer, `{"id":`+string(start["id"])+`,"result":`+startResult+`}`+"\n"); err != nil {
			peerDone <- err
			return
		}
		close(peerReady)
		if _, err := io.Copy(io.Discard, reader); err != nil {
			peerDone <- err
			return
		}
		close(inputEOF)
		<-finishPeer
		peerDone <- peer.writer.Close()
	}()
	peer.wait <- nil

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Args:       []string{"app-server", "--listen", "stdio://"},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close(); _ = peer.reader.Close(); _ = peer.writer.Close() })
	session, err := codex.Open(t.Context(), codex.SessionOptions{Client: client})
	require.NoError(t, err)
	<-peerReady

	require.NoError(t, client.CloseInput())
	require.NoError(t, session.Close())
	select {
	case <-inputEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not observe SDK input EOF")
	}
	_, err = codex.Open(t.Context(), codex.SessionOptions{
		Client: client,
		Resume: &types.ThreadResumeParams{ThreadID: "native-thread-42"},
	})
	require.ErrorIs(t, err, codex.ErrOwnershipConflict)
	close(finishPeer)
	require.NoError(t, <-peerDone)
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream client did not finish after session cleanup")
	}
	require.NoError(t, client.Err())
	require.NoError(t, client.Close())
}

func TestStreamClientRejectsUnexpectedPeerEOF(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexHandshake(peer)
	}()

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	require.NoError(t, <-peerDone)
	require.NoError(t, peer.writer.Close())
	peer.wait <- nil
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("unexpected peer EOF did not terminate the stream client")
	}
	require.ErrorIs(t, client.Err(), codex.ErrUnexpectedEOF)
	require.ErrorIs(t, client.Close(), codex.ErrUnexpectedEOF)
}

func TestStreamClientRejectsUnterminatedPeerFrame(t *testing.T) {
	for _, halfClose := range []bool{false, true} {
		name := "before input half-close"
		if halfClose {
			name = "after input half-close"
		}
		t.Run(name, func(t *testing.T) {
			peer := newStreamPeer()
			peer.transport.Wait = nil
			handshakeDone := make(chan error, 1)
			sendFrame := make(chan struct{})
			peerDone := make(chan error, 1)
			go func() {
				if err := serveCodexHandshake(peer); err != nil {
					handshakeDone <- err
					return
				}
				handshakeDone <- nil
				<-sendFrame
				if _, err := io.WriteString(peer.writer, "{\"method\":\"stream/prelude\"}\n{\"method\":\"stream/final\""); err != nil {
					peerDone <- err
					return
				}
				peerDone <- peer.writer.Close()
			}()

			client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
				Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
			}, peer.transport)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close(); _ = peer.reader.Close(); _ = peer.writer.Close() })
			require.NoError(t, <-handshakeDone)
			if halfClose {
				require.NoError(t, client.CloseInput())
			}
			close(sendFrame)
			require.NoError(t, <-peerDone)

			select {
			case <-client.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("unterminated peer frame did not terminate the stream client")
			}
			require.ErrorIs(t, client.Err(), codex.ErrProtocol)
			require.ErrorContains(t, client.Err(), "unterminated frame")
			require.ErrorIs(t, client.Close(), codex.ErrProtocol)
			require.Equal(t, uint64(1), client.Diagnostics().Notifications)
		})
	}
}

func TestStreamClientWaitFailureReplacesUnexpectedPeerEOF(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexHandshake(peer)
	}()

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	require.NoError(t, <-peerDone)
	require.NoError(t, peer.writer.Close())
	peerErr := errors.New("authoritative replay failure")
	peer.wait <- peerErr
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("peer completion failure did not terminate the stream client")
	}
	require.ErrorIs(t, client.Close(), peerErr)
	require.ErrorIs(t, client.Err(), peerErr)
}

func TestStreamClientPreservesPeerWaitFailure(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexStream(peer, true)
	}()
	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	require.NoError(t, client.CloseInput())
	require.NoError(t, <-peerDone)
	peerErr := errors.New("synthetic peer completion failure")
	peer.wait <- peerErr

	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("peer completion failure did not terminate the stream client")
	}
	require.ErrorIs(t, client.Err(), peerErr)
	require.ErrorIs(t, client.Close(), peerErr)
}

func TestStreamClientClosePreservesPeerWaitFailure(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- serveCodexStream(peer, true)
	}()
	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	require.NoError(t, <-peerDone)
	select {
	case <-peer.waitStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for the peer result")
	}
	peerErr := errors.New("synthetic peer completion failure during close")
	peer.wait <- peerErr
	require.ErrorIs(t, <-closed, peerErr)
	require.ErrorIs(t, client.Err(), peerErr)
}

func TestStreamClientHalfCloseInterruptsBlockedWrite(t *testing.T) {
	peer := newStreamPeer()
	started := make(chan struct{})
	releasePeer := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(peer.reader)
		initialize, err := readCodexFrame(reader)
		if err != nil {
			peerDone <- err
			return
		}
		if err := writeCodexResponse(peer.writer, initialize["id"]); err != nil {
			peerDone <- err
			return
		}
		if _, err := readCodexFrame(reader); err != nil {
			peerDone <- err
			return
		}
		close(started)
		<-releasePeer
		_ = peer.writer.Close()
		peerDone <- nil
	}()

	client, err := codex.NewStreams(context.Background(), codex.StreamOptions{
		Limits:     codex.Limits{WriteTimeout: time.Minute},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	<-started
	require.Equal(t, struct{}{}, <-peer.writeEvents)
	require.Equal(t, struct{}{}, <-peer.writeEvents)

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- client.Call(context.Background(), "blocked", nil, nil)
	}()
	select {
	case <-peer.writeEvents:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked frame never reached the transport writer")
	}
	select {
	case <-client.Done():
		t.Fatalf("stream terminated before half-close: %v", client.Err())
	default:
	}

	require.NoError(t, client.CloseInput())
	close(releasePeer)
	select {
	case err := <-writeDone:
		require.ErrorIs(t, err, codex.ErrInputClosed)
		var callErr *codex.CallError
		require.ErrorAs(t, err, &callErr)
		require.Equal(t, codex.OutcomeUnknown, callErr.Dispatch)
	case <-time.After(5 * time.Second):
		t.Fatal("blocked write survived half-close")
	}
	require.NoError(t, <-peerDone)
	peer.wait <- nil
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream client did not finish after half-closing a blocked write")
	}
	require.NoError(t, client.Close())
}

func TestStreamClientShortWriteTerminatesWithoutRetry(t *testing.T) {
	peer := newStreamPeer()
	writer := &shortWriteCloser{WriteCloser: peer.transport.Writer, attempted: make(chan struct{}, 1)}
	peer.transport.Writer = writer
	peerDone := make(chan error, 1)
	go func() {
		if err := serveCodexHandshake(peer); err != nil {
			peerDone <- err
			return
		}
		<-writer.attempted
		peerDone <- peer.writer.Close()
	}()

	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	peer.wait <- nil
	writer.short.Store(true)

	err = client.Call(context.Background(), "short-write", nil, nil)
	callError(t, err, codex.OutcomeUnknown, io.ErrShortWrite)
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("short write did not terminate the stream client")
	}
	require.EqualValues(t, 1, writer.calls.Load(), "short write was retried")
	require.NoError(t, <-peerDone)
	require.ErrorIs(t, client.Close(), io.ErrShortWrite)
}

func TestStreamClientCloseIsBoundedWhilePeerWaits(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveCodexHandshake(peer) }()
	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Limits:     codex.Limits{ShutdownTimeout: 50 * time.Millisecond},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	require.NoError(t, <-peerDone)

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited indefinitely for the peer")
	}
	_ = peer.writer.Close()
}

func serveCodexStream(peer *streamPeer, waitForInputEOF bool) error {
	if err := serveCodexHandshake(peer); err != nil {
		return err
	}
	if !waitForInputEOF {
		return peer.writer.Close()
	}
	reader := bufio.NewReader(peer.reader)
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	if _, err := io.WriteString(peer.writer, "{\"method\":\"stream/final\",\"params\":{}}\n"); err != nil {
		return err
	}
	return peer.writer.Close()
}

func serveCodexHandshake(peer *streamPeer) error {
	reader := bufio.NewReader(peer.reader)
	initialize, err := readCodexFrame(reader)
	if err != nil {
		return err
	}
	if err := writeCodexResponse(peer.writer, initialize["id"]); err != nil {
		return err
	}
	if _, err := readCodexFrame(reader); err != nil {
		return err
	}
	return nil
}

func readCodexFrame(reader *bufio.Reader) (map[string]json.RawMessage, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	line = []byte(strings.TrimSuffix(string(line), "\n"))
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(line, &frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func writeCodexResponse(writer io.Writer, id json.RawMessage) error {
	if len(id) == 0 {
		return errors.New("initialize request has no ID")
	}
	_, err := io.WriteString(writer, `{"id":`+string(id)+`,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`+"\n")
	return err
}

type observedWriteCloser struct {
	io.WriteCloser
	started chan<- struct{}
}

func (w *observedWriteCloser) Write(value []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	return w.WriteCloser.Write(value)
}

type shortWriteCloser struct {
	io.WriteCloser
	short     atomic.Bool
	calls     atomic.Int32
	attempted chan struct{}
}

func (w *shortWriteCloser) Write(value []byte) (int, error) {
	if !w.short.Load() {
		return w.WriteCloser.Write(value)
	}
	w.calls.Add(1)
	select {
	case w.attempted <- struct{}{}:
	default:
	}
	return 0, nil
}

type closeErrorWriter struct {
	io.WriteCloser
	err   error
	calls atomic.Int32
}

func (w *closeErrorWriter) Close() error {
	w.calls.Add(1)
	if err := w.WriteCloser.Close(); err != nil {
		return err
	}
	return w.err
}

func TestStreamClientShutdownDeadlineStartsAtHalfClose(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveCodexStream(peer, true) }()
	peer.transport.Wait = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return peer.waitForResult(ctx)
	}
	const shutdownTimeout = 100 * time.Millisecond
	client, err := codex.NewStreams(t.Context(), codex.StreamOptions{
		Limits:     codex.Limits{ShutdownTimeout: shutdownTimeout},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "stream-test", Version: "1"}},
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close(); _ = peer.reader.Close(); _ = peer.writer.Close() })
	// A live conversation may outlast the budget reserved for its later shutdown.
	time.Sleep(2 * shutdownTimeout)
	peer.wait <- nil
	require.NoError(t, client.CloseInput())
	require.NoError(t, <-peerDone)
	require.NoError(t, client.Close())
}
