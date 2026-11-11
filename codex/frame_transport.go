package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

type frameTransport interface {
	ReadFrame() ([]byte, error)
	WriteFrame(context.Context, []byte) error
}

var errStreamReaderClosed = errors.New("codex: stream reader closed locally")

type lineFrameReader struct {
	reader *bufio.Reader
	max    int
}

func newLineFrameReader(reader io.Reader, maxFrameBytes int) *lineFrameReader {
	size := 64 << 10
	if maxFrameBytes < size {
		size = maxFrameBytes + 1
	}
	return &lineFrameReader{reader: bufio.NewReaderSize(reader, size), max: maxFrameBytes}
}

func (r *lineFrameReader) ReadFrame() ([]byte, error) {
	var frame []byte
	for {
		part, err := r.reader.ReadSlice('\n')
		total := len(frame) + len(part)
		terminated := len(part) > 0 && part[len(part)-1] == '\n'
		payload := total
		if terminated {
			payload--
		}
		if payload > r.max {
			return nil, fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge)
		}
		frame = append(frame, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if len(frame) > 0 {
				return nil, fmt.Errorf("%w: unterminated frame: %w", ErrProtocol, err)
			}
			return nil, err
		}
		frame = bytes.TrimSuffix(frame, []byte{'\n'})
		frame = bytes.TrimSuffix(frame, []byte{'\r'})
		return frame, nil
	}
}

func writeLineFrame(_ context.Context, writer io.Writer, payload []byte) error {
	frame := make([]byte, len(payload)+1)
	copy(frame, payload)
	frame[len(payload)] = '\n'
	n, err := writer.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

type stdioFrameTransport struct {
	reader *lineFrameReader
	writer io.Writer
}

func newStdioFrameTransport(reader io.Reader, writer io.Writer, maxFrameBytes int) *stdioFrameTransport {
	return &stdioFrameTransport{
		reader: newLineFrameReader(reader, maxFrameBytes),
		writer: writer,
	}
}

func (t *stdioFrameTransport) ReadFrame() ([]byte, error) {
	return t.reader.ReadFrame()
}

func (t *stdioFrameTransport) WriteFrame(ctx context.Context, payload []byte) error {
	return writeLineFrame(ctx, t.writer, payload)
}

type streamFrameTransport struct {
	reader     *lineFrameReader
	readCloser io.ReadCloser
	writer     io.WriteCloser
	stderr     io.ReadCloser
	wait       func(context.Context) error

	mu                    sync.Mutex
	inputHalfClosed       bool
	inputCloseErr         error
	inputHalfClosedSignal chan struct{}
	readEOF               bool
	readClosedLocally     bool
}

func newStreamFrameTransport(reader io.ReadCloser, writer io.WriteCloser, stderr io.ReadCloser, wait func(context.Context) error, maxFrameBytes int) *streamFrameTransport {
	return &streamFrameTransport{
		reader:                newLineFrameReader(reader, maxFrameBytes),
		readCloser:            reader,
		writer:                writer,
		stderr:                stderr,
		wait:                  wait,
		inputHalfClosedSignal: make(chan struct{}),
	}
}

func (t *streamFrameTransport) ReadFrame() ([]byte, error) {
	frame, err := t.reader.ReadFrame()
	if err != nil {
		t.mu.Lock()
		closedLocally := t.readClosedLocally
		if errors.Is(err, io.EOF) && !closedLocally {
			t.readEOF = true
		}
		t.mu.Unlock()
		if closedLocally {
			return nil, errStreamReaderClosed
		}
	}
	return frame, err
}

func (t *streamFrameTransport) WriteFrame(ctx context.Context, payload []byte) error {
	if t.InputHalfClosed() {
		return ErrInputClosed
	}
	err := writeLineFrame(ctx, t.writer, payload)
	if err != nil && t.InputHalfClosed() {
		return ErrInputClosed
	}
	return err
}

func (t *streamFrameTransport) CloseInput() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inputHalfClosed {
		if t.inputCloseErr != nil {
			return t.inputCloseErr
		}
		return ErrInputClosed
	}
	t.inputHalfClosed = true
	t.inputCloseErr = t.writer.Close()
	close(t.inputHalfClosedSignal)
	return t.inputCloseErr
}

