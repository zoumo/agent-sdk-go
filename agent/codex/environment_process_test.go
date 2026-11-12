//go:build !windows

package agentcodex_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestEnvironmentProviderProcessReplacementSemantics(t *testing.T) {
	t.Setenv("AGENT_ENV_PARENT_ONLY", "ambient")
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"nil inherits", nil, "ambient"},
		{"empty replaces", []string{}, "missing"},
		{"nonempty replaces", []string{"AGENT_ENV_PARENT_ONLY=replacement"}, "replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			output := filepath.Join(t.TempDir(), "environment")
			cliPath := filepath.Join(t.TempDir(), "synthetic-cli")
			script := "#!/bin/sh\nprintf '%s' \"${AGENT_ENV_PARENT_ONLY-missing}\" > " + strconv.Quote(output) + "\nexit 42\n"
			require.NoError(t, os.WriteFile(cliPath, []byte(script), 0o700))
			before := slices.Clone(tc.env)
			provider := agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{Executable: cliPath, Env: tc.env}})
			_, err := provider.Start(ctx)
			require.Error(t, err)
			require.NoError(t, provider.Stop(context.Background()))
			data, err := os.ReadFile(output)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(data))
			assert.Equal(t, before, tc.env)
		})
	}
}
