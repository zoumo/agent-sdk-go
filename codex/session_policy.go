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
	if len(options.Features) == 0 && len(options.Tools) == 0 {
		return nil
	}
	if options.Resume != nil {
		config, err := mergePolicyConfig(options.Resume.Config, options.Features, options.Tools)
		if err != nil {
			return err
		}
		resume := *options.Resume
		resume.Config = config
		options.Resume = &resume
		return nil
	}
	config, err := mergePolicyConfig(options.Thread.Config, options.Features, options.Tools)
	if err != nil {
		return err
	}
	options.Thread.Config = config
	return nil
}

func mergePolicyConfig(config types.Optional[map[string]json.RawMessage], features map[Feature]bool, tools map[Tool]bool) (types.Optional[map[string]json.RawMessage], error) {
	policy := make(map[string]json.RawMessage, len(features)+len(tools))
	for feature, enabled := range features {
		policy["features."+string(feature)] = json.RawMessage(strconv.FormatBool(enabled))
	}
	for tool, enabled := range tools {
		policy["tools."+string(tool)+".enabled"] = json.RawMessage(strconv.FormatBool(enabled))
	}

	raw, ok := config.Get()
	if ok {
		raw = maps.Clone(raw)
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage, len(policy))
	}
	policyPaths := slices.Sorted(maps.Keys(policy))
	for _, rawPath := range slices.Sorted(maps.Keys(raw)) {
		for _, policyPath := range policyPaths {
			if dottedPathsConflict(rawPath, policyPath) {
				return config, fmt.Errorf("%w: raw config path %q conflicts with generated path %q", ErrInvalidInput, rawPath, policyPath)
			}
		}
	}
	maps.Copy(raw, policy)
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
