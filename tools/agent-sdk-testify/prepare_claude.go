package main

import (
	"encoding/json"
	"os"
)

func claudeNativeArgs(profile string, params object) []string {
	var args []string
	args = []string{"--print", "--system-prompt", "Complete the supplied synthetic task in the working directory. Be concise.", "--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	if profile != "history" && profile != "existing-children" {
		args = append(args, "--no-session-persistence")
	}
	if profile == "children" || profile == "background-children" || params["topology"] != nil {
		args = append(args, "--forward-subagent-text")
	}
	var ask []string
	switch profile {
	case "fixture-tools":
		ask = []string{"Read", "Write"}
	case "deferred-approval":
		ask = []string{"Read"}
	case "children", "background-children":
		ask = []string{"Agent", "Read"}
	}
	if params["approvalKind"] == "command" {
		ask = []string{"Bash"}
	}
	if ask != nil {
		b, _ := json.Marshal(object{"permissions": object{"ask": ask}})
		args = append(args, "--settings", string(b))
	}
	if params["applicationTool"] == true {
		args = append(args, "--mcp-config", `{"mcpServers":{"fixture":{"type":"sdk","name":"fixture"}}}`)
	}
	if params["configurationTiming"] == "creation" && params["setting"] == "thinking" {
		args = append(args, "--max-thinking-tokens", "1024")
	}
	if params["setting"] == "effort" {
		args = append(args, "--effort", "low")
	}
	return args
}

func prepareClaudeArgs(plan object, args []string, profile string) []string {
	if str(plan["model"]) != "" {
		args = append(args, "--model", str(plan["model"]))
	}
	if profile == "existing-children" {
		args = append(args, "--resume", str(obj(plan["historySeed"])["sessionId"]), "--forward-subagent-text")
	}
	return args
}

func claudeAuthEnvironment(path string, env map[string]string) {
	var settings object
	if e := readJSON(path, &settings); e == nil {
		selected := obj(settings["env"])
		// A selected endpoint must keep its own credentials; inherited
		// auth from a different endpoint is not a substitute.
		if str(selected["ANTHROPIC_BASE_URL"]) != "" {
			for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
				delete(env, k)
			}
		}
		for k, v := range selected {
			if s, ok := v.(string); ok {
				env[k] = s
			}
		}
	}
}

func filterClaudeConfig(path string) error {
	var source object
	if err := readJSON(path, &source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	selected := object{}
	if model, ok := source["model"]; ok {
		selected["model"] = model
	}
	return writeJSON(path, selected)
}
