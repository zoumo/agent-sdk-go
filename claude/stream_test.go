package claude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude"
)

type streamPeer struct {
	reader      *io.PipeReader
	writer      *io.PipeWriter
	writeEvents chan struct{}
	wait        chan error
	waitStarted chan struct{}
	transport   claude.StreamTransport
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
	peer.transport = claude.StreamTransport{
		Reader: sdkInput,
		Writer: &observedWriteCloser{WriteCloser: sdkOutput, started: peer.writeEvents},
		Wait: func(ctx context.Context) error {
			select {
			case <-peer.waitStarted:
			default:
				close(peer.waitStarted)
			}
			select {
			case err := <-peer.wait:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	return peer
}

func (p *streamPeer) close() {
	_ = p.reader.Close()
	_ = p.writer.Close()
}

func TestStreamSessionKeepsReaderAfterInputHalfClose(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamResult(peer) }()

	session := openTestStreamSession(t, peer)
	_, err := session.Submit(t.Context(), claude.Input{Text: "hello"})
	require.NoError(t, err)
	receiveStreamEvent(t, session, func(event *claude.Event) bool { return event.Result != nil })

	require.NoError(t, session.CloseInput())
	require.ErrorIs(t, session.CloseInput(), claude.ErrInputClosed)
	receiveStreamEvent(t, session, func(event *claude.Event) bool {
		return event.Type == "assistant" && event.Assistant != nil
	})
	require.NoError(t, <-peerDone)
	peer.wait <- nil

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stream Session did not finish")
	}
	require.NoError(t, session.Err())
	require.NoError(t, session.Close())
}

func TestStreamSessionRejectsUnexpectedPeerEOF(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamHandshakeOnly(peer) }()

	session := openTestStreamSession(t, peer)
	require.NoError(t, <-peerDone)
	require.NoError(t, peer.writer.Close())
	peer.wait <- nil
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("unexpected peer EOF did not terminate the Session")
	}
	require.ErrorIs(t, session.Err(), claude.ErrUnexpectedEOF)
	require.ErrorIs(t, session.Close(), claude.ErrUnexpectedEOF)
}

func TestStreamSessionWaitFailureReplacesUnexpectedPeerEOF(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamHandshakeOnly(peer) }()

	session := openTestStreamSession(t, peer)
	require.NoError(t, <-peerDone)
	require.NoError(t, peer.writer.Close())
	peerErr := errors.New("authoritative replay failure")
	peer.wait <- peerErr
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("peer completion failure did not terminate the Session")
	}
	require.ErrorIs(t, session.Close(), peerErr)
	require.ErrorIs(t, session.Err(), peerErr)
}

func TestStreamSessionPreservesPeerWaitFailure(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamUntilEOF(peer) }()

	session := openTestStreamSession(t, peer)
	require.NoError(t, session.CloseInput())
	require.NoError(t, <-peerDone)
	peerErr := errors.New("synthetic peer completion failure")
	peer.wait <- peerErr
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("peer completion failure did not terminate the Session")
	}
	require.ErrorIs(t, session.Err(), peerErr)
	require.ErrorIs(t, session.Close(), peerErr)
}

func TestStreamSessionClosePreservesPeerWaitFailure(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamUntilEOF(peer) }()

	session := openTestStreamSession(t, peer)

	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	require.NoError(t, <-peerDone)
	select {
	case <-peer.waitStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for the peer result")
	}
	peerErr := errors.New("synthetic peer completion failure during close")
	peer.wait <- peerErr
	require.ErrorIs(t, <-closed, peerErr)
	require.ErrorIs(t, session.Err(), peerErr)
}

func TestStreamSessionHalfCloseInterruptsBlockedWrite(t *testing.T) {
	peer := newStreamPeer()
	handshakeDone := make(chan error, 1)
	go func() { handshakeDone <- serveStreamHandshakeOnly(peer) }()

	session := openTestStreamSession(t, peer)
	require.NoError(t, <-handshakeDone)
	select {
	case <-peer.writeEvents:
	case <-time.After(5 * time.Second):
		t.Fatal("initialize frame never reached the peer")
	}

	submitted := make(chan error, 1)
	go func() {
		_, err := session.Submit(context.Background(), claude.Input{Text: "blocked"})
		submitted <- err
	}()
	select {
	case <-peer.writeEvents:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked frame never reached the transport writer")
	}
	require.NoError(t, session.CloseInput())
	select {
	case err := <-submitted:
		require.ErrorIs(t, err, claude.ErrInputClosed)
		var dispatch *claude.DispatchError
		require.ErrorAs(t, err, &dispatch)
		require.Equal(t, claude.OutcomeUnknown, dispatch.State)
	case <-time.After(5 * time.Second):
		t.Fatal("blocked write survived input half-close")
	}
	require.NoError(t, peer.writer.Close())
	peer.wait <- nil
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Session did not finish after interrupted write")
	}
	require.NoError(t, session.Close())
}

