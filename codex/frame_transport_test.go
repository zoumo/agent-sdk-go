package codex

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"

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

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, append([]byte(nil), p...))
	if w.short {
		return len(p) - 1, nil
	}
	return len(p), nil
}
