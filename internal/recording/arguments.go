package recording

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// StoryArguments is the single fixed launch policy for both record entry points.
// It never incorporates user/project integrations or command-line overrides.
func StoryArguments(provider, story string) ([]string, error) {
	if !storyID.MatchString(story) {
		return nil, errors.New("unknown user story")
	}
	id := strings.TrimPrefix(story, "SDK-US-")
	if provider == "codex" {
		args := []string{"app-server", "--stdio"}
		settings := []string{
			"features.plugins=false",
			"features.apps=false",
			"features.enable_mcp_apps=false",
			"features.skip_host_skill_discovery=true",
			"skills.include_instructions=false",
			"skills.bundled.enabled=false",
			"features.memories=false",
			"notify=[]",
			"features.shell_snapshot=false",
			"project_doc_max_bytes=0",
			"features.shell_tool=false",
			"features.stable_environment_tools=false",
			"features.multi_agent_v2=false",
			"agents.enabled=false",
			"features.code_mode=false",
			"features.js_repl=false",
			"web_search=\"disabled\"",
			"approval_policy=\"on-request\"",
			"analytics.enabled=false",
			"otel.exporter=\"none\"",
			"otel.trace_exporter=\"none\"",
			"otel.metrics_exporter=\"none\"",
			"features.default_mode_request_user_input=true",
			"tools.update_plan.enabled=false",
			"shell_environment_policy.inherit=\"none\"",
		}
		for _, setting := range settings {
			args = append(args, "-c", setting)
		}
		return args, nil
	}
	if provider != "claude" {
		return nil, errors.New("unknown provider")
	}
	tools := map[string]string{"004": "Read,Write", "009": "Write", "010": "Read", "011": "AskUserQuestion"}[id]
	ask := []string{}
	if tools != "" {
		ask = strings.Split(tools, ",")
	}
	settings, _ := json.Marshal(map[string]any{"disableAllHooks": true, "permissions": map[string]any{"ask": ask}})
	args := []string{"--print", "--safe-mode", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-chrome", "--setting-sources", "", "--settings", string(settings), "--tools", tools, "--max-budget-usd", "1", "--system-prompt", "Perform only the supplied synthetic task. Use no external context or additional tools. Be concise."}
	if id != "019" {
		args = append(args, "--no-session-persistence")
	}
	return args, nil
}
func validateStoryArguments(p StoryPlan) error {
	expected, e := StoryArguments(p.Provider, p.Story)
	if e != nil {
		return e
	}
	if len(expected) != len(p.Args) {
		return errors.New("unreviewed native launch arguments")
	}
	for i, want := range expected {
		if i > 0 && (expected[i-1] == "--settings" || expected[i-1] == "--mcp-config") {
			var a, b any
			if decodeReplayJSON([]byte(want), &a) != nil || decodeReplayJSON([]byte(p.Args[i]), &b) != nil || !reflect.DeepEqual(a, b) {
				return errors.New("unreviewed native settings")
			}
		} else if p.Args[i] != want {
			return errors.New("unreviewed native launch arguments")
		}
	}
	return nil
}

func ReviewedStoryVersions(provider string) []string {
	if provider == "codex" {
		return []string{"0.161.0", "0.162.1"}
	}
	if provider == "claude" {
		return []string{"2.1.199", "2.1.258"}
	}
	return nil
}
