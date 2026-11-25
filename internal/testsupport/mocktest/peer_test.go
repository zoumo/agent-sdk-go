package mocktest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	require.Equal(t, 7, descriptor.SchemaVersion)
	require.Equal(t, session.SessionID, descriptor.FixtureID)
	require.Equal(t, "embedded", descriptor.Tool.Source)
	require.Equal(t, "github.com/zoumo/agent-sdk-testify", descriptor.Tool.Module)
	require.NotEmpty(t, descriptor.Tool.ModuleRevision)
	require.NotEmpty(t, descriptor.Tool.GoVersion)
	require.Empty(t, descriptor.Tool.Command)
	require.Empty(t, descriptor.Tool.Args)
	require.Empty(t, descriptor.Tool.SHA256)
	fixture, err := agenttestify.Open(session.Dir)
	require.NoError(t, err)
	require.FileExists(t, fixture.Source.Path)
	require.Equal(t, "recording", descriptor.Source.Kind)
	require.Equal(t, "external", descriptor.Source.Provenance)
	mockRoute := descriptor.Routes["mock"]
	require.Empty(t, mockRoute.Workspace)
	require.Empty(t, mockRoute.ProtocolCwd)
	require.Empty(t, mockRoute.Env)
	require.Equal(t, session.Workspace, fixture.Routes["mock"].Workspace)
	require.Equal(t, session.ProtocolCwd, fixture.Routes["mock"].ProtocolCwd)
	require.Equal(t, session.Env, sortedEnvironment(fixture.Routes["mock"].Env))
	require.Len(t, mockRoute.Connections, 1)
	require.Equal(t, "main", mockRoute.Connections[0].ID)
	require.Equal(t, "in-process", mockRoute.Connections[0].Transport)
	require.Empty(t, mockRoute.Connections[0].Command)
	require.Empty(t, mockRoute.Connections[0].Args)
	require.Empty(t, mockRoute.Connections[0].Launcher)
	var manifest struct{ Provenance string }
	require.NotNil(t, fixture.Current)
	data, err = os.ReadFile(filepath.Join(fixture.Current.Path, "manifest.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, "synthetic", manifest.Provenance)
	report := closeSession(t, session)
	require.Equal(t, "abandoned", report.Status)
}

func TestFromFixtureRejectsMismatchedEmbeddedIdentity(t *testing.T) {
	prepared := prepareSession(t, "codex", noticeScenario, Options{})
	for _, tc := range []struct {
		name   string
		mutate func(*agenttestify.ToolIdentity)
	}{
		{"source", func(v *agenttestify.ToolIdentity) { v.Source = "executable" }},
		{"module", func(v *agenttestify.ToolIdentity) { v.Module = "other/module" }},
		{"version", func(v *agenttestify.ToolIdentity) { v.Version = "v0.0.0-20261109000000-000000000000" }},
		{"sum", func(v *agenttestify.ToolIdentity) { v.ModuleSum = "h1:wrong" }},
		{"revision", func(v *agenttestify.ToolIdentity) { v.ModuleRevision = "wrong" }},
		{"time", func(v *agenttestify.ToolIdentity) { v.ModuleTime = "wrong" }},
		{"missing Go version", func(v *agenttestify.ToolIdentity) { v.GoVersion = "" }},
		{"invalid Go version", func(v *agenttestify.ToolIdentity) { v.GoVersion = "not-go" }},
		{"command", func(v *agenttestify.ToolIdentity) { v.Command = "fake" }},
		{"args", func(v *agenttestify.ToolIdentity) { v.Args = []string{"fake"} }},
		{"binary hash", func(v *agenttestify.ToolIdentity) { v.SHA256 = "fake" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, err := agenttestify.Open(prepared.Dir)
			require.NoError(t, err)
			tc.mutate(&fixture.Tool)
			session, err := FromFixture(fixture)
			require.Nil(t, session)
			require.ErrorContains(t, err, "embedded testify identity")
		})
	}
	require.Equal(t, "abandoned", closeSession(t, prepared).Status)
}

func TestOpenRejectsLegacyFixtureSchemas(t *testing.T) {
	for _, schemaVersion := range []int{3, 4, 5, 6} {
		t.Run(strconv.Itoa(schemaVersion), func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, "fixture.json"),
				[]byte(`{"schemaVersion":`+strconv.Itoa(schemaVersion)+`}`),
				0o600,
			))

			session, err := Open(dir)
			require.Nil(t, session)
			require.ErrorContains(t, err, "unsupported fixture schemaVersion")
			require.ErrorContains(t, err, "schema 7 is required")
		})
	}
}

func TestOpenAndFromFixturePreservePreparedOwnership(t *testing.T) {
	prepared := prepareSession(t, "claude", noticeScenario, Options{})

	opened, err := Open(prepared.Dir)
	require.NoError(t, err)
	require.Equal(t, prepared.SessionID, opened.SessionID)
	require.Equal(t, prepared.ConnectionIDs(), opened.ConnectionIDs())

	attached, err := FromFixture(opened.fixture)
	require.NoError(t, err)
	require.Equal(t, prepared.SessionID, attached.SessionID)
	require.Equal(t, prepared.ConnectionIDs(), attached.ConnectionIDs())

	require.NoError(t, attached.Close())
	require.NoError(t, opened.Close())
	require.DirExists(t, prepared.Dir, "attach must not take directory ownership")
	require.NoError(t, prepared.Close())
	require.NoDirExists(t, prepared.Dir)
}

