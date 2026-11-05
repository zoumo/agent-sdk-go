package mocktest

import (
	"context"
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

func prepareSession(t *testing.T, provider, scenario string, options Options) *Session {
	t.Helper()
	session, err := Prepare(t.TempDir(), provider, []byte(scenario), options)
	require.NoError(t, err)
	return session
}

func closeSession(t *testing.T, session *Session) *SessionReport {
	t.Helper()
	report, err := session.Report()
	require.NotNil(t, report)
	require.NoError(t, err)
	require.NoError(t, session.Close())
	require.NoDirExists(t, session.Dir)
	return report
}

func TestExplicitExecutableRequired(t *testing.T) {
	for _, path := range []string{"", "agent-sdk-testify", filepath.Join(t.TempDir(), "missing")} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("AGENT_SDK_TESTIFY_BIN", path)
			_, err := Executable()
			require.ErrorContains(t, err, "AGENT_SDK_TESTIFY_BIN")
		})
	}
}

func TestPreparedSyntheticProvenanceAndIdentity(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	var descriptor struct {
		SchemaVersion int
		ToolSHA256    string
		Source        struct {
			Kind, Path, Provenance string
		}
		Routes struct {
			Mock struct {
				Workspace string
				Env       map[string]string
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(session.Dir, "session.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &descriptor))
	require.Equal(t, 3, descriptor.SchemaVersion)
	require.FileExists(t, filepath.Join(descriptor.Source.Path, "plan.json"))
	require.Equal(t, "recording", descriptor.Source.Kind)
	require.Equal(t, "external", descriptor.Source.Provenance)
	require.Equal(t, session.Workspace, descriptor.Routes.Mock.Workspace)
	require.Equal(t, session.Env, sortedEnvironment(descriptor.Routes.Mock.Env))
	var manifest struct{ Provenance string }
	data, err = os.ReadFile(filepath.Join(descriptor.Source.Path, "manifest.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, "synthetic", manifest.Provenance)
	report := closeSession(t, session)
	require.Equal(t, "abandoned", report.Status)
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
			session := prepareSession(t, provider, noticeScenario, Options{Complete: func() error {
				complete = true
				return writer.Close()
			}})
			require.NoError(t, session.Run(ctx, input, io.Discard))
			require.True(t, complete, "progress must release the input before mock waits for final EOF")
			require.Equal(t, "passed", closeSession(t, session).Status)
		})
	}
}

func TestBridgeFailureSemantics(t *testing.T) {
	t.Run("scripted disconnect", func(t *testing.T) {
		scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
		called := false
		session := prepareSession(t, "codex", scenario, Options{Complete: func() error { called = true; return nil }})
		err := session.Run(context.Background(), strings.NewReader(""), io.Discard)
		require.ErrorIs(t, err, ErrDisconnected)
		require.False(t, called)
		require.Equal(t, "failed", closeSession(t, session).Status)
	})
	t.Run("scripted deadline", func(t *testing.T) {
		input, writer := io.Pipe()
		defer input.Close()
		defer writer.Close()
		scenario := strings.Replace(noticeScenario, `"version":1`, `"version":1,"timeoutMs":100`, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		session := prepareSession(t, "codex", scenario, Options{})
		err := session.Run(ctx, input, io.Discard)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NoError(t, ctx.Err(), "mock deadline must return before the parent deadline")
		require.Equal(t, "failed", closeSession(t, session).Status)
	})
	t.Run("disconnect with idle file descriptor", func(t *testing.T) {
		input, writer, err := os.Pipe()
		require.NoError(t, err)
		defer input.Close()
		defer writer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
		session := prepareSession(t, "codex", scenario, Options{})
		err = session.Run(ctx, input, io.Discard)
		require.ErrorIs(t, err, ErrDisconnected)
		require.NoError(t, ctx.Err(), "a child can disconnect while upstream stdin remains open")
		require.Equal(t, "failed", closeSession(t, session).Status)
	})
	t.Run("cancel blocked input", func(t *testing.T) {
		input, writer := io.Pipe()
		defer input.Close()
		defer writer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		session := prepareSession(t, "codex", noticeScenario, Options{Complete: func() error { cancel(); return nil }})
		err := session.Run(ctx, input, io.Discard)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, "passed", closeSession(t, session).Status)
	})
	t.Run("callback error", func(t *testing.T) {
		want := errors.New("completion failed")
		session := prepareSession(t, "claude", noticeScenario, Options{Complete: func() error { return want }})
		err := session.Run(context.Background(), strings.NewReader(""), io.Discard)
		require.ErrorIs(t, err, want)
		require.Equal(t, "passed", closeSession(t, session).Status)
	})
}

func TestLauncherUsesPreparedSession(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			session := prepareSession(t, provider, noticeScenario, Options{})
			launcher, err := session.Launcher("main")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command, err := commandContext(ctx, launcher)
			require.NoError(t, err)
			command.Env = session.Env
			command.Dir = session.Workspace
			command.Stdin = strings.NewReader("")
			out, err := command.CombinedOutput()
			require.NoError(t, err, string(out))
			report := closeSession(t, session)
			require.Equal(t, "passed", report.Status)
		})
	}
}

func commandContext(ctx context.Context, executable string) (*exec.Cmd, error) {
	if executable == "" {
		return nil, errors.New("empty executable")
	}
	return exec.CommandContext(ctx, executable), nil
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
	session := prepareSession(t, "codex", noticeScenario, Options{})
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx, strings.NewReader(""), output) }()
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
	require.Equal(t, "passed", closeSession(t, session).Status)
}

// A slow consumer must still receive the final protocol frame on disconnect.
func TestDisconnectDrainsOutputBeforeReturn(t *testing.T) {
	reader, output := io.Pipe()
	defer reader.Close()
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	scenario := strings.TrimSuffix(noticeScenario, "]}") + `,{"ordinal":2,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx, strings.NewReader(""), output) }()
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
	require.Equal(t, "{\"method\":\"notice\",\"type\":\"notice\"}\n", string(data)) //nolint:testifylint // preserves framing bytes and trailing newline
	require.ErrorIs(t, early, ErrDisconnected)
	require.NoError(t, ctx.Err())
	require.Equal(t, "failed", closeSession(t, session).Status)
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
	session := prepareSession(t, "codex", noticeScenario, Options{})
	err := session.Run(ctx, input, failingWriter{want})
	require.ErrorIs(t, err, want)
	require.NoError(t, ctx.Err(), "output failure must cancel the peer without waiting for the parent deadline")
	require.Equal(t, "passed", closeSession(t, session).Status)
}
