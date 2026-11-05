package claudesim

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FilterMCPConfig removes one SDK-generated synthetic MCP declaration before
// the native mock process sees its startup arguments.
func FilterMCPConfig(args []string) ([]string, error) {
	filtered := make([]string, 0, len(args))
	seen := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--mcp-config":
			if i+1 == len(args) {
				return nil, errors.New("missing MCP config value")
			}
			i++
			if err := validateSyntheticMCPConfig(args[i]); err != nil {
				return nil, err
			}
			seen++
		case strings.HasPrefix(arg, "--mcp-config="):
			if err := validateSyntheticMCPConfig(strings.TrimPrefix(arg, "--mcp-config=")); err != nil {
				return nil, err
			}
			seen++
		default:
			filtered = append(filtered, arg)
		}
	}
	if seen != 1 {
		return nil, errors.New("synthetic MCP config argument count is not one")
	}
	return filtered, nil
}

func validateSyntheticMCPConfig(raw string) error {
	var config struct {
		Servers map[string]struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"mcpServers"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || len(config.Servers) == 0 {
		return errors.New("invalid synthetic MCP config")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid synthetic MCP config")
	}
	for name, server := range config.Servers {
		if name == "" || server.Type != "sdk" || server.Name != name {
			return errors.New("MCP config is not the SDK synthetic declaration")
		}
	}
	return nil
}

func mcpFilterLauncher(root, launcher string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "launcher-mcp")
	script := "#!/bin/sh\nexec " + quoteLauncher(executable) +
		" --filter-mcp " + quoteLauncher(launcher) + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func quoteLauncher(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
