package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	agenttestify "github.com/zoumo/agent-sdk-testify"
)

type resultMaterial struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Action          string   `json:"action"`
	Path            string   `json:"path"`
	PlanSHA256      string   `json:"planSHA256"`
	ManifestSHA256  string   `json:"manifestSHA256"`
	RecordingSHA256 string   `json:"recordingSHA256"`
	Provenance      string   `json:"provenance"`
	Connections     []string `json:"connections"`
}

type evidenceConnection struct {
	ID     string `json:"id"`
	Status string `json:"status"`

	Transport       string `json:"transport"`
	FullConsumption bool   `json:"fullConsumption"`
	ClientEOF       bool   `json:"clientEOF"`
	NativeLaunched  bool   `json:"nativeLaunched"`
	CleanupComplete bool   `json:"cleanupComplete"`
	ExitCode        *int   `json:"exitCode,omitempty"`
	Failure         string `json:"failure,omitempty"`
	Error           string `json:"error,omitempty"`

	ReceiptPath        string `json:"receiptPath,omitempty"`
	ProcessReceiptPath string `json:"processReceiptPath,omitempty"`
	ResultPath         string `json:"resultPath,omitempty"`
}

type evidenceExecution struct {
	ID              string               `json:"id"`
	FixtureID       string               `json:"fixtureId"`
	Command         string               `json:"command"`
	Status          string               `json:"status"`
	Complete        bool                 `json:"complete"`
	Material        resultMaterial       `json:"material"`
	ExecutionSHA256 string               `json:"executionSha256"`
	ExecutionPath   string               `json:"executionPath"`
	Connections     []evidenceConnection `json:"connections"`
}

type sessionEvidence struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	SessionID       string              `json:"sessionId"`
	Status          string              `json:"status"`
	Executions      []evidenceExecution `json:"executions"`
	ValidationError string              `json:"validationError,omitempty"`
}

type evidenceOutcome struct {
	Evidence      *sessionEvidence
	ProtocolError error
	Error         error
}

func inspectSessionEvidence(session *preparedSession, executionID string) (*sessionEvidence, error) {
	evidence := &sessionEvidence{
		SchemaVersion: preparedSessionSchema,
		SessionID:     session.SessionID,
		Status:        "abandoned",
	}
	if executionID == "" {
		return evidence, nil
	}
	if session.Fixture == nil {
		return evidence, errors.New("prepared fixture is unavailable")
	}
	report, err := session.Fixture.ExecutionReport(executionID)
	if err != nil {
		evidence.Status = "unavailable"
		return evidence, fmt.Errorf("execution report: %w", err)
	}
	if report == nil {
		evidence.Status = "unavailable"
		return evidence, errors.New("execution report is unavailable")
	}
	evidence.Status = string(report.Status)
	evidence.Executions = []evidenceExecution{evidenceExecutionView(report)}
	return evidence, nil
}

func evidenceExecutionView(report *agenttestify.ExecutionReport) evidenceExecution {
	execution := evidenceExecution{
		ID:              report.ExecutionID,
		FixtureID:       report.FixtureID,
		Command:         "mock",
		Status:          string(report.Status),
		Complete:        report.Complete,
		Material:        evidenceMaterial(report.Material),
		ExecutionSHA256: report.ExecutionSHA256,
		ExecutionPath:   report.ExecutionPath,
		Connections:     make([]evidenceConnection, len(report.Connections)),
	}
	for i, connection := range report.Connections {
		var exitCode *int
		if connection.ExitCode != nil {
			value := *connection.ExitCode
			exitCode = &value
		}
		execution.Connections[i] = evidenceConnection{
			ID:                 connection.ID,
			Status:             string(connection.Status),
			Transport:          string(connection.Transport),
			FullConsumption:    connection.FullConsumption,
			ClientEOF:          connection.ClientEOF,
			NativeLaunched:     connection.NativeLaunched,
			CleanupComplete:    connection.CleanupComplete,
			ExitCode:           exitCode,
			Failure:            string(connection.Failure),
			Error:              connection.Error,
			ReceiptPath:        connection.ReceiptPath,
			ProcessReceiptPath: connection.ProcessReceiptPath,
			ResultPath:         connection.ResultPath,
		}
	}
	return execution
}

