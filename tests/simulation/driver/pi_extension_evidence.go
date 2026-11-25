package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"github.com/zoumo/agent-sdk-go/replay"
)

var piExtensionAliasPattern = regexp.MustCompile(`^/tmp/agent-sdk-pi-replay-[0-9a-f]{32}/session\.ts$`)

func validPiExtensionAlias(path string) bool { return piExtensionAliasPattern.MatchString(path) }

type piReplayMaterial struct {
	alias, rawSHA256 string
	frames           int
}

// Only the native recording carries the handshake alias. The importer and
// execution report supply the material identity that binds this byte stream.
func readPiReplayMaterial(root string) (piReplayMaterial, error) {
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
			Ordinal   int
			Direction string
			Wire      struct {
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
		if frame.Direction == "s2c" && frame.Wire.Type == "response" && frame.Wire.Command == "get_commands" && frame.Wire.Success {
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

type piMockStart struct {
	ExecutionID     string
	ExecutionSHA256 string
}

func writePiMockStart(runtimeDir string, start replay.ExecutionStart) error {
	return writePiReplayJSON(filepath.Join(runtimeDir, "mock-start.json"), piMockStart{
		ExecutionID: start.ExecutionID, ExecutionSHA256: start.ExecutionSHA256,
	})
}

type piReplayExecution struct {
	Case              string `json:"case"`
	FixtureID         string `json:"fixtureId"`
	ExecutionID       string `json:"executionId"`
	ConnectionID      string `json:"connectionId"`
	ExecutionStatus   string `json:"executionStatus"`
	ExecutionComplete bool   `json:"executionComplete"`
	Status            string `json:"status"`
	Transport         string `json:"transport"`
	MaterialID        string `json:"materialId"`
	Provenance        string `json:"provenance"`
	RecordingSHA256   string `json:"recordingSHA256"`
	Frames            int    `json:"frames"`
	ExecutionSHA256   string `json:"executionSHA256"`
	FullConsumption   bool   `json:"fullConsumption"`
	ClientEOF         bool   `json:"clientEOF"`
	CleanupComplete   bool   `json:"cleanupComplete"`
	NativeLaunched    bool   `json:"nativeLaunched"`
	TestifyVersion    string `json:"testifyVersion"`
	ModuleRevision    string `json:"moduleRevision,omitempty"`
	TestifyModuleSum  string `json:"testifyModuleSum"`
	Failure           string `json:"failure,omitempty"`
	Error             string `json:"error,omitempty"`
}

func samePiRecording(candidate replay.Material, material piReplayMaterial) bool {
	return candidate.ID != "" && candidate.Provenance == "consumer" && candidate.RecordingSHA256 == material.rawSHA256
}

func verifyPiReplayResult(fixture *replay.Fixture, runtimeDir, output, code string, material piReplayMaterial) error {
	var start piMockStart
	if err := readPiExtensionJSON(filepath.Join(runtimeDir, "mock-start.json"), &start); err != nil {
		return fmt.Errorf("read Replay Pi mock start identity: %w", err)
	}
	report, err := fixture.ExecutionReport(start.ExecutionID)
	if err != nil {
		return fmt.Errorf("read Replay Pi execution report: %w", err)
	}
	if report == nil || report.FixtureID != fixture.FixtureID || report.ExecutionID != start.ExecutionID ||
		start.ExecutionSHA256 == "" || report.ExecutionSHA256 != start.ExecutionSHA256 ||
		!samePiRecording(report.Material, material) || !reflect.DeepEqual(report.Material, *fixture.Current) {
		return errors.New("replay: Pi execution identity differs from the prepared fixture and mock start")
	}
	if len(report.Connections) != 1 || report.Connections[0].ID != piMockConnection {
		return errors.New("replay: Pi execution did not report exactly the main connection")
	}
	connection := report.Connections[0]
	summary := piReplayExecution{
		Case: code, FixtureID: report.FixtureID, ExecutionID: report.ExecutionID, ConnectionID: connection.ID,
		ExecutionStatus: string(report.Status), ExecutionComplete: report.Complete, Status: string(connection.Status),
		Transport: string(connection.Transport), MaterialID: report.Material.ID, Provenance: report.Material.Provenance,
		RecordingSHA256: report.Material.RecordingSHA256, Frames: material.frames, ExecutionSHA256: report.ExecutionSHA256,
		FullConsumption: connection.FullConsumption, ClientEOF: connection.ClientEOF, CleanupComplete: connection.CleanupComplete,
		NativeLaunched: connection.NativeLaunched, TestifyVersion: fixture.Tool.Version, TestifyModuleSum: fixture.Tool.ModuleSum, ModuleRevision: fixture.Tool.ModuleRevision,
		Failure: string(connection.Failure), Error: connection.Error,
	}
	return errors.Join(writePiReplayJSON(filepath.Join(output, "execution.json"), summary), validatePiReplayCompletion(report, connection))
}

func validatePiReplayCompletion(report *replay.ExecutionReport, connection replay.ConnectionReport) error {
	if report.Status != replay.ExecutionStatusPassed || !report.Complete || connection.Status != replay.ConnectionStatusPassed {
		return fmt.Errorf("strict Replay mock execution %s (complete=%t), connection %s (%s): %s",
			report.Status, report.Complete, connection.Status, connection.Failure, connection.Error)
	}
	if connection.Transport != replay.MockTransportInProcess || !connection.FullConsumption || !connection.ClientEOF ||
		!connection.CleanupComplete || connection.NativeLaunched || connection.ExitCode != nil || connection.Failure != "" || connection.Error != "" {
		return errors.New("replay: Pi execution did not establish full consumption, client EOF, and cleanup")
	}
	return nil
}

func writePiReplayJSON(path string, value any) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return json.NewEncoder(file).Encode(value)
}
