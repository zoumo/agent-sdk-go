package claudesim

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
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

// executionHostFromRun adopts the immutable execution snapshot returned by the
// public library before protocol I/O. It never reads result files.
func executionHostFromRun(run *mocktest.Run) (Host, error) {
	if run == nil || run.Start.ExecutionID == "" || run.Start.ConnectionID == "" {
		return Host{}, errors.New("main execution start is missing")
	}
	start := run.Start
	if !filepath.IsAbs(start.Workspace) || !filepath.IsAbs(start.Config) || len(start.Env) == 0 {
		return Host{}, errors.New("main execution start identity differs")
	}
	for _, path := range []string{start.Workspace, start.Config} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return Host{}, fmt.Errorf("main execution host path is unavailable: %s", path)
		}
	}
	if start.Env["CLAUDE_CONFIG_DIR"] != start.Config {
		return Host{}, errors.New("main execution config environment differs")
	}
	return Host{
		Workspace: start.Workspace,
		Config:    start.Config,
		Env:       environment(start.Env),
	}, nil
}