func evidenceMaterial(material agenttestify.Material) resultMaterial {
	return resultMaterial{
		ID:              material.ID,
		Kind:            material.Kind,
		Action:          material.Action,
		Path:            material.Path,
		PlanSHA256:      material.PlanSHA256,
		ManifestSHA256:  material.ManifestSHA256,
		RecordingSHA256: material.RecordingSHA256,
		Provenance:      material.Provenance,
		Connections:     append([]string(nil), material.Connections...),
	}
}

func finalizePreparedSession(parent context.Context, session *preparedSession, pkg *materialPackage, executionID, output string, requireExecution bool) *evidenceOutcome {
	outcome := &evidenceOutcome{}
	evidence, evidenceErr := inspectSessionEvidence(session, executionID)
	outcome.Evidence = evidence
	if evidenceErr != nil {
		outcome.Error = errors.Join(outcome.Error, evidenceErr)
	}
	if requireExecution && executionID == "" {
		outcome.ProtocolError = errors.Join(outcome.ProtocolError, errors.New("SDK business passed without a mock execution"))
	}
	if executionID != "" && session.Fixture != nil {
		report, err := session.Fixture.ExecutionReport(executionID)
		if err != nil {
			outcome.Error = errors.Join(outcome.Error, fmt.Errorf("validate execution report: %w", err))
		} else {
			if err := validateExecutionEvidence(report, session, pkg); err != nil {
				outcome.Error = errors.Join(outcome.Error, err)
			}
			if requireExecution && report.Status != agenttestify.ExecutionStatusPassed {
				outcome.ProtocolError = errors.Join(outcome.ProtocolError,
					fmt.Errorf("SDK business passed with mock execution status %s", report.Status))
			}
		}
	}
	if err := writeJSON(filepath.Join(output, "evidence.json"), evidence); err != nil {
		outcome.Error = errors.Join(outcome.Error, fmt.Errorf("write execution evidence: %w", err))
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), lifecycleTimeout)
	defer cancel()
	if cleanupCtx.Err() != nil {
		outcome.Error = errors.Join(outcome.Error, cleanupCtx.Err())
	}
	var cleanupErr error
	if session.Runner != nil {
		cleanupErr = session.Runner.Close()
	}
	cleanupErr = errors.Join(cleanupErr, cleanupPreparedSession(session.Dir))
	if cleanupErr != nil {
		outcome.Error = errors.Join(outcome.Error, cleanupErr)
	}
	if _, err := os.Lstat(session.ResourceRoot); err == nil {
		outcome.Error = errors.Join(outcome.Error, errors.New("cleanup left the session resource root in place"))
	} else if !errors.Is(err, os.ErrNotExist) {
		outcome.Error = errors.Join(outcome.Error, fmt.Errorf("cleanup verification: %w", err))
	}
	if outcome.Error != nil && evidence != nil {
		evidence.ValidationError = outcome.Error.Error()
		if err := writeJSON(filepath.Join(output, "evidence.json"), evidence); err != nil {
			outcome.Error = errors.Join(outcome.Error, fmt.Errorf("rewrite execution evidence: %w", err))
		}
	}
	return outcome
}

func validateExecutionEvidence(report *agenttestify.ExecutionReport, session *preparedSession, pkg *materialPackage) error {
	if report == nil {
		return errors.New("execution report is unavailable")
	}
	if report.FixtureID != session.SessionID || report.ExecutionID == "" ||
		report.ExecutionSHA256 == "" || report.ExecutionPath == "" {
		return errors.New("execution report identity differs from the prepared session")
	}
	switch report.Status {
	case agenttestify.ExecutionStatusOpen, agenttestify.ExecutionStatusRunning:
		return fmt.Errorf("mock execution %s remained %s after cleanup", report.ExecutionID, report.Status)
	case agenttestify.ExecutionStatusPassed, agenttestify.ExecutionStatusFailed:
	default:
		return fmt.Errorf("unsupported execution status %q", report.Status)
	}
	if !report.Complete {
		return fmt.Errorf("mock execution %s is not complete", report.ExecutionID)
	}
	if err := validateSessionEvidencePath(session, report.ExecutionPath, "execution descriptor"); err != nil {
		return err
	}
	hash, err := digestPath(report.ExecutionPath)
	if err != nil || hash != report.ExecutionSHA256 {
		return errors.New("execution descriptor hash differs from the typed report")
	}
	if pkg != nil {
		if err := validateExecutionMaterial(report.Material, pkg, session); err != nil {
			return err
		}
	}
	expected := session.ConnectionIDs()
	got := make([]string, len(report.Connections))
	for i, connection := range report.Connections {
		got[i] = connection.ID
		if err := validateEmbeddedConnection(connection, session); err != nil {
			return err
		}
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, expected) {
		return errors.New("execution report connections differ from the prepared route")
	}
	if report.Status == agenttestify.ExecutionStatusPassed {
		for _, connection := range report.Connections {
			if connection.Status != agenttestify.ConnectionStatusPassed {
				return fmt.Errorf("execution report passes while connection %s is %s", connection.ID, connection.Status)
			}
		}
	}
	return nil
}

