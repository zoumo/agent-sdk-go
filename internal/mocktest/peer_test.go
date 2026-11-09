package mocktest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agenttestify "github.com/zoumo/agent-sdk-testify"
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

func TestPreparedSyntheticProvenanceAndIdentity(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	var descriptor struct {
		SchemaVersion int
		FixtureID     string `json:"fixtureId"`
		Tool          struct {
			Source         string
			Version        string
			Command        string
			Args           []string
			SHA256         string
			Module         string
			ModuleSum      string `json:"moduleSum"`
			ModuleRevision string `json:"moduleRevision"`
			GoVersion      string `json:"goVersion"`
		}
		Source struct {
			Kind, Path, Provenance string
		}
		Routes map[string]struct {
			Workspace   string
			ProtocolCwd string `json:"protocolCwd"`
			Env         map[string]string
			Connections []struct {
				ID        string
				Transport string
				Command   string
				Args      []string
				Launcher  string
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(session.Dir, "fixture.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &descriptor))
	require.Equal(t, 5, descriptor.SchemaVersion)
	require.Equal(t, session.SessionID, descriptor.FixtureID)
	require.Equal(t, "embedded", descriptor.Tool.Source)
	require.Equal(t, "github.com/zoumo/agent-sdk-testify", descriptor.Tool.Module)
	require.NotEmpty(t, descriptor.Tool.ModuleRevision)
	require.NotEmpty(t, descriptor.Tool.GoVersion)
	require.Empty(t, descriptor.Tool.Command)
	require.Empty(t, descriptor.Tool.Args)
	require.Empty(t, descriptor.Tool.SHA256)
	require.FileExists(t, filepath.Join(descriptor.Source.Path, "plan.json"))
	require.Equal(t, "recording", descriptor.Source.Kind)
	require.Equal(t, "external", descriptor.Source.Provenance)
	mockRoute := descriptor.Routes["mock"]
	require.Equal(t, session.Workspace, mockRoute.Workspace)
	require.Equal(t, session.ProtocolCwd, mockRoute.ProtocolCwd)
	require.Equal(t, session.Env, sortedEnvironment(mockRoute.Env))
	require.Len(t, mockRoute.Connections, 1)
	require.Equal(t, "main", mockRoute.Connections[0].ID)
	require.Equal(t, "in-process", mockRoute.Connections[0].Transport)
	require.Empty(t, mockRoute.Connections[0].Command)
	require.Empty(t, mockRoute.Connections[0].Args)
	require.Empty(t, mockRoute.Connections[0].Launcher)
	var manifest struct{ Provenance string }
	data, err = os.ReadFile(filepath.Join(descriptor.Source.Path, "manifest.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, "synthetic", manifest.Provenance)
	report := closeSession(t, session)
	require.Equal(t, "abandoned", report.Status)
}

func TestInProcessRunPublishesEmbeddedEvidence(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	require.Equal(t, agenttestify.MockTransportInProcess, run.Start.Transport)
	require.NotEqual(t, session.Workspace, run.Start.Workspace)
	require.DirExists(t, run.Start.Workspace)
	require.DirExists(t, run.Start.ProtocolCwd)
	routeRoot := filepath.Join(session.Dir, "runtime", "routes", "mock")
	executionRoot := filepath.Join(session.Dir, "runtime", "executions", run.Start.ExecutionID)
	wantEnv := make([]string, len(session.Env))
	for i, entry := range session.Env {
		wantEnv[i] = strings.ReplaceAll(entry, routeRoot, executionRoot)
	}
	require.Equal(t, wantEnv, run.Environment())

	output := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(run.Reader)
		output <- data
	}()
	closeDone := run.CloseInputWhenComplete(run.Writer.Close)
	require.NoError(t, <-closeDone)
	result, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	require.True(t, result.Execution.Complete)
	require.Len(t, result.Execution.Connections, 1)
	connection := result.Execution.Connections[0]
	require.Equal(t, agenttestify.MockTransportInProcess, connection.Transport)
	require.Equal(t, agenttestify.ConnectionStatusPassed, connection.Status)
	require.True(t, connection.FullConsumption)
	require.True(t, connection.ClientEOF)
	require.False(t, connection.NativeLaunched)
	require.True(t, connection.CleanupComplete)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
	require.Contains(t, string(<-output), `"notice"`)
	require.NoError(t, run.CloseSDK())

	report := closeSession(t, session)
	require.Equal(t, "passed", report.Status)
	require.Len(t, report.Executions, 1)
	require.Equal(t, "passed", report.Executions[0].Status)
	require.Equal(t, []ConnectionReport{{ID: "main", Status: "passed"}}, report.Executions[0].Connections)
}

func TestInProcessRunReportsScriptedTimeout(t *testing.T) {
	scenario := strings.Replace(noticeScenario, `"version":1`, `"version":1,"timeoutMs":25`, 1)
	session := prepareSession(t, "codex", scenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	result, err := run.Wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusFailed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.Equal(t, agenttestify.FailureClassTimeout, result.Execution.Connections[0].Failure)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestInProcessRunReportsScriptedDisconnect(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"disconnect":true}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Writer.Close())
	result, err := run.Wait(ctx)
	require.ErrorIs(t, err, ErrDisconnected)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusFailed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.Equal(t, agenttestify.FailureClassStrictReplay, result.Execution.Connections[0].Failure)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestEmbeddedConnectionHasNoExecutableIdentity(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	command, args, err := session.Command("main")
	require.Empty(t, command)
	require.Empty(t, args)
	require.ErrorContains(t, err, "no executable command")
	launcher, err := session.Launcher("main")
	require.Empty(t, launcher)
	require.ErrorContains(t, err, "no launcher")
	require.Equal(t, "abandoned", closeSession(t, session).Status)
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
