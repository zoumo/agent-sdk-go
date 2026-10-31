package mocktest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const noticeScenario = `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"wire":{"method":"notice","type":"notice"}}]}`

func TestExplicitExecutableRequired(t *testing.T) {
	for _, path := range []string{"", "agent-sdk-testify", filepath.Join(t.TempDir(), "missing")} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("AGENT_SDK_TESTIFY_BIN", path)
			_, err := Executable()
			require.ErrorContains(t, err, "AGENT_SDK_TESTIFY_BIN")
		})
	}
}

func TestProgressBeforeEOFWithoutPython(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			complete := false
			err := Run(ctx, provider, input, io.Discard, []byte(noticeScenario), Options{Complete: func() error {
				complete = true
				return writer.Close()
			}})
			require.NoError(t, err)
			require.True(t, complete, "progress must release the input before mock waits for final EOF")
		})
	}
}

func TestBridgeFailureSemantics(t *testing.T) {
	t.Run("disconnect code 4", func(t *testing.T) {
		scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
		called := false
		err := Run(context.Background(), "codex", strings.NewReader(""), io.Discard, []byte(scenario), Options{Complete: func() error { called = true; return nil }})
		require.ErrorIs(t, err, ErrDisconnected)
		require.False(t, called)
	})
	t.Run("deadline code 3", func(t *testing.T) {
		input, writer := io.Pipe()
		defer input.Close()
		defer writer.Close()
		scenario := strings.Replace(noticeScenario, `"version":1`, `"version":1,"timeoutMs":100`, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := Run(ctx, "codex", input, io.Discard, []byte(scenario), Options{})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NoError(t, ctx.Err(), "mock exit 3 must return before the parent deadline")
	})
	t.Run("disconnect with idle file descriptor", func(t *testing.T) {
		input, writer, err := os.Pipe()
		require.NoError(t, err)
		defer input.Close()
		defer writer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
		err = Run(ctx, "codex", input, io.Discard, []byte(scenario), Options{})
		require.ErrorIs(t, err, ErrDisconnected)
		require.NoError(t, ctx.Err(), "a child can disconnect while upstream stdin remains open")
	})
	t.Run("cancel blocked input", func(t *testing.T) {
		input, writer := io.Pipe()
		defer input.Close()
		defer writer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := Run(ctx, "codex", input, io.Discard, []byte(noticeScenario), Options{Complete: func() error { cancel(); return nil }})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("callback error", func(t *testing.T) {
		want := errors.New("completion failed")
		err := Run(context.Background(), "claude", strings.NewReader(""), io.Discard, []byte(noticeScenario), Options{Complete: func() error { return want }})
		require.ErrorIs(t, err, want)
	})
}

func TestLauncherWithoutPythonAndReceipt(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "space and ' quote")
			require.NoError(t, os.Mkdir(dir, 0700))
			scenario := filepath.Join(dir, "scenario.json")
			receipt := filepath.Join(dir, "receipt.json")
			require.NoError(t, os.WriteFile(scenario, []byte(noticeScenario), 0600))
			launcher, err := Launcher(dir, provider, scenario, receipt)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, launcher)
			cmd.Env = []string{"PATH="}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			data, err := os.ReadFile(receipt)
			require.NoError(t, err)
			var got struct {
				Version                                    int
				FullConsumption, NativeLaunched, ClientEOF bool
				Peer, Provider, RecordingSHA256            string
			}
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, 1, got.Version)
			require.True(t, got.FullConsumption)
			require.True(t, got.ClientEOF)
			require.False(t, got.NativeLaunched)
			require.Equal(t, "Go native mock", got.Peer)
			require.Equal(t, provider, got.Provider)
			sum := sha256.Sum256([]byte(noticeScenario))
			require.Equal(t, hex.EncodeToString(sum[:]), got.RecordingSHA256)
		})
	}
}

// gateWriter releases a blocked Write as Close starts, but lets the test hold
// Close itself open to prove Run joins the cancellation callback.
type gateWriter struct {
	written, closing, release, closed chan struct{}
	writeOnce, closeOnce              sync.Once
}

func newGateWriter() *gateWriter {
	return &gateWriter{written: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
}
func (w *gateWriter) Write(p []byte) (int, error) {
	w.writeOnce.Do(func() { close(w.written) })
	<-w.closing
	return 0, io.ErrClosedPipe
}
func (w *gateWriter) Close() error {
	w.closeOnce.Do(func() { close(w.closing); <-w.release; close(w.closed) })
	return nil
}
func TestCancellationJoinsStreamCleanup(t *testing.T) {
	output := newGateWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, "codex", strings.NewReader(""), output, []byte(noticeScenario), Options{}) }()
	select {
	case <-output.written:
	case <-time.After(3 * time.Second):
		cancel()
		close(output.release)
		t.Fatal("stdout did not reach writer")
	}
	cancel()
	select {
	case <-output.closing:
	case <-time.After(3 * time.Second):
		close(output.release)
		t.Fatal("cancel did not close writer")
	}
	var early error
	returned := false
	select {
	case early = <-done:
		returned = true
	case <-time.After(100 * time.Millisecond):
	}
	close(output.release)
	if !returned {
		select {
		case early = <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not join cleanup")
		}
	}
	require.False(t, returned, "Run returned while cancellation cleanup was still active")
	require.ErrorIs(t, early, context.Canceled)
	select {
	case <-output.closed:
	default:
		t.Fatal("stream cleanup outlived Run")
	}
}

// A slow consumer must still receive the final protocol frame on disconnect.
func TestDisconnectDrainsOutputBeforeReturn(t *testing.T) {
	reader, output := io.Pipe()
	defer reader.Close()
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	scenario := strings.TrimSuffix(noticeScenario, "]}") + `,{"ordinal":2,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
	done := make(chan error, 1)
	go func() { done <- Run(ctx, "codex", strings.NewReader(""), output, []byte(scenario), Options{}) }()
	// Hold consumption while the peer writes and exits. Closing the output on
	// process exit would truncate buffered frames before the consumer drains them.
	var early error
	returned := false
	select {
	case early = <-done:
		returned = true
	case <-time.After(100 * time.Millisecond):
	}
	data := make([]byte, len(`{"method":"notice","type":"notice"}`)+1)
	_, readErr := io.ReadFull(reader, data)
	if !returned {
		select {
		case early = <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not drain output")
		}
	}
	require.False(t, returned, "Run returned before buffered protocol output drained")
	require.NoError(t, readErr)
	require.Equal(t, "{\"method\":\"notice\",\"type\":\"notice\"}\n", string(data))
	require.ErrorIs(t, early, ErrDisconnected)
	require.NoError(t, ctx.Err())
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
func TestOutputFailureCancelsIdlePeer(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	want := errors.New("caller output failed")
	err := Run(ctx, "codex", input, failingWriter{want}, []byte(noticeScenario), Options{})
	require.ErrorIs(t, err, want)
	require.NoError(t, ctx.Err(), "output failure must cancel the peer without waiting for the parent deadline")
}
