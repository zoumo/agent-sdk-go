package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	agenttestify "github.com/zoumo/agent-sdk-testify"
)

func TestValidateExecutionEvidenceRequiresEmbeddedCompletionFacts(t *testing.T) {
	fixture := executionEvidenceFixture(t)
	require.NoError(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg))

	t.Run("cross fixture", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.FixtureID = "other-fixture"
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "identity differs")
	})
	t.Run("open execution", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.Status = agenttestify.ExecutionStatusOpen
		fixture.report.Complete = false
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "remained open")
	})
	t.Run("material mismatch", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.Material.RecordingSHA256 = "different"
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "material identity differs")
	})
	t.Run("connection inventory", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.Connections = nil
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "connections differ")
	})
	t.Run("process identity", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		exitCode := 0
		fixture.report.Connections[0].Transport = agenttestify.MockTransportProcess
		fixture.report.Connections[0].NativeLaunched = true
		fixture.report.Connections[0].ExitCode = &exitCode
		fixture.report.Connections[0].ProcessReceiptPath = fixture.execution
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "process identity")
	})
	t.Run("partial consumption", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.Connections[0].ClientEOF = false
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "does not prove full consumption")
	})
	t.Run("missing receipt", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		require.NoError(t, os.Remove(fixture.receipt))
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "receipt evidence is unavailable")
	})
	t.Run("failed cleanup", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.Status = agenttestify.ExecutionStatusFailed
		fixture.report.Connections[0].Status = agenttestify.ConnectionStatusFailed
		fixture.report.Connections[0].CleanupComplete = false
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "cleanup is incomplete")
	})
	t.Run("descriptor hash", func(t *testing.T) {
		fixture := executionEvidenceFixture(t)
		fixture.report.ExecutionSHA256 = "different"
		require.ErrorContains(t, validateExecutionEvidence(fixture.report, fixture.session, fixture.pkg), "descriptor hash differs")
	})
}

func TestInspectSessionEvidenceLeavesExecutionlessFixtureAbandoned(t *testing.T) {
	evidence, err := inspectSessionEvidence(&preparedSession{SessionID: "fixture-1"}, "")
	require.NoError(t, err)
	require.Equal(t, preparedSessionSchema, evidence.SchemaVersion)
	require.Equal(t, "fixture-1", evidence.SessionID)
	require.Equal(t, "abandoned", evidence.Status)
	require.Empty(t, evidence.Executions)
}

type executionEvidenceFixtureValues struct {
	session   *preparedSession
	pkg       *materialPackage
	report    *agenttestify.ExecutionReport
	execution string
	receipt   string
}

func executionEvidenceFixture(t *testing.T) *executionEvidenceFixtureValues {
	t.Helper()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session")
	executionDir := filepath.Join(sessionDir, "executions", "execution-1")
	materialDir := filepath.Join(sessionDir, "materials", "material-1")
	require.NoError(t, os.MkdirAll(executionDir, 0o700))
	require.NoError(t, os.MkdirAll(materialDir, 0o700))
	executionPath := filepath.Join(executionDir, "execution.json")
	require.NoError(t, os.WriteFile(executionPath, []byte(`{"schemaVersion":5}`), 0o600))
	executionHash := digest([]byte(`{"schemaVersion":5}`))
	receiptPath := filepath.Join(executionDir, "receipts", "main.json")
	resultPath := filepath.Join(executionDir, "receipts", "main.result.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(receiptPath), 0o700))
	require.NoError(t, os.WriteFile(receiptPath, []byte(`{}`), 0o600))
	require.NoError(t, os.WriteFile(resultPath, []byte(`{}`), 0o600))

	session := &preparedSession{
		SchemaVersion: preparedSessionSchema,
		SessionID:     "fixture-1",
		AgentType:     "codex",
		Dir:           sessionDir,
		Mock: preparedRoute{Connections: []preparedConnection{{
			ID: "main", Transport: "in-process",
		}}},
	}
	pkg := &materialPackage{Material: materialDescriptor{
		ID: "material-1", Kind: "material", Action: "mock", Path: materialDir,
		Provenance: "external", PlanSHA256: "plan", ManifestSHA256: "manifest",
		RecordingSHA256: "recording", Connections: []string{"main"},
	}}
	report := &agenttestify.ExecutionReport{
		FixtureID:       session.SessionID,
		ExecutionID:     "execution-1",
		Status:          agenttestify.ExecutionStatusPassed,
		Complete:        true,
		ExecutionSHA256: executionHash,
		ExecutionPath:   executionPath,
		Material: agenttestify.Material{
			ID: "material-1", Kind: "material", Action: "mock", Path: materialDir,
			Provenance: "external", PlanSHA256: "plan", ManifestSHA256: "manifest",
			RecordingSHA256: "recording", Connections: []string{"main"},
		},
		Connections: []agenttestify.ConnectionReport{{
			ID: "main", Status: agenttestify.ConnectionStatusPassed,
			Transport:       agenttestify.MockTransportInProcess,
			FullConsumption: true,
			ClientEOF:       true,
			CleanupComplete: true,
			ReceiptPath:     receiptPath,
			ResultPath:      resultPath,
		}},
	}
	return &executionEvidenceFixtureValues{
		session: session, pkg: pkg, report: report,
		execution: executionPath, receipt: receiptPath,
	}
}
