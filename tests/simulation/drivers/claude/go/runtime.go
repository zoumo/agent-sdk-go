package claudesim

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// runtimePaths applies a prepared host configuration without mutating source plan.
// Absence is allowed for older SDK-owned unit fixtures only.
func runtimePaths(root, project, config string) (string, string, error) {
	b, err := os.ReadFile(filepath.Join(root, "runtime.json"))
	if errors.Is(err, os.ErrNotExist) {
		return project, config, nil
	}
	if err != nil {
		return "", "", err
	}
	var r struct{ Workspace, Config string }
	if err := json.Unmarshal(b, &r); err != nil {
		return "", "", err
	}
	return r.Workspace, r.Config, nil
}
