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

	"github.com/zoumo/agent-sdk-go/internal/queue"
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
			select {
			case session.settingsGate <- struct{}{}:
				<-session.settingsGate
			default:
				t.Error("settings gate remained locked")
			}
			receipt, err := session.Submit(t.Context(), Input{Text: "after failure"})
			assert.False(t, receipt.Written)
			assert.True(t, errors.Is(err, ErrClosed) || errors.As(err, &transportError))
		})
	}
}

func TestSessionSubmitRejectsNilContextBeforeSettingsGate(t *testing.T) {
	session, _ := openPromptTracePeer(t, nil, nil, false)
	writer := &promptTraceFaultWriter{
		WriteCloser: session.client.streamWriter,
		block:       true,
		started:     make(chan struct{}),
		closed:      make(chan struct{}),
	}
	session.client.streamWriter = writer
	t.Cleanup(func() { _ = writer.Close() })

	first := make(chan error, 1)
	go func() {
		_, err := session.Submit(
			WithTraceContext(t.Context(), TraceContext{Traceparent: promptParent}),
			Input{Text: "first"},
		)
		first <- err
	}()
	select {
	case <-writer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("settings write did not start")
	}

	nilResult := make(chan error, 1)
	go func() {
		_, err := session.Submit(nil, Input{Text: "nil context"}) //nolint:staticcheck // verifies rejection before the settings gate.
		nilResult <- err
	}()
	select {
	case err := <-nilResult:
		require.Error(t, err)
		require.ErrorIs(t, err, queue.ErrNilContext)
		assert.NotErrorIs(t, err, context.Canceled)
		var dispatch *DispatchError
		require.ErrorAs(t, err, &dispatch)
		assert.Equal(t, NotSent, dispatch.State)
	case <-time.After(500 * time.Millisecond):
		_ = writer.Close()
		<-first
		<-nilResult
		t.Fatal("nil-context Submit waited behind the settings gate")
	}

	_ = writer.Close()
	select {
	case err := <-first:
		require.Error(t, err)
		var transportError *Error
		require.ErrorAs(t, err, &transportError)
		assert.Equal(t, "write", transportError.Op)
		assert.True(t, transportError.OutcomeUnknown)
	case <-time.After(3 * time.Second):
		t.Fatal("blocked settings write did not settle after releasing the writer")
	}
	assert.NotContains(t, writer.written.String(), `"type":"user"`)
}

func TestPromptTraceCanceledSettingsWaitsReturnPromptly(t *testing.T) {
	for _, operation := range []string{"submit", "effort"} {
		t.Run(operation, func(t *testing.T) {
			session, _ := openPromptTracePeer(t, nil, nil, false)
			writer := &promptTraceFaultWriter{
				WriteCloser: session.client.streamWriter,
				block:       true,
				started:     make(chan struct{}),
				closed:      make(chan struct{}),
			}
			session.client.streamWriter = writer
			t.Cleanup(func() { _ = writer.Close() })

			first := make(chan error, 1)
			go func() {
				_, err := session.Submit(
					WithTraceContext(t.Context(), TraceContext{Traceparent: promptParent}),
					Input{Text: "first"},
				)
				first <- err
			}()
			select {
			case <-writer.started:
			case <-time.After(3 * time.Second):
				t.Fatal("settings write did not start")
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type result struct {
				submission Submission
				receipt    ConfigurationReceipt
				err        error
			}
			second := make(chan result, 1)
			go func() {
				if operation == "submit" {
					submission, err := session.Submit(ctx, Input{Text: "canceled"})
					second <- result{submission: submission, err: err}
					return
				}
				receipt, err := session.SetEffort(ctx, EffortChange{Level: new("high")})
				second <- result{receipt: receipt, err: err}
			}()
			time.Sleep(100 * time.Millisecond)
			select {
			case got := <-second:
				cancel()
				_ = writer.Close()
				<-first
				t.Fatalf("%s returned before cancellation: %v", operation, got.err)
			default:
			}

			cancel()
			start := time.Now()
			select {
			case got := <-second:
				assert.Less(t, time.Since(start), 500*time.Millisecond)
				require.Error(t, got.err)
				require.ErrorIs(t, got.err, context.Canceled)
				var dispatch *DispatchError
				require.ErrorAs(t, got.err, &dispatch)
				assert.Equal(t, NotSent, dispatch.State)
				if operation == "submit" {
					assert.False(t, got.submission.Written)
				} else {
					assert.False(t, got.receipt.Accepted)
					assert.Empty(t, got.receipt.Requested)
				}
			case <-time.After(500 * time.Millisecond):
				_ = writer.Close()
				<-first
				<-second
				t.Fatalf("canceled %s waited behind the settings gate", operation)
			}

			_ = writer.Close()
			select {
			case err := <-first:
				require.Error(t, err)
				var transportError *Error
				require.ErrorAs(t, err, &transportError)
				assert.Equal(t, "write", transportError.Op)
				assert.True(t, transportError.OutcomeUnknown)
			case <-time.After(3 * time.Second):
				t.Fatal("blocked settings write did not settle after releasing the writer")
			}
			assert.NotContains(t, writer.written.String(), `"type":"user"`)
		})
	}
}
