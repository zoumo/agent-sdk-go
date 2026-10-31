package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

func codexNativeArgs(profile string, params object) []string {
	var args []string
	args = []string{"app-server", "--stdio", "-c", "features.default_mode_request_user_input=true"}
	if profile == "children" || profile == "background-children" || params["historyTarget"] == "child-messages" || params["topology"] != nil {
		args = append(args, "-c", "agents.enabled=true", "-c", "features.multi_agent=true")
	}
	if params["topology"] != nil {
		args = append(args, "-c", "agents.max_depth=2")
	}
	if params["approvalKind"] == "command" {
		args = append(args, "-c", `sandbox_mode="read-only"`)
	}
	return args
}

func prepareCodexChildFixture(root string) error {
	prompt := "Use fixture_read exactly once to read only child-fixture.txt in the inherited working directory. Return exactly SYNTHETIC_CHILD_TOKEN. Do not create agents or descendants. Do not use shell, network, search, writes, or any other file or tool."
	b, _ := json.Marshal(prompt)
	role := "name=\"fixture_reader\"\ndescription=\"Read the single synthetic child fixture with fixture_read.\"\ndeveloper_instructions=" + string(b) + "\n"
	if err := writeFiles(filepath.Join(root, "config"), object{"agents/fixture_reader.toml": role}); err != nil {
		return err
	}
	return nil
}

// Select only variables referenced by the active native provider. A real TOML
// parser handles table and inline-table forms without a new config language.
func codexAuthEnvironment(path string, env map[string]string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var config struct {
		Provider  string `toml:"model_provider"`
		Providers map[string]struct {
			EnvKey  string            `toml:"env_key"`
			Headers map[string]string `toml:"env_http_headers"`
		} `toml:"model_providers"`
	}
	if err = toml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("selected Codex config: %w", err)
	}
	selected := config.Providers[config.Provider]
	names := []string{selected.EnvKey}
	for _, name := range selected.Headers {
		names = append(names, name)
	}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); name != "" && ok {
			env[name] = value
		}
	}
	return nil
}

// Only connection selection crosses into the isolated configuration. User
// projects, hooks, tools and instructions are not case materials.
func filterCodexConfig(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var source object
	if err = toml.Unmarshal(data, &source); err != nil {
		return err
	}
	selected := object{}
	for _, key := range []string{"model", "model_provider", "model_reasoning_effort"} {
		if value, ok := source[key]; ok {
			selected[key] = value
		}
	}
	provider := str(source["model_provider"])
	if provider != "" {
		fields := obj(obj(source["model_providers"])[provider])
		connection := object{}
		for _, key := range []string{"name", "base_url", "env_key", "wire_api", "requires_openai_auth", "http_headers", "env_http_headers", "supports_websockets"} {
			if value, ok := fields[key]; ok {
				connection[key] = value
			}
		}
		selected["model_providers"] = object{provider: connection}
	}
	filtered, err := toml.Marshal(selected)
	if err != nil {
		return err
	}
	return os.WriteFile(path, filtered, 0600)
}
