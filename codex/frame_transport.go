package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"unicode/utf8"

	"github.com/coder/websocket"
)

type frameTransport interface {
	ReadFrame() ([]byte, error)
	WriteFrame(context.Context, []byte) error
}

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
	reader      *lineFrameReader
	writer      io.Writer
	inputClosed func() bool
}

func newStreamFrameTransport(reader io.Reader, writer io.Writer, maxFrameBytes int, inputClosed func() bool) *streamFrameTransport {
	return &streamFrameTransport{
		reader:      newLineFrameReader(reader, maxFrameBytes),
		writer:      writer,
		inputClosed: inputClosed,
	}
}

func (t *streamFrameTransport) ReadFrame() ([]byte, error) {
	return t.reader.ReadFrame()
}

func (t *streamFrameTransport) WriteFrame(ctx context.Context, payload []byte) error {
	if t.inputClosed() {
		return ErrInputClosed
	}
	return writeLineFrame(ctx, t.writer, payload)
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
