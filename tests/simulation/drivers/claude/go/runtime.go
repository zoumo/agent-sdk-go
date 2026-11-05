package claudesim

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Host is the prepared SDK host identity. Empty fields retain the legacy
// runtime.json/source-plan behavior used by not-yet-migrated simulator flows.
type Host struct {
	Workspace string
	Config    string
	Env       []string
}

// runtimePaths applies a prepared host configuration without mutating source plan.
// Absence is allowed for older SDK-owned unit fixtures only.
func runtimePaths(root, project, config string) (string, string, []string, error) {
	b, err := os.ReadFile(filepath.Join(root, "runtime.json"))
	if errors.Is(err, os.ErrNotExist) {
		return project, config, nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	var r struct {
		Workspace string
		Config    string
		Env       []string
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", "", nil, err
	}
	return r.Workspace, r.Config, r.Env, nil
}

func resolveHost(root, project, fixtureProject, config string, prepared Host) (Host, error) {
	workspace, runtimeConfig, runtimeEnv, err := runtimePaths(root, executionProject(project, fixtureProject), config)
	if err != nil {
		return Host{}, err
	}
	if prepared.Workspace != "" {
		workspace = prepared.Workspace
	}
	if prepared.Config != "" {
		runtimeConfig = prepared.Config
	}
	env := []string{"CLAUDE_CONFIG_DIR=" + runtimeConfig}
	if prepared.Env != nil {
		env = append([]string(nil), prepared.Env...)
	} else if len(runtimeEnv) > 0 {
		env = append([]string(nil), runtimeEnv...)
	}
	return Host{Workspace: workspace, Config: runtimeConfig, Env: env}, nil
}

type executionNotice struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Command         string `json:"command"`
	ConnectionID    string `json:"connectionId"`
	Execution       string `json:"execution"`
	ExecutionID     string `json:"executionId"`
	ExecutionSHA256 string `json:"executionSHA256"`
}

type executionDescriptor struct {
	SchemaVersion int               `json:"schemaVersion"`
	ExecutionID   string            `json:"executionId"`
	Command       string            `json:"command"`
	Workspace     string            `json:"workspace"`
	Config        string            `json:"config"`
	Env           map[string]string `json:"env"`
}

// executionHost adopts the launched main execution after Session.Open has
// created it. History is materialized into that execution, not the pre-launch
// mock route template.
func executionHost(root string) (Host, error) {
	logPath := filepath.Join(root, "result-main.jsonl")
	file, err := os.Open(logPath)
	if err != nil {
		return Host{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Host{}, err
		}
		return Host{}, errors.New("main execution notice is missing")
	}
	var notice executionNotice
	if err := json.Unmarshal(scanner.Bytes(), &notice); err != nil {
		return Host{}, fmt.Errorf("main execution notice: %w", err)
	}
	if notice.SchemaVersion != 1 || notice.Command != "mock" || notice.ConnectionID != "main" ||
		!filepath.IsAbs(notice.Execution) || notice.ExecutionID == "" || notice.ExecutionSHA256 == "" {
		return Host{}, errors.New("main execution notice identity differs")
	}
	data, err := os.ReadFile(notice.Execution)
	if err != nil {
		return Host{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != notice.ExecutionSHA256 {
		return Host{}, errors.New("main execution descriptor hash differs")
	}
	var descriptor executionDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return Host{}, fmt.Errorf("main execution descriptor: %w", err)
	}
	if descriptor.SchemaVersion != 3 || descriptor.ExecutionID != notice.ExecutionID ||
		descriptor.Command != "mock" || !filepath.IsAbs(descriptor.Workspace) ||
		!filepath.IsAbs(descriptor.Config) || len(descriptor.Env) == 0 {
		return Host{}, errors.New("main execution descriptor identity differs")
	}
	for _, path := range []string{descriptor.Workspace, descriptor.Config} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return Host{}, fmt.Errorf("main execution host path is unavailable: %s", path)
		}
	}
	if descriptor.Env["CLAUDE_CONFIG_DIR"] != descriptor.Config {
		return Host{}, errors.New("main execution config environment differs")
	}
	env := make([]string, 0, len(descriptor.Env))
	for key, value := range descriptor.Env {
		env = append(env, key+"="+value)
	}
	return Host{Workspace: descriptor.Workspace, Config: descriptor.Config, Env: env}, nil
}
