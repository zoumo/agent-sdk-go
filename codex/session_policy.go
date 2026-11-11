package codex

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Feature selects a native thread feature under features.<key>.
type Feature string

const (
	FeatureShellTool       Feature = "shell_tool"
	FeatureViewImage       Feature = "view_image"
	FeatureImageGeneration Feature = "image_generation"
)

// Tool selects a native tool switch under tools.<key>.enabled.
type Tool string

const (
	ToolUpdatePlan       Tool = "update_plan"
	ToolRequestUserInput Tool = "experimental_request_user_input"
)

func applySessionPolicyConfig(options *SessionOptions) error {
	if len(options.Features) == 0 && len(options.Tools) == 0 && len(options.MCPServers) == 0 && len(options.ShellEnv) == 0 {
		return nil
	}
	generated, err := generatedSessionConfig(options)
	if err != nil {
		return err
	}
	if options.Resume != nil {
		config, err := mergeGeneratedConfig(options.Resume.Config, generated)
		if err != nil {
			return err
		}
		resume := *options.Resume
		resume.Config = config
		options.Resume = &resume
		return nil
	}
	config, err := mergeGeneratedConfig(options.Thread.Config, generated)
	if err != nil {
		return err
	}
	options.Thread.Config = config
	return nil
}

func generatedSessionConfig(options *SessionOptions) (map[string]json.RawMessage, error) {
	generated := make(map[string]json.RawMessage, len(options.Features)+len(options.Tools)+len(options.MCPServers))
	for feature, enabled := range options.Features {
		generated["features."+string(feature)] = json.RawMessage(strconv.FormatBool(enabled))
	}
	for tool, enabled := range options.Tools {
		generated["tools."+string(tool)+".enabled"] = json.RawMessage(strconv.FormatBool(enabled))
	}
	if err := addMCPServerConfig(generated, options.MCPServers); err != nil {
		return nil, err
	}
	if len(options.ShellEnv) != 0 {
		if err := validateEnvironment(options.ShellEnv); err != nil {
			return nil, fmt.Errorf("%w: ShellEnv: %s", ErrInvalidInput, err)
		}
		for _, key := range slices.Sorted(maps.Keys(options.ShellEnv)) {
			if reservedShellEnvKey(key) {
				return nil, fmt.Errorf("%w: ShellEnv key %q is owned by Codex", ErrInvalidInput, key)
			}
		}
		encoded, err := json.Marshal(options.ShellEnv)
		if err != nil {
			return nil, fmt.Errorf("%w: encode ShellEnv", ErrInvalidInput)
		}
		generated["shell_environment_policy.set"] = encoded
	}
	return generated, nil
}

func mergeGeneratedConfig(config types.Optional[map[string]json.RawMessage], generated map[string]json.RawMessage) (types.Optional[map[string]json.RawMessage], error) {
	raw, ok := config.Get()
	if ok {
		raw = maps.Clone(raw)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage, len(generated))
	}
	generatedPaths := slices.Sorted(maps.Keys(generated))
	for _, rawPath := range slices.Sorted(maps.Keys(raw)) {
		for _, generatedPath := range generatedPaths {
			if dottedPathsConflict(rawPath, generatedPath) {
				return config, fmt.Errorf("%w: raw config path %q conflicts with generated path %q", ErrInvalidInput, rawPath, generatedPath)
			}
		}
	}
	maps.Copy(raw, generated)
	return types.Some(raw), nil
}

func dottedPathsConflict(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func reservedShellEnvKey(key string) bool {
	switch foldEnvironmentKey(key) {
	case "codex_session_id", "codex_thread_id", "codex_tool_call_id",
		"codex_exec_server_noise_auth_token", "node_repl_auth_token",
		"codex_guardian_decisions_api_key", "openai_federation_rule_id",
		"openai_identity_token_file", "openai_workload_identity_context":
		return true
	}
	return false
}