func TestStreamSessionCloseIsBoundedWhilePeerWaits(t *testing.T) {
	peer := newStreamPeer()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveStreamHandshakeOnly(peer) }()

	dir := t.TempDir()
	session, err := claude.OpenStreams(t.Context(), claude.SessionOptions{
		CLIPath:           filepath.Join(dir, "must-not-start"),
		WorkDir:           dir,
		Env:               []string{"HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, ".claude")},
		InitializeTimeout: 2 * time.Second,
		ShutdownTimeout:   50 * time.Millisecond,
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(); peer.close() })
	require.NoError(t, <-peerDone)

	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited indefinitely for the peer")
	}
}

func TestNativeArgsIsTheOpenVector(t *testing.T) {
	options := claude.SessionOptions{
		Model:           "synthetic",
		Effort:          "high",
		Tools:           []string{"Read", "Write"},
		DisallowedTools: []string{"Bash"},
		ResumeID:        "session-1",
	}
	args, err := claude.NativeArgs(options)
	require.NoError(t, err)
	require.Equal(t, []string{
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--include-partial-messages",
		"--model=synthetic",
		"--effort=high",
		"--tools=Read,Write",
		"--disallowedTools=Bash",
		"--resume=session-1",
	}, args)
}

func openTestStreamSession(t *testing.T, peer *streamPeer) *claude.Session {
	t.Helper()
	dir := t.TempDir()
	session, err := claude.OpenStreams(t.Context(), claude.SessionOptions{
		CLIPath:           filepath.Join(dir, "must-not-start"),
		WorkDir:           dir,
		Env:               []string{"HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, ".claude")},
		InitializeTimeout: 2 * time.Second,
		ShutdownTimeout:   500 * time.Millisecond,
	}, peer.transport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(); peer.close() })
	return session
}

func receiveStreamEvent(t *testing.T, session *claude.Session, accept func(*claude.Event) bool) claude.Received {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		received, err := session.Receive(ctx)
		require.NoError(t, err)
		if received.Event != nil && accept(received.Event) {
			return received
		}
	}
}

func serveStreamResult(peer *streamPeer) error {
	reader, err := serveStreamInitialize(peer)
	if err != nil {
		return err
	}
	if _, err := readStreamFrame(reader); err != nil {
		return err
	}
	if err := writeStreamFrame(peer.writer, `{"type":"system","subtype":"init","session_id":"synthetic","claude_code_version":"2.1.258"}`); err != nil {
		return err
	}
	if err := writeStreamFrame(peer.writer, `{"type":"result","subtype":"success","session_id":"synthetic","is_error":false,"result":"ready"}`); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	if err := writeStreamFrame(peer.writer, `{"type":"assistant","session_id":"synthetic","message":{"role":"assistant","content":[]}}`); err != nil {
		return err
	}
	return peer.writer.Close()
}

func serveStreamUntilEOF(peer *streamPeer) error {
	reader, err := serveStreamInitialize(peer)
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	return peer.writer.Close()
}

func serveStreamHandshakeOnly(peer *streamPeer) error {
	_, err := serveStreamInitialize(peer)
	return err
}

func serveStreamInitialize(peer *streamPeer) (*bufio.Reader, error) {
	reader := bufio.NewReader(peer.reader)
	frame, err := readStreamFrame(reader)
	if err != nil {
		return nil, err
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(frame, &request); err != nil {
		return nil, err
	}
	id := request["request_id"]
	if len(id) == 0 {
		return nil, errors.New("initialize request has no request_id")
	}
	response := `{"type":"control_response","response":{"subtype":"success","request_id":` + string(id) + `,"response":{}}}`
	if err := writeStreamFrame(peer.writer, response); err != nil {
		return nil, err
	}
	return reader, nil
}

func readStreamFrame(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return line[:len(line)-1], nil
}

func writeStreamFrame(writer io.Writer, frame string) error {
	_, err := io.WriteString(writer, frame+"\n")
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