func validateExecutionMaterial(material agenttestify.Material, pkg *materialPackage, session *preparedSession) error {
	want := pkg.Material
	if material.ID != want.ID ||
		material.Kind != want.Kind ||
		material.Action != want.Action ||
		material.Provenance != want.Provenance ||
		material.PlanSHA256 != want.PlanSHA256 ||
		material.ManifestSHA256 != want.ManifestSHA256 ||
		material.RecordingSHA256 != want.RecordingSHA256 {
		return errors.New("execution material identity differs from the exported material")
	}
	gotPath, err := canonicalPath(material.Path)
	if err != nil {
		return fmt.Errorf("execution material path: %w", err)
	}
	sessionPath, err := canonicalPath(session.Dir)
	if err != nil {
		return fmt.Errorf("prepared session path: %w", err)
	}
	wantPath, err := canonicalPath(want.Path)
	if err != nil || gotPath != wantPath {
		return errors.New("execution material path differs from the prepared source")
	}
	if !pathWithin(gotPath, sessionPath) {
		return errors.New("execution material is outside the prepared session")
	}
	gotConnections := append([]string(nil), material.Connections...)
	wantConnections := append([]string(nil), want.Connections...)
	sort.Strings(gotConnections)
	sort.Strings(wantConnections)
	if !reflect.DeepEqual(gotConnections, wantConnections) {
		return errors.New("execution material connections differ from the exported material")
	}
	return nil
}

func validateEmbeddedConnection(connection agenttestify.ConnectionReport, session *preparedSession) error {
	if _, ok := session.Connection(connection.ID); !ok {
		return fmt.Errorf("execution report contains undeclared connection %s", connection.ID)
	}
	switch connection.Status {
	case agenttestify.ConnectionStatusPending, agenttestify.ConnectionStatusRunning:
		return fmt.Errorf("connection %s remained %s after cleanup", connection.ID, connection.Status)
	case agenttestify.ConnectionStatusPassed, agenttestify.ConnectionStatusFailed:
	default:
		return fmt.Errorf("connection %s has unsupported status %q", connection.ID, connection.Status)
	}
	if connection.Transport != agenttestify.MockTransportInProcess ||
		connection.NativeLaunched || connection.ExitCode != nil ||
		connection.ProcessReceiptPath != "" {
		return fmt.Errorf("connection %s carries process identity in an embedded execution", connection.ID)
	}
	if err := validateSessionEvidencePath(session, connection.ReceiptPath, "replay receipt"); err != nil {
		return fmt.Errorf("connection %s: %w", connection.ID, err)
	}
	if err := validateSessionEvidencePath(session, connection.ResultPath, "mock result"); err != nil {
		return fmt.Errorf("connection %s: %w", connection.ID, err)
	}
	if connection.Status == agenttestify.ConnectionStatusPassed {
		if !connection.FullConsumption || !connection.ClientEOF || !connection.CleanupComplete {
			return fmt.Errorf("connection %s does not prove full consumption, client EOF and cleanup", connection.ID)
		}
		if connection.Failure != "" || connection.Error != "" {
			return fmt.Errorf("connection %s reports a failure while passed", connection.ID)
		}
	}
	return nil
}

func validateSessionEvidencePath(session *preparedSession, path, label string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s path must be absolute", label)
	}
	clean := filepath.Clean(path)
	if !pathWithin(clean, filepath.Clean(session.Dir)) {
		return fmt.Errorf("%s path is outside the prepared session", label)
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s evidence is unavailable", label)
	}
	return nil
}
