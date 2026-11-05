package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
)

type evidenceStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type evidenceIndexEntry struct {
	ID               string           `json:"id"`
	Command          string           `json:"command"`
	DescriptorSHA256 string           `json:"descriptorSha256"`
	Status           string           `json:"status"`
	Connections      []evidenceStatus `json:"connections,omitempty"`
}

type evidenceIndex struct {
	SchemaVersion int                  `json:"schemaVersion"`
	SessionID     string               `json:"sessionId"`
	Executions    []evidenceIndexEntry `json:"executions"`
}

type resultMaterial struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	PlanSHA256      string `json:"planSHA256"`
	ManifestSHA256  string `json:"manifestSHA256"`
	RecordingSHA256 string `json:"recordingSHA256"`
	Provenance      string `json:"provenance"`
}

type resultNotice struct {
	SchemaVersion   int            `json:"schemaVersion"`
	SessionID       string         `json:"sessionId"`
	Command         string         `json:"command"`
	ConnectionID    string         `json:"connectionId"`
	Execution       string         `json:"execution"`
	ExecutionID     string         `json:"executionId"`
	ExecutionSHA256 string         `json:"executionSHA256"`
	Material        resultMaterial `json:"material"`
	ProcessID       int            `json:"processId"`
	ProcessReceipt  string         `json:"processReceipt"`
	Receipt         string         `json:"receipt"`
	Result          string         `json:"result"`
	Status          string         `json:"status"`
}

type executionDescriptor struct {
	SchemaVersion int               `json:"schemaVersion"`
	ExecutionID   string            `json:"executionId"`
	SessionID     string            `json:"sessionId"`
	Command       string            `json:"command"`
	Source        preparedSource    `json:"source"`
	Provenance    string            `json:"provenance"`
	Executor      string            `json:"executor"`
	Target        string            `json:"target"`
	Workspace     string            `json:"workspace"`
	HistoryFiles  map[string]string `json:"historyFiles,omitempty"`
	Connections   []executionEntry  `json:"connections"`
}

type executionEntry struct {
	ID              string   `json:"id"`
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	Material        string   `json:"material,omitempty"`
	PlanSHA256      string   `json:"planSHA256,omitempty"`
	ManifestSHA256  string   `json:"manifestSHA256,omitempty"`
	RecordingSHA256 string   `json:"recordingSHA256,omitempty"`
	Receipt         string   `json:"receipt"`
	ProcessReceipt  string   `json:"processReceipt,omitempty"`
}

type sessionEvidence struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	SessionID       string                `json:"sessionId"`
	Status          string                `json:"status"`
	Index           evidenceIndex         `json:"index"`
	Executions      []executionDescriptor `json:"executions"`
	ValidationError string                `json:"validationError,omitempty"`
}

type evidenceOutcome struct {
	Evidence      *sessionEvidence
	ProtocolError error
	Error         error
}

