package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultNoticesBindSessionExecutionAndConnection(t *testing.T) {
	fixture := resultNoticeFixture(t)
	require.NoError(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence))

	t.Run("cross session", func(t *testing.T) {
		fixture := resultNoticeFixture(t)
		notices := fixture.readNotices(t)
		notices[1].SessionID = "other-session"
		fixture.writeNotices(t, notices)
		require.ErrorContains(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence), "identity/status")
	})
	t.Run("cross execution", func(t *testing.T) {
		fixture := resultNoticeFixture(t)
		notices := fixture.readNotices(t)
		notices[1].ExecutionID = "other-execution"
		fixture.writeNotices(t, notices)
		require.ErrorContains(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence), "identity/status")
	})
	t.Run("cross connection", func(t *testing.T) {
		fixture := resultNoticeFixture(t)
		notices := fixture.readNotices(t)
		notices[1].ConnectionID = "probe"
		fixture.writeNotices(t, notices)
		require.ErrorContains(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence), "identity/status")
	})
	t.Run("missing receipt", func(t *testing.T) {
		fixture := resultNoticeFixture(t)
		require.NoError(t, os.Remove(fixture.receipt))
		require.ErrorContains(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence), "evidence is unavailable")
	})
	t.Run("material mismatch", func(t *testing.T) {
		fixture := resultNoticeFixture(t)
		notices := fixture.readNotices(t)
		notices[1].Material.RecordingSHA256 = "different"
		fixture.writeNotices(t, notices)
		require.ErrorContains(t, validateResultNotices(fixture.logs, fixture.session, fixture.pkg, fixture.evidence), "material differs")
	})
}

type noticeFixture struct {
	session  *preparedSession
	pkg      *materialPackage
	evidence *sessionEvidence
	logs     map[string]string
	path     string
	receipt  string
}

func resultNoticeFixture(t *testing.T) *noticeFixture {
	t.Helper()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session")
	executionDir := filepath.Join(sessionDir, "executions", "execution-1")
	materialDir := filepath.Join(sessionDir, "materials", "material-1")
	for _, dir := range []string{executionDir, materialDir} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	sourcePath := filepath.Join(sessionDir, "source", "plan.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o700))
	require.NoError(t, os.WriteFile(sourcePath, []byte(`{"provider":"codex"}`), 0o600))
	session := &preparedSession{
		SchemaVersion: preparedSessionSchema,
		SessionID:     "session-1",
		AgentType:     "codex",
		Source: preparedSource{
			Kind: "case", ID: "SDK-CASE-001", Path: sourcePath,
			SHA256: digest([]byte(`{"provider":"codex"}`)), Provenance: "official",
		},
		Dir: sessionDir,
		Mock: preparedRoute{Connections: []preparedConnection{
			{ID: "main", Command: "/tool", Args: []string{"agent", "mock", "--session-dir", sessionDir, "--connection", "main"}},
		}},
	}
	pkg := &materialPackage{
		Root: root,
		Material: materialDescriptor{
			ID: "material-1", Path: materialDir, Provenance: "builtin",
			PlanSHA256: "plan", ManifestSHA256: "manifest", RecordingSHA256: "recording",
			Files: map[string]string{"remote-probe/raw/raw.jsonl": "probe"},
		},
		Plan: materialPlan{Provider: "codex"},
	}
	receipt := filepath.Join(executionDir, "receipts", "main.json")
	processReceipt := filepath.Join(executionDir, "receipts", "main.process.json")
	result := filepath.Join(executionDir, "receipts", "main.result.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(receipt), 0o700))
	writeJSONTest(t, receipt, map[string]any{
		"version": 1, "status": "passed", "provider": "codex",
		"recordingSHA256": pkg.Material.RecordingSHA256,
		"planSHA256":      pkg.Material.PlanSHA256,
		"fullConsumption": true, "clientEOF": true,
		"cleanupComplete": true, "nativeLaunched": false,
	})
	writeJSONTest(t, processReceipt, map[string]any{
		"schemaVersion": 1, "sessionId": session.SessionID,
		"executionId": "execution-1", "connectionId": "main",
		"status": "passed", "exitCode": 0,
	})
	writeJSONTest(t, result, map[string]any{})
	executionPath := filepath.Join(executionDir, "execution.json")
	execution := executionDescriptor{
		SchemaVersion: preparedSessionSchema,
		ExecutionID:   "execution-1",
		SessionID:     session.SessionID,
		Command:       "mock",
		Source:        session.Source,
		Provenance:    pkg.Material.Provenance,
		Executor:      "external-sdk",
		Target:        "replay",
		Connections: []executionEntry{{
			ID: "main", Command: "/tool",
			Args:     []string{"agent", "mock", "--session-dir", sessionDir, "--connection", "main"},
			Material: materialDir, PlanSHA256: pkg.Material.PlanSHA256,
			ManifestSHA256: pkg.Material.ManifestSHA256, RecordingSHA256: pkg.Material.RecordingSHA256,
			Receipt: receipt, ProcessReceipt: processReceipt,
		}},
	}
	executionBytes, err := json.Marshal(execution)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(executionPath, executionBytes, 0o600))
	notice := resultNotice{
		SchemaVersion: 1, SessionID: session.SessionID, Command: "mock",
		ConnectionID: "main", Execution: executionPath,
		ExecutionID: "execution-1", ExecutionSHA256: digest(executionBytes),
		Material: resultMaterial{
			ID: pkg.Material.ID, Path: materialDir,
			PlanSHA256: pkg.Material.PlanSHA256, ManifestSHA256: pkg.Material.ManifestSHA256,
			RecordingSHA256: pkg.Material.RecordingSHA256, Provenance: pkg.Material.Provenance,
		},
		ProcessID: 42, ProcessReceipt: processReceipt, Receipt: receipt, Result: result,
		Status: "running",
	}
	fixture := &noticeFixture{
		session: session, pkg: pkg,
		evidence: &sessionEvidence{
			SchemaVersion: preparedSessionSchema,
			SessionID:     session.SessionID,
			Status:        "passed",
			Index: evidenceIndex{
				SchemaVersion: preparedSessionSchema,
				SessionID:     session.SessionID,
				Executions: []evidenceIndexEntry{{
					ID: "execution-1", Command: "mock", Status: "passed",
					Connections: []evidenceStatus{{ID: "main", Status: "passed"}},
				}},
			},
			Executions: []executionDescriptor{execution},
		},
		logs:    map[string]string{"main": filepath.Join(root, "result-main.jsonl")},
		path:    filepath.Join(root, "result-main.jsonl"),
		receipt: receipt,
	}
	done := notice
	done.Status = "passed"
	fixture.writeNotices(t, []resultNotice{notice, done})
	return fixture
}

func (f *noticeFixture) readNotices(t *testing.T) []resultNotice {
	t.Helper()
	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	var notices []resultNotice
	for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte("\n")) {
		var notice resultNotice
		require.NoError(t, json.Unmarshal(line, &notice))
		notices = append(notices, notice)
	}
	return notices
}

func (f *noticeFixture) writeNotices(t *testing.T, notices []resultNotice) {
	t.Helper()
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, notice := range notices {
		require.NoError(t, encoder.Encode(notice))
	}
	require.NoError(t, os.WriteFile(f.path, data.Bytes(), 0o600))
}
