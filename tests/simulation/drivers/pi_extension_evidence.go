package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var piExtensionAliasPattern = regexp.MustCompile(`^/tmp/agent-sdk-pi-replay-[0-9a-f]{32}/session\.ts$`)

func validPiExtensionAlias(path string) bool { return piExtensionAliasPattern.MatchString(path) }

type piReplayMaterial struct {
	alias, rawSHA256 string
	frames           int
}

func readPiReplayMaterial(root, code string) (piReplayMaterial, error) {
	var plan struct{ AgentType, CaseID, DriverProfile string }
	if err := readPiExtensionJSON(filepath.Join(root, "plan.json"), &plan); err != nil {
		return piReplayMaterial{}, fmt.Errorf("read Pi extension recording plan: %w", err)
	}
	if plan.AgentType != "pi" || plan.CaseID != "pi-controlled-"+code || plan.DriverProfile != "consumer" {
		return piReplayMaterial{}, errors.New("pi: extension recording does not match the requested case")
	}
	var metadata struct{ Provenance, RecordingSHA256 string }
	if err := readPiExtensionJSON(filepath.Join(root, "material.json"), &metadata); err != nil {
		return piReplayMaterial{}, fmt.Errorf("read Pi extension material identity: %w", err)
	}
	if metadata.Provenance != "consumer" {
		return piReplayMaterial{}, errors.New("pi: extension replay requires a consumer recording")
	}
	file, err := os.Open(filepath.Join(root, "raw", "raw.jsonl"))
	if err != nil {
		return piReplayMaterial{}, fmt.Errorf("read native Pi recording: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	lines := bufio.NewScanner(io.TeeReader(file, hash))
	lines.Buffer(make([]byte, 4096), 64<<20)
	material := piReplayMaterial{}
	for lines.Scan() {
		var frame struct {
			Ordinal int
			Wire    struct {
				Type, Command string
				Success       bool
				Data          json.RawMessage
			}
		}
		material.frames++
		if err := json.Unmarshal(lines.Bytes(), &frame); err != nil {
			return piReplayMaterial{}, fmt.Errorf("decode Pi recording frame %d: %w", material.frames, err)
		}
		if frame.Ordinal != material.frames {
			return piReplayMaterial{}, errors.New("pi: recording frame ordinals are incomplete")
		}
		if frame.Wire.Type == "response" && frame.Wire.Command == "get_commands" && frame.Wire.Success {
			alias, err := extensionAliasFromCommands(frame.Wire.Data)
			if err != nil {
				return piReplayMaterial{}, err
			}
			if material.alias != "" {
				return piReplayMaterial{}, errors.New("pi: recording has multiple extension handshake responses")
			}
			material.alias = alias
		}
	}
	if err := lines.Err(); err != nil {
		return piReplayMaterial{}, err
	}
	material.rawSHA256 = hex.EncodeToString(hash.Sum(nil))
	if material.rawSHA256 != metadata.RecordingSHA256 {
		return piReplayMaterial{}, errors.New("pi: recording bytes do not match the material hash")
	}
	if material.alias == "" {
		return piReplayMaterial{}, errors.New("pi: recording has no unified SDK extension handshake")
	}
	return material, nil
}

func extensionAliasFromCommands(raw json.RawMessage) (string, error) {
	var data struct {
		Commands []struct {
			Name, Source string
			SourceInfo   struct{ Path string }
		}
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("decode Pi extension handshake response: %w", err)
	}
	var alias string
	for _, command := range data.Commands {
		if command.Name == "agent-sdk-session-ready" && command.Source == "extension" {
			if alias != "" || !validPiExtensionAlias(command.SourceInfo.Path) {
				return "", errors.New("pi: extension handshake has an invalid or duplicate replay alias")
			}
			alias = command.SourceInfo.Path
		}
	}
	if alias == "" {
		return "", errors.New("pi: extension handshake did not report the SDK extension")
	}
	return alias, nil
}

type piReplayNotice struct {
	SchemaVersion                       int
	Command, Status, ConnectionStatus   string
	ExecutionStatus                     string
	ExecutionComplete                   bool
	ExecutionID, FixtureID, MaterialID  string
	ConnectionID, ExecutionSHA256       string
	Execution, Result, Receipt          string
	ProcessReceipt, Error, FailureClass string
	Material                            struct{ ID, Provenance, RecordingSHA256 string }
}

type piReplayReceipt struct {
	SchemaVersion, Frames                         int
	Status, Provider, Provenance, RecordingSHA256 string
	ExecutionID, ConnectionID, MaterialID         string
	FullConsumption, ClientEOF, CleanupComplete   bool
	NativeLaunched                                bool
	ExitCode                                      *int
}

func verifyPiReplayResult(fixture, output string, material piReplayMaterial) error {
	start, final, err := readPiReplayNotices(filepath.Join(output, "result.jsonl"))
	if err != nil {
		return err
	}
	if start.SchemaVersion != 3 || final.SchemaVersion != 3 || start.Command != "mock" || final.Command != "mock" ||
		start.Status != "running" || start.FixtureID == "" || start.FixtureID != final.FixtureID ||
		!validPiReplayID(start.ExecutionID) || start.ExecutionID != final.ExecutionID ||
		!validPiReplayID(start.MaterialID) || start.MaterialID != final.MaterialID ||
		start.ConnectionID != "main" || final.ConnectionID != "main" || start.ExecutionSHA256 != final.ExecutionSHA256 ||
		final.Material.ID != final.MaterialID || final.Material.Provenance != "consumer" || final.Material.RecordingSHA256 != material.rawSHA256 {
		return errors.New("testify: mock notifications disagree with the requested Pi recording")
	}
	if final.Status != "passed" || final.ConnectionStatus != "passed" || final.ExecutionStatus != "passed" || !final.ExecutionComplete {
		return fmt.Errorf("strict Testify mock failed (%s): %s", final.FailureClass, final.Error)
	}
	pkg := filepath.Join(fixture, "packages", final.MaterialID)
	execution := filepath.Join("executions", final.ExecutionID)
	if final.Execution != filepath.Join(execution, "execution.json") || final.Result != filepath.Join(execution, "receipts", "main.result.json") ||
		final.Receipt != filepath.Join(execution, "receipts", "main.json") || final.ProcessReceipt != filepath.Join(execution, "receipts", "main.process.json") {
		return errors.New("testify: mock reported unexpected durable evidence paths")
	}
	descriptor, err := os.ReadFile(filepath.Join(pkg, final.Execution))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(descriptor)
	if hex.EncodeToString(digest[:]) != final.ExecutionSHA256 {
		return errors.New("testify: mock execution descriptor hash mismatch")
	}
	var result piReplayNotice
	if err := readPiExtensionJSON(filepath.Join(pkg, final.Result), &result); err != nil {
		return err
	}
	if result != final {
		return errors.New("testify: mock durable result differs from the final notification")
	}
	return copyPiReplayReceipts(pkg, output, final, material)
}

func readPiReplayNotices(path string) (start, final piReplayNotice, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return start, final, fmt.Errorf("read Testify mock notifications: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&start); err != nil {
		return start, final, fmt.Errorf("read Testify mock start notification: %w", err)
	}
	if err := dec.Decode(&final); err != nil {
		return start, final, fmt.Errorf("read Testify mock final notification: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return start, final, errors.New("testify: mock emitted extra or invalid notifications")
	}
	return start, final, nil
}

func validPiReplayID(id string) bool {
	_, err := hex.DecodeString(id)
	return len(id) == 32 && err == nil
}

func copyPiReplayReceipts(pkg, output string, final piReplayNotice, material piReplayMaterial) error {
	data, err := os.ReadFile(filepath.Join(pkg, final.Receipt))
	if err != nil {
		return err
	}
	var protocol piReplayReceipt
	if err := json.Unmarshal(data, &protocol); err != nil {
		return err
	}
	if protocol.SchemaVersion != 2 || protocol.Status != "passed" || protocol.Provider != "pi" || protocol.Provenance != "consumer" ||
		protocol.RecordingSHA256 != material.rawSHA256 || protocol.Frames != material.frames || protocol.MaterialID != final.MaterialID ||
		protocol.ExecutionID != final.ExecutionID || protocol.ConnectionID != "main" || !protocol.FullConsumption || !protocol.ClientEOF ||
		!protocol.CleanupComplete || protocol.NativeLaunched || protocol.ExitCode == nil || *protocol.ExitCode != 0 {
		return errors.New("testify: mock receipt did not establish strict Pi recording consumption and cleanup")
	}
	processData, err := os.ReadFile(filepath.Join(pkg, final.ProcessReceipt))
	if err != nil {
		return err
	}
	var process struct {
		SchemaVersion                                int
		Status, FixtureID, ExecutionID, ConnectionID string
		ExitCode                                     *int
	}
	if err := json.Unmarshal(processData, &process); err != nil {
		return err
	}
	if process.SchemaVersion != 1 || process.Status != "passed" || process.FixtureID != final.FixtureID ||
		process.ExecutionID != final.ExecutionID || process.ConnectionID != "main" || process.ExitCode == nil || *process.ExitCode != 0 {
		return errors.New("testify: mock process receipt did not establish a clean exit")
	}
	return errors.Join(
		os.WriteFile(filepath.Join(output, "protocol-receipt.json"), data, 0o600),
		os.WriteFile(filepath.Join(output, "process-receipt.json"), processData, 0o600),
	)
}
