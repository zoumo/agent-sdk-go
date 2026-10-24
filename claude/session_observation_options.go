// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"fmt"
	"time"
)

type SubagentOptions struct {
	MaxNodes int
	MaxDepth int
	// MaxUnresolvedEvents/Bytes bound native frames awaiting source attribution.
	MaxUnresolvedEvents int
	MaxUnresolvedBytes  int
	// MaxUnresolvedWait bounds association waiting, never provider work.
	MaxUnresolvedWait time.Duration
	// History contains caller-verified local-agent associations, not replay frames.
	History []SubagentHistory
}

func isTreeTaskEvent(e Event) bool {
	if e.System == nil {
		return false
	}
	switch e.Subtype {
	case "task_started", "task_updated", "task_progress", "task_notification":
		return true
	}
	return false
}

func isTreeRootEvent(e Event) bool {
	switch e.Type {
	case "assistant", "user", "result", "stream_event":
		return true
	case "system":
		// Subtypes reviewed in official TS 0.3.302. Unknown future subtypes stay
		// unscoped unless their own agent/task/tool evidence proves attribution.
		switch e.Subtype {
		case "init", "status", "compact_boundary", "api_retry",
			"background_tasks_changed", "session_state_changed", "commands_changed",
			"control_request_progress", "elicitation_complete", "files_persisted",
			"hook_started", "hook_progress", "hook_response", "informational",
			"local_command_output", "memory_recall", "mirror_error",
			"model_refusal_fallback", "model_refusal_no_fallback", "notification",
			"permission_denied", "plugin_install", "thinking_tokens",
			"worker_shutting_down", "task_started", "task_progress", "task_updated",
			"task_notification":
			return true
		}
	}
	return false
}

func normalizedSubagentOptions(options Options) (SubagentOptions, error) {
	bounds := options.Subagents
	for _, bound := range []struct {
		name              string
		value             *int
		fallback, ceiling int
	}{
		{"MaxNodes", &bounds.MaxNodes, 4096, 65536},
		{"MaxDepth", &bounds.MaxDepth, 32, 256},
		{"MaxUnresolvedEvents", &bounds.MaxUnresolvedEvents, 128, 65536},
		{"MaxUnresolvedBytes", &bounds.MaxUnresolvedBytes, 4 << 20, 64 << 20},
	} {
		if *bound.value < 0 || *bound.value > bound.ceiling {
			return SubagentOptions{}, fmt.Errorf("claude: %s must be between 0 and %d", bound.name, bound.ceiling)
		}
		if *bound.value == 0 {
			*bound.value = bound.fallback
		}
	}
	if bounds.MaxUnresolvedWait < 0 || bounds.MaxUnresolvedWait > time.Minute {
		return SubagentOptions{}, fmt.Errorf("claude: MaxUnresolvedWait must be between 0 and 1 minute")
	}
	if bounds.MaxUnresolvedWait == 0 {
		bounds.MaxUnresolvedWait = 5 * time.Second
	}
	if len(bounds.History) > bounds.MaxNodes {
		return SubagentOptions{}, fmt.Errorf("claude: history exceeds MaxNodes")
	}
	for _, seed := range bounds.History {
		if seed.SessionID == "" || seed.TaskID == "" {
			return SubagentOptions{}, fmt.Errorf("claude: history requires session and task IDs")
		}
	}
	return bounds, nil
}
