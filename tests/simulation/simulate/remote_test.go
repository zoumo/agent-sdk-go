package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	codexsim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/codex/go"
	agenttestify "github.com/zoumo/agent-sdk-testify"
)

func TestRemoteRequiresDeclaredMainAndProbe(t *testing.T) {
	root := t.TempDir()
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"wire":{"method":"notice","type":"notice"}}]}`
	session, err := mocktest.Prepare(t.TempDir(), "codex", []byte(scenario), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })

	service, err := startMockRemote(context.Background(), nil, root)
	require.ErrorContains(t, err, "prepared fixture")
	require.Nil(t, service)

	service, err = startMockRemote(context.Background(), session, root)
	require.ErrorContains(t, err, "main and probe")
	require.Nil(t, service)
}

func TestRemoteAuthenticatesOnlyDeclaredConnectionPaths(t *testing.T) {
	session := prepareRemoteSession(t)

	service, err := startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.close() })
	require.Equal(t, []string{service.executionID}, session.Runner.ExecutionIDs())
	report, err := session.Fixture.ExecutionReport(service.executionID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusOpen, report.Status)
	require.Len(t, report.Connections, 2)
	for _, connection := range report.Connections {
		require.Equal(t, agenttestify.ConnectionStatusRunning, connection.Status)
		require.Equal(t, agenttestify.MockTransportInProcess, connection.Transport)
		require.False(t, connection.NativeLaunched)
		require.Nil(t, connection.ExitCode)
	}
	client := &http.Client{Timeout: 5 * time.Second}

	mainURL := strings.Replace(service.endpoint.Connections["main"].URL, "ws://", "http://", 1)
	unknown, err := client.Get(mainURL + "/extra")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, unknown.StatusCode)
	require.NoError(t, unknown.Body.Close())

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		mainURL, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer wrong")
	unauthorized, err := client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, unauthorized.StatusCode)
	require.NoError(t, unauthorized.Body.Close())

	require.ErrorContains(t, service.close(), "0/2 required connections")
}

func TestRemoteStartRejectsPendingExecutionAndStartsFresh(t *testing.T) {
	session := prepareRemoteSession(t)
	args := []string{"app-server", "--listen", "stdio://"}
	previous, err := session.Runner.NewRun(t.Context(), mocktest.RunOptions{
		Connection: "main",
		NativeArgs: args,
	})
	require.NoError(t, err)
	previousID := previous.Start.ExecutionID

	service, err := startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.ErrorContains(t, err, "fixture has unfinished execution "+previousID)
	require.Nil(t, service)

	probe, err := session.Runner.NewRun(t.Context(), mocktest.RunOptions{
		Connection:  "probe",
		ExecutionID: previousID,
		NativeArgs:  args,
	})
	require.NoError(t, err)
	require.NoError(t, previous.Abort())
	require.NoError(t, probe.Abort())
	_, err = previous.Wait(t.Context())
	require.Error(t, err)
	_, err = probe.Wait(t.Context())
	require.Error(t, err)
	require.NoError(t, previous.CloseSDK())
	require.NoError(t, probe.CloseSDK())
	previousReport, err := session.Fixture.ExecutionReport(previousID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusFailed, previousReport.Status)
	require.True(t, previousReport.Complete)

	service, err = startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.close() })
	require.NotEqual(t, previousID, service.executionID)
	require.ElementsMatch(t, []string{previousID, service.executionID}, session.Runner.ExecutionIDs())
	report, err := session.Fixture.ExecutionReport(service.executionID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusOpen, report.Status)
	require.Len(t, report.Connections, 2)
	for _, connection := range report.Connections {
		require.Equal(t, agenttestify.ConnectionStatusRunning, connection.Status)
	}
	require.ErrorContains(t, service.close(), "0/2 required connections")
}

func TestRemoteProbeRequiresExplicitExecutionID(t *testing.T) {
	session := prepareRemoteSession(t)
	args := []string{"app-server", "--listen", "stdio://"}
	mainRun, err := session.Runner.NewRun(t.Context(), mocktest.RunOptions{
		Connection: "main",
		NativeArgs: args,
	})
	require.NoError(t, err)

	probeRun, err := session.Runner.NewRun(t.Context(), mocktest.RunOptions{
		Connection: "probe",
		NativeArgs: args,
	})
	require.ErrorContains(t, err, "fixture has unfinished execution")
	require.Nil(t, probeRun)
	require.Equal(t, []string{mainRun.Start.ExecutionID}, session.Runner.ExecutionIDs())

	require.NoError(t, mainRun.Abort())
	_, err = mainRun.Wait(t.Context())
	require.Error(t, err)
}

func TestRemoteBridgeDrainFailureRetainsTypedEvidence(t *testing.T) {
	session := prepareRemoteSession(t)
	service, err := startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.close() })

	for _, id := range remoteConnectionIDs {
		endpoint := service.endpoint.Connections[id]
		conn, _, err := websocket.Dial(t.Context(), endpoint.URL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + endpoint.BearerToken}},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.opened["main"] && service.opened["probe"]
	}, time.Second, 10*time.Millisecond)

	require.ErrorContains(t, service.close(), "did not drain before cleanup")
	report, err := session.Fixture.ExecutionReport(service.executionID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusFailed, report.Status)
	require.True(t, report.Complete)
	require.Len(t, report.Connections, 2)
	for _, connection := range evidenceExecutionView(report).Connections {
		require.Equal(t, string(agenttestify.ConnectionStatusFailed), connection.Status)
		require.False(t, connection.FullConsumption)
		require.False(t, connection.ClientEOF)
		requireEmbeddedConnectionFacts(t, connection)
		require.NotEmpty(t, connection.ReceiptPath)
		require.NotEmpty(t, connection.ResultPath)
	}

	output := t.TempDir()
	outcome := finalizePreparedSession(t.Context(), session, nil, service.executionID, output, false)
	require.NoError(t, outcome.Error)
	evidence := readSessionEvidence(t, filepath.Join(output, "evidence.json"))
	require.Equal(t, "failed", evidence.Status)
	require.Len(t, evidence.Executions, 1)
	require.True(t, evidence.Executions[0].Complete)
	require.Len(t, evidence.Executions[0].Connections, 2)
}

func TestRemotePassedEvidenceRequiresProbeResult(t *testing.T) {
	session := prepareRemoteSession(t)
	tool, err := discoverTool()
	require.NoError(t, err)
	material := filepath.Join(t.TempDir(), "material")
	require.NoError(t, exportPreparedSession(t.Context(), tool, session, material))

	service, err := startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.close() })

	main := service.endpoint.Connections["main"]
	probe := service.endpoint.Connections["probe"]
	require.NoError(t, codexsim.AcceptRemoteClose(t.Context(), material,
		main.URL, main.BearerToken, probe.URL, probe.BearerToken))
	require.NoError(t, service.close())

	report, err := session.Fixture.ExecutionReport(service.executionID)
	require.NoError(t, err)
	require.Equal(t, agenttestify.ExecutionStatusPassed, report.Status)
	var resultPath string
	for _, connection := range report.Connections {
		if connection.ID == "probe" {
			resultPath = connection.ResultPath
		}
	}
	require.NotEmpty(t, resultPath)
	require.NoError(t, os.Remove(resultPath))

	output := t.TempDir()
	outcome := finalizePreparedSession(t.Context(), session, nil, service.executionID, output, false)
	require.ErrorContains(t, outcome.Error, "mock result")
	require.Equal(t, "unavailable", outcome.Evidence.Status)
	evidence := readSessionEvidence(t, filepath.Join(output, "evidence.json"))
	require.Equal(t, "unavailable", evidence.Status)
}

func prepareRemoteSession(t *testing.T) *preparedSession {
	t.Helper()
	tool, err := discoverTool()
	require.NoError(t, err)
	session, err := prepareSession(t.Context(), tool, sourceSelection{
		Provider: "codex",
		Case:     "SDK-CASE-032",
	}, filepath.Join(t.TempDir(), "session"), "test")
	require.NoError(t, err)
	t.Cleanup(func() {
		if session.Runner != nil {
			require.NoError(t, session.Runner.Close())
		}
		require.NoError(t, cleanupPreparedSession(session.Dir))
	})
	return session
}

func requireEmbeddedConnectionFacts(t *testing.T, connection evidenceConnection) {
	t.Helper()
	require.Equal(t, string(agenttestify.MockTransportInProcess), connection.Transport)
	require.False(t, connection.NativeLaunched)
	require.True(t, connection.CleanupComplete)
	require.Nil(t, connection.ExitCode)
	require.Empty(t, connection.ProcessReceiptPath)
}
