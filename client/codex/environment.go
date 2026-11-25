package codex

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// validateEnvironment checks keys and values in a stable order. ASCII case
// folding keeps declarations unambiguous on Windows as well.
func validateEnvironment(env map[string]string) error {
	seen := make(map[string]string, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if key == "" || !utf8.ValidString(key) || strings.ContainsAny(key, "\x00=") {
			return fmt.Errorf("invalid environment key %q", key)
		}
		folded := foldEnvironmentKey(key)
		if previous, ok := seen[folded]; ok {
			return fmt.Errorf("duplicate environment keys %q and %q", previous, key)
		}
		seen[folded] = key
		if value := env[key]; !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid environment value for key %q", key)
		}
	}
	return nil
}

// foldEnvironmentKey normalizes only ASCII letters, matching environment-name comparisons.
func foldEnvironmentKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, key)
}
