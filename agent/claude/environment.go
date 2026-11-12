package agentclaude

import (
	"maps"
	"os"
	"slices"
	"strings"
)

const claudeSessionStateEnv = "CLAUDE_CODE_SDK_READS_SESSION_STATE"

func effectiveProcessEnvironment(base []string, overlay map[string]string) []string {
	env := slices.Clone(base)
	if base == nil {
		env = os.Environ()
	}
	for _, key := range slices.Sorted(maps.Keys(overlay)) {
		env = append(env, key+"="+overlay[key])
	}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, claudeSessionStateEnv) {
			return env
		}
	}
	return append(env, claudeSessionStateEnv+"=1")
}
