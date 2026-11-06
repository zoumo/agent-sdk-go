package codex_test

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionFeatureAndToolPolicyStart(t *testing.T) {
	f := startFake(t, "normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"low"`),
		"future.option":          json.RawMessage(`{"opaque":9007199254740993}`),
	}
	features := map[codex.Feature]bool{
		codex.FeatureShellTool:          false,
		codex.FeatureViewImage:          true,
		codex.FeatureImageGeneration:    false,
		codex.Feature("future_feature"): true,
	}
	tools := map[codex.Tool]bool{
		codex.ToolUpdatePlan:              false,
		codex.ToolRequestUserInput:        true,
		codex.Tool("future_tool"):         false,
		codex.Tool("another_future_tool"): true,
	}
	wantConfig := maps.Clone(rawConfig)
	wantConfig["features.shell_tool"] = json.RawMessage(`false`)
	wantConfig["features.view_image"] = json.RawMessage(`true`)
	wantConfig["features.image_generation"] = json.RawMessage(`false`)
	wantConfig["features.future_feature"] = json.RawMessage(`true`)
	wantConfig["tools.update_plan.enabled"] = json.RawMessage(`false`)
	wantConfig["tools.experimental_request_user_input.enabled"] = json.RawMessage(`true`)
	wantConfig["tools.future_tool.enabled"] = json.RawMessage(`false`)
	wantConfig["tools.another_future_tool.enabled"] = json.RawMessage(`true`)
	wantRawConfig := maps.Clone(rawConfig)
	wantFeatures := maps.Clone(features)
	wantTools := maps.Clone(tools)

	s, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:   f.c,
		Thread:   types.ThreadStartParams{Config: types.Some(rawConfig)},
		Features: features,
		Tools:    tools,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	start := requireFrame(t, f, "thread/start")
	var params struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	require.NoError(t, json.Unmarshal(start.Params, &params))
	require.Equal(t, wantConfig, params.Config)
	require.Equal(t, wantRawConfig, rawConfig)
	require.Equal(t, wantFeatures, features)
	require.Equal(t, wantTools, tools)
}

func TestSessionFeatureAndToolPolicyResume(t *testing.T) {
	f := startFake(t, "resume-normal", codex.Limits{})
	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort": json.RawMessage(`"high"`),
	}
	resume := &types.ThreadResumeParams{
		ThreadID: resumedNativeID,
		Config:   types.Some(rawConfig),
	}
	features := map[codex.Feature]bool{codex.FeatureViewImage: true}
	tools := map[codex.Tool]bool{codex.ToolUpdatePlan: false}
	wantConfig := maps.Clone(rawConfig)
	wantConfig["features.view_image"] = json.RawMessage(`true`)
	wantConfig["tools.update_plan.enabled"] = json.RawMessage(`false`)

	s, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:   f.c,
		Resume:   resume,
		Features: features,
		Tools:    tools,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	frame := requireFrame(t, f, "thread/resume")
	var params struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	require.NoError(t, json.Unmarshal(frame.Params, &params))
	require.Equal(t, wantConfig, params.Config)
	require.Equal(t, map[string]json.RawMessage{"model_reasoning_effort": json.RawMessage(`"high"`)}, rawConfig)
	callerConfig, ok := resume.Config.Get()
	require.True(t, ok)
	require.Equal(t, rawConfig, callerConfig, "resume pointer config was mutated")
	require.Equal(t, map[codex.Feature]bool{codex.FeatureViewImage: true}, features)
	require.Equal(t, map[codex.Tool]bool{codex.ToolUpdatePlan: false}, tools)

	require.NoError(t, s.Close())
	second, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:   f.c,
		Resume:   &types.ThreadResumeParams{ThreadID: resumedNativeID},
		Features: map[codex.Feature]bool{codex.FeatureImageGeneration: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	require.Equal(t, map[string]json.RawMessage{
		"features.image_generation": json.RawMessage(`true`),
	}, configFromFrame(t, requireLastFrame(t, f, "thread/resume")))
}

