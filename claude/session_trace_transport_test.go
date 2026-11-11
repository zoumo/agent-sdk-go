package claude

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptTraceFaultWriter struct {
	io.WriteCloser
	block   bool
	short   bool
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
	written bytes.Buffer
}

func (writer *promptTraceFaultWriter) Write(frame []byte) (int, error) {
	if bytes.Contains(frame, []byte(`"apply_flag_settings"`)) {
		close(writer.started)
		if writer.block {
			<-writer.closed
			return 0, io.ErrClosedPipe
		}
		prefix := frame[:len(frame)/2]
		_, _ = writer.written.Write(prefix)
		if writer.short {
			return len(prefix), nil
		}
		return len(prefix), io.ErrUnexpectedEOF
	}
	_, _ = writer.written.Write(frame)
	return writer.WriteCloser.Write(frame)
}

func (writer *promptTraceFaultWriter) Close() error {
	writer.once.Do(func() { close(writer.closed) })
	return writer.WriteCloser.Close()
}

func TestPromptTraceTransportFailure(t *testing.T) {
	for _, mode := range []string{"blocked", "partial", "short"} {
		t.Run(mode, func(t *testing.T) {
			session, _ := openPromptTracePeer(t, nil, nil, false)
			writer := &promptTraceFaultWriter{
				WriteCloser: session.client.streamWriter,
				block:       mode == "blocked",
				short:       mode == "short",
				started:     make(chan struct{}),
				closed:      make(chan struct{}),
			}
			session.client.streamWriter = writer
			t.Cleanup(func() { _ = writer.Close() })
			type result struct {
				submission Submission
				err        error
			}
			results := make(chan result, 1)
			go func() {
				submission, err := session.Submit(WithTraceContext(t.Context(), TraceContext{Traceparent: promptParent}), Input{Text: "must not send"})
				results <- result{submission, err}
			}()
			select {
			case <-writer.started:
			case <-time.After(3 * time.Second):
				t.Fatal("settings write did not start")
			}
			var outcome result
			select {
			case outcome = <-results:
			case <-time.After(3 * time.Second):
				assert.Fail(t, "settings write did not settle after its deadline")
				_ = writer.Close()
				select {
				case outcome = <-results:
				case <-time.After(3 * time.Second):
					t.Fatal("submission did not settle after closing writer")
				}
			}
			assert.False(t, outcome.submission.Written)
			require.Error(t, outcome.err)
			var transportError *Error
			require.ErrorAs(t, outcome.err, &transportError)
			assert.Equal(t, "write", transportError.Op)
			assert.True(t, transportError.OutcomeUnknown)
			switch mode {
			case "blocked":
				assert.ErrorIs(t, outcome.err, context.DeadlineExceeded)
			case "short":
				assert.ErrorIs(t, outcome.err, io.ErrShortWrite)
			default:
				assert.ErrorIs(t, outcome.err, io.ErrUnexpectedEOF)
			}
			assert.NotContains(t, writer.written.String(), `"type":"user"`)
			assert.Error(t, session.Err())
			assert.Empty(t, session.client.writeGate)
			if session.settingsMu.TryLock() {
				session.settingsMu.Unlock()
			} else {
				t.Error("settings gate remained locked")
			}
			receipt, err := session.Submit(t.Context(), Input{Text: "after failure"})
			assert.False(t, receipt.Written)
			assert.True(t, errors.Is(err, ErrClosed) || errors.As(err, &transportError))
		})
	}
}
