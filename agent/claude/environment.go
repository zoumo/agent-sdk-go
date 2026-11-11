package agentclaude

import (
	"maps"
	"os"
	"slices"
)

func effectiveProcessEnvironment(base []string, overlay map[string]string) []string {
	env := slices.Clone(base)
	if base == nil {
		env = os.Environ()
	}
	for _, key := range slices.Sorted(maps.Keys(overlay)) {
		env = append(env, key+"="+overlay[key])
	}
	return env
}