func TestSessionFeatureAndToolPolicyRejectsRawConfigConflicts(t *testing.T) {
	const secret = "config-value-must-not-leak"
	f := startFake(t, "resume-normal", codex.Limits{})
	cases := []struct {
		name          string
		rawPath       string
		generatedPath string
		features      map[codex.Feature]bool
		tools         map[codex.Tool]bool
	}{
		{
			name:          "feature exact",
			rawPath:       "features.shell_tool",
			generatedPath: "features.shell_tool",
			features:      map[codex.Feature]bool{codex.FeatureShellTool: false},
		},
		{
			name:          "feature ancestor",
			rawPath:       "features",
			generatedPath: "features.view_image",
			features:      map[codex.Feature]bool{codex.FeatureViewImage: true},
		},
		{
			name:          "feature descendant",
			rawPath:       "features.image_generation.enabled",
			generatedPath: "features.image_generation",
			features:      map[codex.Feature]bool{codex.FeatureImageGeneration: false},
		},
		{
			name:          "tool exact",
			rawPath:       "tools.update_plan.enabled",
			generatedPath: "tools.update_plan.enabled",
			tools:         map[codex.Tool]bool{codex.ToolUpdatePlan: false},
		},
		{
			name:          "tool ancestor",
			rawPath:       "tools.update_plan",
			generatedPath: "tools.update_plan.enabled",
			tools:         map[codex.Tool]bool{codex.ToolUpdatePlan: true},
		},
		{
			name:          "tool root ancestor",
			rawPath:       "tools",
			generatedPath: "tools.experimental_request_user_input.enabled",
			tools:         map[codex.Tool]bool{codex.ToolRequestUserInput: false},
		},
		{
			name:          "tool descendant",
			rawPath:       "tools.update_plan.enabled.value",
			generatedPath: "tools.update_plan.enabled",
			tools:         map[codex.Tool]bool{codex.ToolUpdatePlan: true},
		},
	}
	for _, tc := range cases {
		for _, resume := range []bool{false, true} {
			name := "start/" + tc.name
			if resume {
				name = "resume/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				raw := map[string]json.RawMessage{
					tc.rawPath: json.RawMessage(`{"secret":"` + secret + `"}`),
				}
				options := codex.SessionOptions{Client: f.c, Features: tc.features, Tools: tc.tools}
				var callerResume *types.ThreadResumeParams
				if resume {
					callerResume = &types.ThreadResumeParams{ThreadID: resumedNativeID, Config: types.Some(raw)}
					options.Resume = callerResume
				} else {
					options.Thread.Config = types.Some(raw)
				}

				var first string
				for range 2 {
					s, err := codex.Open(context.Background(), options)
					require.Nil(t, s)
					require.ErrorIs(t, err, codex.ErrInvalidInput)
					require.Contains(t, err.Error(), tc.rawPath)
					require.Contains(t, err.Error(), tc.generatedPath)
					require.NotContains(t, err.Error(), secret)
					if first == "" {
						first = err.Error()
					} else {
						require.Equal(t, first, err.Error())
					}
				}
				require.Equal(t, map[string]json.RawMessage{
					tc.rawPath: json.RawMessage(`{"secret":"` + secret + `"}`),
				}, raw)
				if callerResume != nil {
					callerConfig, ok := callerResume.Config.Get()
					require.True(t, ok)
					require.Equal(t, raw, callerConfig)
				}
			})
		}
	}
	assertMethods(t, f, "initialize", "initialized")
}

func TestSessionFeatureAndToolPolicyDefaultsAndIsolation(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	features := map[codex.Feature]bool{codex.FeatureShellTool: false}
	first, err := codex.Open(context.Background(), codex.SessionOptions{
		Client:   f.c,
		Features: features,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	rawConfig := map[string]json.RawMessage{
		"model_reasoning_effort":    json.RawMessage(`"low"`),
		"features.shell_tool_extra": json.RawMessage(`true`),
	}
	tools := map[codex.Tool]bool{codex.ToolUpdatePlan: true}
	second, err := codex.Open(context.Background(), codex.SessionOptions{
		Client: f.c,
		Thread: types.ThreadStartParams{Config: types.Some(rawConfig)},
		Tools:  tools,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	require.NotEqual(t, first.ThreadID(), second.ThreadID())

	var configs []map[string]json.RawMessage
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/start" {
			configs = append(configs, configFromFrame(t, frame))
		}
	}
	require.Equal(t, []map[string]json.RawMessage{
		{"features.shell_tool": json.RawMessage(`false`)},
		{
			"model_reasoning_effort":    json.RawMessage(`"low"`),
			"features.shell_tool_extra": json.RawMessage(`true`),
			"tools.update_plan.enabled": json.RawMessage(`true`),
		},
	}, configs)
	require.Equal(t, map[codex.Feature]bool{codex.FeatureShellTool: false}, features)
	require.Equal(t, map[codex.Tool]bool{codex.ToolUpdatePlan: true}, tools)
}

func configFromFrame(t *testing.T, frame fakeEnvelope) map[string]json.RawMessage {
	t.Helper()
	var params struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	require.NoError(t, json.Unmarshal(frame.Params, &params))
	require.NotNil(t, params.Config)
	return params.Config
}

func requireFrame(t *testing.T, f fixture, method string) fakeEnvelope {
	t.Helper()
	for _, frame := range trace(t, f.trace) {
		if frame.Method == method {
			return frame
		}
	}
	require.FailNow(t, "missing frame", method)
	return fakeEnvelope{}
}

func requireLastFrame(t *testing.T, f fixture, method string) fakeEnvelope {
	t.Helper()
	var found fakeEnvelope
	for _, frame := range trace(t, f.trace) {
		if frame.Method == method {
			found = frame
		}
	}
	if found.Method != "" {
		return found
	}
	require.FailNow(t, "missing frame", method)
	return fakeEnvelope{}
}