func TestInProcessRunPublishesEmbeddedEvidence(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			session := prepareSession(t, provider, noticeScenario, Options{})
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
			require.Empty(t, result.ProcessReceiptPath)
			require.Contains(t, string(<-output), `"notice"`)
			require.NoError(t, run.CloseSDK())

			report := closeSession(t, session)
			require.Equal(t, "passed", report.Status)
			require.Len(t, report.Executions, 1)
			require.Equal(t, "passed", report.Executions[0].Status)
			require.Equal(t, []ConnectionReport{{ID: "main", Status: "passed"}}, report.Executions[0].Connections)
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), `"command":`)
		})
	}
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

func TestInProcessRunReportsAbort(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":1,"method":"initialize","params":{}}}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)

	require.NoError(t, run.Abort())
	require.NoError(t, run.Abort())
	result, err := run.Wait(ctx)
	require.ErrorContains(t, err, "aborted")
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.NotEqual(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.NotEqual(t, agenttestify.ConnectionStatusPassed, result.Execution.Connections[0].Status)
	require.Equal(t, agenttestify.FailureClassAborted, result.Execution.Connections[0].Failure)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestInProcessRunReportsWaitCancellation(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":1,"method":"initialize","params":{}}}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	runCtx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRun()
	run, err := session.NewRun(runCtx, RunOptions{})
	require.NoError(t, err)

	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	result, err := run.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.NotEqual(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.Equal(t, agenttestify.FailureClassCanceled, result.Execution.Connections[0].Failure)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestInProcessRunCancellationUnblocksWriter(t *testing.T) {
	scenario, err := json.Marshal(map[string]any{
		"version":    1,
		"provenance": "synthetic",
		"frames": []map[string]any{{
			"ordinal":   1,
			"direction": "s2c",
			"channel":   "protocol",
			"epoch":     1,
			"wire": map[string]any{
				"method":  "notice",
				"type":    "notice",
				"padding": strings.Repeat("x", 1<<20),
			},
		}},
	})
	require.NoError(t, err)
	session := prepareSession(t, "codex", string(scenario), Options{})
	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	run, err := session.NewRun(runCtx, RunOptions{})
	require.NoError(t, err)

	writeDone := make(chan error, 1)
	wrote := make(chan struct{}, 1)
	go func() {
		chunk := make([]byte, 4<<10)
		for {
			_, writeErr := run.Writer.Write(chunk)
			if writeErr != nil {
				writeDone <- writeErr
				return
			}
			select {
			case wrote <- struct{}{}:
			default:
			}
		}
	}()
	select {
	case <-wrote:
	case <-time.After(3 * time.Second):
		t.Fatal("SDK writer did not make progress")
	}

	waitCtx, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	result, err := run.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.NotEqual(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.Equal(t, agenttestify.FailureClassCanceled, result.Execution.Connections[0].Failure)
	select {
	case writeErr := <-writeDone:
		require.Error(t, writeErr)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock the SDK writer")
	}
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestInProcessRunHalfCloseIsIdempotent(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)

	outputDone := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(io.Discard, run.Reader)
		outputDone <- readErr
	}()
	closeDone := run.CloseInputWhenComplete(func() error {
		return errors.Join(run.Writer.Close(), run.Writer.Close())
	})
	select {
	case closeErr := <-closeDone:
		require.NoError(t, closeErr)
	case <-time.After(3 * time.Second):
		t.Fatal("idempotent half-close blocked")
	}

	result, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	select {
	case outputErr := <-outputDone:
		require.NoError(t, outputErr)
	case <-time.After(3 * time.Second):
		t.Fatal("SDK reader still blocked after final peer EOF")
	}
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "passed", closeSession(t, session).Status)
}

func TestSessionCleanupRejectsActiveRun(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":1,"method":"initialize","params":{}}}]}`
	session := prepareSession(t, "claude", scenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)

	require.ErrorContains(t, session.Close(), "active runs")
	require.DirExists(t, session.Dir)

	require.NoError(t, run.Abort())
	result, err := run.Wait(ctx)
	require.Error(t, err)
	require.NotNil(t, result)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestRunReportOutlivesFixtureCleanup(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)

	outputDone := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(io.Discard, run.Reader)
		outputDone <- readErr
	}()
	require.NoError(t, <-run.CloseInputWhenComplete(run.Writer.Close))
	first, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NoError(t, <-outputDone)
	require.NoError(t, run.CloseSDK())

	materialID := first.Execution.Material.ID
	first.Execution.Connections[0].ID = "mutated"
	first.Execution.Material.Connections[0] = "mutated"
	report := closeSession(t, session)
	require.Equal(t, "passed", report.Status)

	repeated, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, materialID, repeated.Execution.Material.ID)
	require.Equal(t, "main", repeated.Execution.Connections[0].ID)
	require.Equal(t, []string{"main"}, repeated.Execution.Material.Connections)
	require.True(t, repeated.Execution.Complete)
	require.True(t, repeated.Execution.Connections[0].CleanupComplete)
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
	require.Error(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusFailed, result.Execution.Status)
	require.Len(t, result.Execution.Connections, 1)
	require.Equal(t, agenttestify.FailureClassStrictReplay, result.Execution.Connections[0].Failure)
	requireEmbeddedFailure(t, result.Execution.Connections[0])
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestCloseInputWhenCompleteJoinsCallbackError(t *testing.T) {
	want := errors.New("completion failed")
	session := prepareSession(t, "codex", noticeScenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	done := run.CloseInputWhenComplete(func() error {
		_ = run.Writer.Close()
		return want
	})
	require.ErrorIs(t, <-done, want)
	result, err := run.Wait(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Execution)
	require.Equal(t, agenttestify.ExecutionStatusPassed, result.Execution.Status)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "passed", closeSession(t, session).Status)
}

func TestEmbeddedConnectionHasNoExecutableIdentity(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	require.Equal(t, agenttestify.MockTransportInProcess, run.Start.Transport)
	require.Empty(t, run.Start.NativeArgs)
	require.NoError(t, run.Writer.Close())
	result, err := run.Wait(ctx)
	require.NoError(t, err)
	connection := result.Execution.Connections[0]
	require.False(t, connection.NativeLaunched)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
	require.Empty(t, result.ProcessReceiptPath)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "passed", closeSession(t, session).Status)
}

func requireEmbeddedFailure(t *testing.T, connection agenttestify.ConnectionReport) {
	t.Helper()
	require.Equal(t, agenttestify.ConnectionStatusFailed, connection.Status)
	require.NotEmpty(t, connection.Failure)
	require.False(t, connection.NativeLaunched)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}

func TestSessionCleanupRacesRunStartup(t *testing.T) {
	for range 24 {
		session := prepareSession(t, "codex", noticeScenario, Options{})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		type started struct {
			run *Run
			err error
		}
		ready := make(chan struct{})
		done := make(chan started, 1)
		go func() {
			close(ready)
			run, err := session.NewRun(ctx, RunOptions{})
			done <- started{run, err}
		}()
		<-ready
		closeErr := session.Close()
		outcome := <-done
		if outcome.run != nil {
			require.ErrorContains(t, closeErr, "active runs")
			require.NoError(t, outcome.run.Abort())
			_, err := outcome.run.Wait(ctx)
			require.Error(t, err)
			require.NoError(t, outcome.run.CloseSDK())
		} else {
			require.Error(t, outcome.err)
			if closeErr == nil {
				require.ErrorContains(t, outcome.err, "session is closed")
			}
		}
		require.NoError(t, session.Close())
		require.NoDirExists(t, session.Dir)
		cancel()
	}
}

func TestSessionCleanupAfterFailedRunStartup(t *testing.T) {
	session := prepareSession(t, "codex", noticeScenario, Options{})
	run, err := session.NewRun(context.Background(), RunOptions{Connection: "undeclared"})
	require.Error(t, err)
	if run != nil {
		require.NoError(t, run.Abort())
		_, err = run.Wait(context.Background())
		require.Error(t, err)
		require.NoError(t, run.CloseSDK())
	}
	require.NoError(t, session.Close())
	require.NoDirExists(t, session.Dir)
}

func TestInProcessRunWaitRetainsStrictFailure(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":1,"method":"initialize","params":{}}}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	_, err = io.WriteString(run.Writer, "{\"id\":1,\"method\":\"wrong\",\"params\":{}}\n")
	require.NoError(t, err)
	require.NoError(t, run.Writer.Close())
	first, err := run.Wait(ctx)
	require.Error(t, err)
	require.NotNil(t, first.Execution)
	require.Equal(t, agenttestify.ExecutionStatusFailed, first.Execution.Status)
	require.Equal(t, agenttestify.FailureClassStrictReplay, first.Execution.Connections[0].Failure)
	repeated, err := run.Wait(ctx)
	require.Error(t, err)
	require.Equal(t, first.Execution, repeated.Execution)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}

func TestInProcessRunReportsContextCancellation(t *testing.T) {
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"c2s","channel":"protocol","epoch":1,"wire":{"id":1,"method":"initialize","params":{}}}]}`
	session := prepareSession(t, "codex", scenario, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := session.NewRun(ctx, RunOptions{})
	require.NoError(t, err)
	cancel()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWait()
	result, err := run.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, agenttestify.ExecutionStatusFailed, result.Execution.Status)
	require.Equal(t, agenttestify.FailureClassCanceled, result.Execution.Connections[0].Failure)
	require.NoError(t, run.CloseSDK())
	require.Equal(t, "failed", closeSession(t, session).Status)
}
