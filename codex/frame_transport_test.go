package codex

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLineFrameReadContract(t *testing.T) {
	t.Run("fragmented reads and consecutive frames", func(t *testing.T) {
		reader := newLineFrameReader(iotest.OneByteReader(strings.NewReader("alpha\r\nbeta\n")), 16)
		first, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, "alpha", string(first))
		second, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, "beta", string(second))
		_, err = reader.ReadFrame()
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("buffer-full fragment", func(t *testing.T) {
		payload := strings.Repeat("x", 70<<10)
		reader := newLineFrameReader(strings.NewReader(payload+"\n"), len(payload))
		got, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, payload, string(got))
	})

	t.Run("CRLF removes one delimiter suffix per kind", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader("alpha\r\r\n"), 16)
		got, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, "alpha\r", string(got))
	})

	t.Run("exact payload limit excludes newline", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader("1234\n"), 4)
		got, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, "1234", string(got))
	})

	t.Run("payload over limit", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader("12345\n"), 4)
		_, err := reader.ReadFrame()
		require.ErrorIs(t, err, ErrProtocol)
		require.ErrorIs(t, err, ErrFrameTooLarge)
	})

	t.Run("empty EOF is ordinary EOF", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader(""), 4)
		_, err := reader.ReadFrame()
		require.ErrorIs(t, err, io.EOF)
		require.NotErrorIs(t, err, ErrProtocol)
	})

	t.Run("unterminated nonempty frame", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader("partial"), 16)
		_, err := reader.ReadFrame()
		require.ErrorIs(t, err, ErrProtocol)
		require.ErrorContains(t, err, "unterminated frame")
	})

	t.Run("valid prefix precedes later framing failure", func(t *testing.T) {
		reader := newLineFrameReader(strings.NewReader("valid\npartial"), 16)
		got, err := reader.ReadFrame()
		require.NoError(t, err)
		require.Equal(t, "valid", string(got))
		_, err = reader.ReadFrame()
		require.ErrorIs(t, err, ErrProtocol)
		require.ErrorContains(t, err, "unterminated frame")
	})
}

func TestStreamFrameTransportPreservesFramingFailureAtEOF(t *testing.T) {
	t.Run("unterminated frame does not mark clean EOF", func(t *testing.T) {
		transport := newStreamFrameTransport(
			io.NopCloser(strings.NewReader("partial")),
			discardWriteCloser{io.Discard},
			nil,
			nil,
			16,
		)
		_, err := transport.ReadFrame()
		require.ErrorIs(t, err, ErrProtocol)
		require.ErrorContains(t, err, "unterminated frame")
		require.False(t, transport.ReadEOF())
	})

	t.Run("local close preserves observed framing failure", func(t *testing.T) {
		reader := &localCloseProtocolReader{
			readStarted:  make(chan struct{}),
			closeStarted: make(chan struct{}),
		}
		transport := newStreamFrameTransport(reader, discardWriteCloser{io.Discard}, nil, nil, 16)
		type result struct {
			err error
		}
		resultCh := make(chan result, 1)
		go func() {
			_, err := transport.ReadFrame()
			resultCh <- result{err: err}
		}()
		select {
		case <-reader.readStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("frame reader did not start")
		}
		require.NoError(t, transport.closeReader())
		select {
		case got := <-resultCh:
			require.ErrorIs(t, got.err, ErrProtocol)
			require.ErrorContains(t, got.err, "unterminated frame")
			require.NotErrorIs(t, got.err, errStreamReaderClosed)
		case <-time.After(5 * time.Second):
			t.Fatal("frame reader did not return after local close")
		}
	})
}

func TestLineFrameWriteContract(t *testing.T) {
	t.Run("writes payload and delimiter once", func(t *testing.T) {
		writer := new(recordingWriter)
		require.NoError(t, writeLineFrame(context.Background(), writer, []byte("payload")))
		require.Len(t, writer.calls, 1)
		require.Equal(t, "payload\n", string(writer.calls[0]))
	})

	t.Run("short write is not retried", func(t *testing.T) {
		writer := &recordingWriter{short: true}
		err := writeLineFrame(context.Background(), writer, []byte("payload"))
		require.ErrorIs(t, err, io.ErrShortWrite)
		require.Len(t, writer.calls, 1)
		require.Equal(t, "payload\n", string(writer.calls[0]))
	})
}

type recordingWriter struct {
	calls [][]byte
	short bool
}

type pipeTestTransport struct {
	writer io.Writer
}

func newPipeTestTransport(writer io.Writer) *pipeTestTransport {
	return &pipeTestTransport{writer: writer}
}

func (t *pipeTestTransport) ReadFrame() ([]byte, error) { return nil, io.EOF }

func (t *pipeTestTransport) WriteFrame(ctx context.Context, payload []byte) error {
	return writeLineFrame(ctx, t.writer, payload)
}

func (t *pipeTestTransport) StreamInput() streamInput { return nil }

func (t *pipeTestTransport) StderrReader() io.Reader { return nil }

func (t *pipeTestTransport) Shutdown(_, _ <-chan struct{}, _ time.Duration) transportShutdown {
	return transportShutdown{}
}

func (t *pipeTestTransport) Abort() {}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, append([]byte(nil), p...))
	if w.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}

type discardWriteCloser struct {
	io.Writer
}

func (discardWriteCloser) Close() error { return nil }

type localCloseProtocolReader struct {
	readStarted  chan struct{}
	closeStarted chan struct{}
	readOnce     sync.Once
	closeOnce    sync.Once
}

func (r *localCloseProtocolReader) Read(p []byte) (int, error) {
	r.readOnce.Do(func() { close(r.readStarted) })
	<-r.closeStarted
	return copy(p, "partial"), io.EOF
}

func (r *localCloseProtocolReader) Close() error {
	r.closeOnce.Do(func() { close(r.closeStarted) })
	return nil
}