func inspectSessionEvidence(session *preparedSession) (*sessionEvidence, error) {
	indexPath := filepath.Join(session.Dir, "executions", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("execution index: %w", err)
	}
	evidence := &sessionEvidence{Status: materialStatusUnavailable}
	if err := json.Unmarshal(data, &evidence.Index); err != nil {
		return nil, fmt.Errorf("execution index: %w", err)
	}
	evidence.SchemaVersion = evidence.Index.SchemaVersion
	evidence.SessionID = evidence.Index.SessionID
	if evidence.SchemaVersion != preparedSessionSchema || evidence.SessionID != session.SessionID {
		return nil, errors.New("execution index belongs to a different session")
	}
	if len(evidence.Index.Executions) == 0 {
		evidence.Status = "abandoned"
		return evidence, nil
	}
	if len(evidence.Index.Executions) != 1 {
		return nil, fmt.Errorf("session contains %d mock executions; exactly one is required", len(evidence.Index.Executions))
	}
	indexed := evidence.Index.Executions[0]
	if indexed.ID == "" || filepath.Base(indexed.ID) != indexed.ID || indexed.Command != "mock" || indexed.DescriptorSHA256 == "" {
		return nil, errors.New("execution index identity is incomplete")
	}
	switch indexed.Status {
	case "open", "running":
		return nil, fmt.Errorf("mock execution %s remained %s after cleanup", indexed.ID, indexed.Status)
	case "passed", "failed":
	default:
		return nil, fmt.Errorf("unsupported execution status %q", indexed.Status)
	}
	descriptorPath := filepath.Join(session.Dir, "executions", indexed.ID, "execution.json")
	descriptorHash, err := digestPath(descriptorPath)
	if err != nil {
		return nil, err
	}
	if descriptorHash != indexed.DescriptorSHA256 {
		return nil, errors.New("execution descriptor hash differs from the index")
	}
	descriptorBytes, err := os.ReadFile(descriptorPath)
	if err != nil {
		return nil, err
	}
	var descriptor executionDescriptor
	if err := json.Unmarshal(descriptorBytes, &descriptor); err != nil {
		return nil, fmt.Errorf("execution descriptor: %w", err)
	}
	if descriptor.SchemaVersion != preparedSessionSchema ||
		descriptor.ExecutionID != indexed.ID ||
		descriptor.SessionID != session.SessionID ||
		descriptor.Command != "mock" {
		return nil, errors.New("execution descriptor identity differs from the index")
	}
	if err := validateIndexedConnections(indexed, descriptor); err != nil {
		return nil, err
	}
	evidence.Status = indexed.Status
	evidence.Executions = []executionDescriptor{descriptor}
	return evidence, nil
}

func validateIndexedConnections(indexed evidenceIndexEntry, descriptor executionDescriptor) error {
	if len(indexed.Connections) != len(descriptor.Connections) {
		return errors.New("execution connection inventory differs from the index")
	}
	statuses := make(map[string]string, len(indexed.Connections))
	for _, connection := range indexed.Connections {
		if _, exists := statuses[connection.ID]; exists {
			return fmt.Errorf("execution index repeats connection %s", connection.ID)
		}
		statuses[connection.ID] = connection.Status
	}
	for _, connection := range descriptor.Connections {
		status, ok := statuses[connection.ID]
		if !ok {
			return fmt.Errorf("execution connection %s is absent from the index", connection.ID)
		}
		switch status {
		case "passed", "failed":
		case "pending":
			if indexed.Status == "passed" {
				return fmt.Errorf("passed execution leaves connection %s pending", connection.ID)
			}
		default:
			return fmt.Errorf("execution connection %s has stale status %q", connection.ID, status)
		}
	}
	return nil
}