func (t *streamFrameTransport) InputHalfClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inputHalfClosed
}

func (t *streamFrameTransport) InputHalfClosedSignal() <-chan struct{} {
	return t.inputHalfClosedSignal
}

func (t *streamFrameTransport) StderrReader() io.Reader {
	return t.stderr
}

func (t *streamFrameTransport) HasStderr() bool {
	return t.stderr != nil
}

func (t *streamFrameTransport) ReadEOF() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.readEOF
}

func (t *streamFrameTransport) closeReader() error {
	t.mu.Lock()
	t.readClosedLocally = true
	t.mu.Unlock()
	return t.readCloser.Close()
}

// Complete coordinates only endpoint shutdown. The Client still decides which
// terminal cause and public result the observed facts produce.
func (t *streamFrameTransport) Complete(readDone <-chan struct{}, timeout time.Duration) streamCompletion {
	deadline := time.Now().Add(timeout)
	_ = t.CloseInput()

	var waitErr error
	if t.wait != nil {
		waitCtx, cancel := context.WithDeadline(context.Background(), deadline)
		waitErr = t.wait(waitCtx)
		cancel()
		if waitErr != nil {
			_ = t.closeReader()
		}
	}

	readComplete := awaitUntil(readDone, deadline)
	_ = t.CloseInput()
	if !readComplete {
		_ = t.closeReader()
		readComplete = awaitUntil(readDone, time.Now().Add(timeout))
	}
	_ = t.closeReader()
	if t.stderr != nil {
		_ = t.stderr.Close()
	}
	return streamCompletion{
		waitErr:      waitErr,
		readComplete: readComplete,
		readEOF:      t.ReadEOF(),
	}
}

type streamCompletion struct {
	waitErr      error
	readComplete bool
	readEOF      bool
}

type websocketFrameTransport struct {
	conn *websocket.Conn
	max  int
}

func newWebsocketFrameTransport(conn *websocket.Conn, maxFrameBytes int) *websocketFrameTransport {
	return &websocketFrameTransport{conn: conn, max: maxFrameBytes}
}

func (t *websocketFrameTransport) ReadFrame() ([]byte, error) {
	kind, raw, err := t.conn.Read(context.Background())
	if err != nil {
		var networkError net.Error
		var closeError websocket.CloseError
		switch {
		case errors.As(err, &closeError) && !utf8.ValidString(closeError.Reason):
			return nil, fmt.Errorf("%w: invalid WebSocket close reason", ErrProtocol)
		case errors.Is(err, websocket.ErrMessageTooBig):
			return nil, fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge)
		case websocket.CloseStatus(err) == websocket.StatusProtocolError || websocket.CloseStatus(err) == websocket.StatusInvalidFramePayloadData:
			return nil, fmt.Errorf("%w: invalid WebSocket frame", ErrProtocol)
		case websocket.CloseStatus(err) >= 0, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed), errors.As(err, &networkError):
			return nil, fmt.Errorf("%w: read: %w", ErrRemoteTransport, io.EOF)
		default:
			return nil, fmt.Errorf("%w: invalid WebSocket framing", ErrProtocol)
		}
	}
	if len(raw) > t.max {
		return nil, fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge)
	}
	if kind != websocket.MessageText {
		return nil, fmt.Errorf("%w: expected WebSocket text message", ErrProtocol)
	}
	return raw, nil
}

func (t *websocketFrameTransport) WriteFrame(ctx context.Context, payload []byte) error {
	// The shared writer owns cancellation and dispatch classification. A call
	// may receive its response before Write returns; its deferred context
	// cancellation must not tear down an otherwise successful connection.
	if err := t.conn.Write(context.Background(), websocket.MessageText, payload); err != nil {
		// Close reasons and library errors may contain peer-controlled text,
		// including reflected headers. Expose only safe classification.
		if ctx.Err() != nil {
			return fmt.Errorf("%w: write: %w", ErrRemoteTransport, ctx.Err())
		}
		return fmt.Errorf("%w: write: %w", ErrRemoteTransport, io.EOF)
	}
	return nil
}