func finalizePreparedSession(parent context.Context, session *preparedSession, pkg *materialPackage, logs map[string]string, output string, requireExecution bool) *evidenceOutcome {
	outcome := &evidenceOutcome{}
	evidence, evidenceErr := inspectSessionEvidence(session)
	outcome.Evidence = evidence
	if evidenceErr != nil {
		outcome.Error = errors.Join(outcome.Error, evidenceErr)
	} else {
		if requireExecution && len(evidence.Index.Executions) == 0 {
			outcome.ProtocolError = errors.Join(outcome.ProtocolError, errors.New("SDK business passed without a mock execution"))
		}
		if pkg != nil {
			for i, execution := range evidence.Executions {
				if err := validateExecutionEvidence(execution, evidence.Index.Executions[i], session, pkg); err != nil {
					outcome.Error = errors.Join(outcome.Error, err)
				}
			}
		}
		if pkg != nil && logs != nil && len(evidence.Executions) == 1 {
			if err := validateResultNotices(logs, session, pkg, evidence); err != nil {
				outcome.ProtocolError = errors.Join(outcome.ProtocolError, err)
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
	if err := cleanupPreparedSession(session.Dir); err != nil {
		outcome.Error = errors.Join(outcome.Error, err)
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

func validateExecutionEvidence(execution executionDescriptor, indexed evidenceIndexEntry, session *preparedSession, pkg *materialPackage) error {
	if execution.Command != "mock" ||
		execution.Executor != "external-sdk" ||
		execution.Target != "replay" ||
		execution.Provenance != pkg.Material.Provenance ||
		!reflect.DeepEqual(execution.Source, session.Source) {
		return errors.New("execution descriptor identity differs from the prepared session")
	}
	if err := validateExecutionHistory(execution, pkg); err != nil {
		return err
	}
	seen := map[string]bool{}
	statuses := map[string]string{}
	for _, connection := range indexed.Connections {
		statuses[connection.ID] = connection.Status
	}
	for _, entry := range execution.Connections {
		if seen[entry.ID] {
			return fmt.Errorf("execution descriptor repeats connection %s", entry.ID)
		}
		seen[entry.ID] = true
		routeConnection, ok := session.Connection(entry.ID)
		if !ok || entry.Command != routeConnection.Command || !reflect.DeepEqual(entry.Args, routeConnection.Args) {
			return fmt.Errorf("execution descriptor connection %s differs from the route", entry.ID)
		}
		recordingHash := pkg.Material.RecordingSHA256
		if entry.ID == "probe" {
			recordingHash = pkg.Material.Files["remote-probe/raw/raw.jsonl"]
		}
		if entry.PlanSHA256 != pkg.Material.PlanSHA256 ||
			entry.ManifestSHA256 != pkg.Material.ManifestSHA256 ||
			entry.RecordingSHA256 != recordingHash {
			return fmt.Errorf("execution descriptor connection %s differs from the exported material", entry.ID)
		}
		materialPath, err := canonicalPath(entry.Material)
		if err != nil {
			return err
		}
		wantMaterial, err := canonicalPath(pkg.Material.Path)
		if err != nil || materialPath != wantMaterial {
			return fmt.Errorf("execution descriptor connection %s material differs from the export", entry.ID)
		}
		for label, path := range map[string]string{
			"receipt":         entry.Receipt,
			"process receipt": entry.ProcessReceipt,
		} {
			if err := validateSessionEvidencePath(session, path, label); err != nil {
				return err
			}
		}
		if err := validateDurableConnectionReceipt(entry, execution.ExecutionID, session, pkg, statuses[entry.ID]); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(sortedStrings(mapKeys(seen)), session.ConnectionIDs()) {
		return errors.New("execution descriptor connections differ from the session route")
	}
	return nil
}

func validateDurableConnectionReceipt(entry executionEntry, executionID string, session *preparedSession, pkg *materialPackage, status string) error {
	if status != "passed" && status != "failed" {
		return nil
	}
	expectedRecording := pkg.Material.RecordingSHA256
	if entry.ID == "probe" {
		expectedRecording = pkg.Material.Files["remote-probe/raw/raw.jsonl"]
	}
	var receipt map[string]any
	if err := readEvidenceJSON(entry.Receipt, "replay receipt", &receipt); err != nil {
		return err
	}
	if receipt["version"] != float64(1) ||
		receipt["status"] != status ||
		receipt["provider"] != pkg.Plan.Provider ||
		receipt["recordingSHA256"] != expectedRecording ||
		receipt["planSHA256"] != pkg.Material.PlanSHA256 ||
		receipt["error"] != nil {
		return fmt.Errorf("connection %s replay receipt differs from the execution", entry.ID)
	}
	if status == "passed" &&
		(receipt["fullConsumption"] != true || receipt["clientEOF"] != true ||
			receipt["cleanupComplete"] != true || receipt["nativeLaunched"] != false) {
		return fmt.Errorf("connection %s replay receipt is incomplete", entry.ID)
	}
	var process map[string]any
	if err := readEvidenceJSON(entry.ProcessReceipt, "mock process receipt", &process); err != nil {
		return err
	}
	if process["schemaVersion"] != float64(1) ||
		process["sessionId"] != session.SessionID ||
		process["executionId"] != executionID ||
		process["connectionId"] != entry.ID ||
		process["status"] != status {
		return fmt.Errorf("connection %s process receipt differs from the execution", entry.ID)
	}
	if status == "passed" && (process["exitCode"] != float64(0) || process["error"] != nil) {
		return fmt.Errorf("connection %s process receipt is not successful", entry.ID)
	}
	return nil
}

func validateExecutionHistory(execution executionDescriptor, pkg *materialPackage) error {
	for name, digest := range execution.HistoryFiles {
		if pkg.Material.Files[name] != digest {
			return errors.New("execution history bindings differ from selected material")
		}
	}
	expected := map[string]string{}
	maps.Copy(expected, pkg.Manifest.SavedFiles)
	var plan map[string]any
	if len(pkg.AssertionPlan) > 0 && json.Unmarshal(pkg.AssertionPlan, &plan) == nil {
		seed, _ := plan["historySeed"].(map[string]any)
		files, _ := seed["files"].(map[string]any)
		for name, value := range files {
			switch value := value.(type) {
			case string:
				expected[name] = value
			case map[string]any:
				if digest, ok := value["sha256"].(string); ok {
					expected[name] = digest
				}
			}
		}
	}
	for name, digest := range expected {
		if execution.HistoryFiles[name] != digest {
			return errors.New("execution history bindings differ from selected material")
		}
	}
	return nil
}

func validateResultNotices(logs map[string]string, session *preparedSession, pkg *materialPackage, evidence *sessionEvidence) error {
	if evidence == nil || len(evidence.Executions) != 1 || len(evidence.Index.Executions) != 1 {
		return errors.New("result notices require one execution")
	}
	execution := evidence.Executions[0]
	indexed := evidence.Index.Executions[0]
	statuses := map[string]string{}
	for _, connection := range indexed.Connections {
		statuses[connection.ID] = connection.Status
	}
	if len(logs) != len(session.ConnectionIDs()) {
		return errors.New("result notice inventory differs from the session route")
	}
	for _, id := range session.ConnectionIDs() {
		expectedStatus := statuses[id]
		if expectedStatus == "pending" {
			continue
		}
		path, ok := logs[id]
		if !ok {
			return fmt.Errorf("result notice for connection %s is missing", id)
		}
		notices, err := readResultNotices(path)
		if err != nil {
			return fmt.Errorf("connection %s: %w", id, err)
		}
		if len(notices) != 2 {
			return fmt.Errorf("connection %s has %d result notices; want start and completion", id, len(notices))
		}
		start, done := notices[0], notices[1]
		if err := validateResultNotice(start, session, pkg, id, indexed.ID, "running"); err != nil {
			return err
		}
		if err := validateResultNotice(done, session, pkg, id, indexed.ID, expectedStatus); err != nil {
			return err
		}
		if err := validateConnectionReceipts(done, pkg); err != nil {
			return fmt.Errorf("connection %s: %w", id, err)
		}
		start.Status, done.Status = "", ""
		if !reflect.DeepEqual(start, done) {
			return fmt.Errorf("connection %s result identity changed during execution", id)
		}
		if err := validateResultExecution(done, execution); err != nil {
			return fmt.Errorf("connection %s: %w", id, err)
		}
	}
	return nil
}

func readResultNotices(path string) ([]resultNotice, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("result notice log is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var notices []resultNotice
	for scanner.Scan() {
		if len(notices) >= 2 {
			return nil, errors.New("result notice log has extra records")
		}
		var notice resultNotice
		if err := json.Unmarshal(scanner.Bytes(), &notice); err != nil {
			return nil, fmt.Errorf("result notice: %w", err)
		}
		notices = append(notices, notice)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return notices, nil
}

func validateResultNotice(notice resultNotice, session *preparedSession, pkg *materialPackage, id, executionID, status string) error {
	if notice.SchemaVersion != 1 || notice.SessionID != session.SessionID ||
		notice.Command != "mock" || notice.ConnectionID != id ||
		notice.ExecutionID != executionID || notice.ExecutionSHA256 == "" ||
		notice.Status != status || notice.ProcessID <= 0 {
		return fmt.Errorf("result notice identity/status differs for connection %s", id)
	}
	if notice.Material.ID != pkg.Material.ID ||
		notice.Material.Path != pkg.Material.Path ||
		notice.Material.PlanSHA256 != pkg.Material.PlanSHA256 ||
		notice.Material.ManifestSHA256 != pkg.Material.ManifestSHA256 ||
		notice.Material.RecordingSHA256 != pkg.Material.RecordingSHA256 ||
		notice.Material.Provenance != pkg.Material.Provenance {
		return fmt.Errorf("result notice material differs for connection %s", id)
	}
	for label, path := range map[string]string{
		"execution":       notice.Execution,
		"receipt":         notice.Receipt,
		"process receipt": notice.ProcessReceipt,
		"result":          notice.Result,
	} {
		if err := validateSessionEvidencePath(session, path, label); err != nil {
			return err
		}
	}
	hash, err := digestPath(notice.Execution)
	if err != nil || hash != notice.ExecutionSHA256 {
		return fmt.Errorf("execution descriptor hash differs for connection %s", id)
	}
	return nil
}

func validateConnectionReceipts(notice resultNotice, pkg *materialPackage) error {
	expectedRecording := pkg.Material.RecordingSHA256
	if notice.ConnectionID == "probe" {
		expectedRecording = pkg.Material.Files["remote-probe/raw/raw.jsonl"]
	}
	var receipt map[string]any
	if err := readEvidenceJSON(notice.Receipt, "replay receipt", &receipt); err != nil {
		return err
	}
	if receipt["version"] != float64(1) ||
		receipt["status"] != notice.Status ||
		receipt["provider"] != pkg.Plan.Provider ||
		receipt["recordingSHA256"] != expectedRecording ||
		receipt["planSHA256"] != pkg.Material.PlanSHA256 ||
		receipt["error"] != nil {
		return errors.New("replay receipt differs from the result notice")
	}
	if notice.Status == "passed" {
		if receipt["fullConsumption"] != true || receipt["clientEOF"] != true ||
			receipt["cleanupComplete"] != true || receipt["nativeLaunched"] != false {
			return errors.New("replay receipt does not prove full consumption, EOF and cleanup")
		}
	}
	var process map[string]any
	if err := readEvidenceJSON(notice.ProcessReceipt, "mock process receipt", &process); err != nil {
		return err
	}
	if process["schemaVersion"] != float64(1) ||
		process["sessionId"] != notice.SessionID ||
		process["executionId"] != notice.ExecutionID ||
		process["connectionId"] != notice.ConnectionID ||
		process["status"] != notice.Status {
		return errors.New("mock process receipt differs from the result notice")
	}
	if notice.Status == "passed" {
		if process["exitCode"] != float64(0) || process["error"] != nil {
			return errors.New("mock process receipt is not successful")
		}
	}
	return nil
}

func readEvidenceJSON(path, label string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s evidence is unavailable: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a regular file", label)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func validateResultExecution(notice resultNotice, execution executionDescriptor) error {
	if notice.ExecutionID != execution.ExecutionID || notice.ExecutionSHA256 == "" {
		return errors.New("result notice execution identity differs")
	}
	for _, entry := range execution.Connections {
		if entry.ID == notice.ConnectionID {
			if notice.Receipt != entry.Receipt || notice.ProcessReceipt != entry.ProcessReceipt {
				return errors.New("result notice receipt paths differ from the execution")
			}
			return nil
		}
	}
	return fmt.Errorf("result notice connection %s is absent from the execution", notice.ConnectionID)
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

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
