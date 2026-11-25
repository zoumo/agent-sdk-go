package claudetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func AssignedSessionLiveEnvironment(t *testing.T) (string, string, []string) {
	t.Helper()
	if os.Getenv("SDK_REAL_AGENT_E2E") != "1" {
		t.Skip("SDK_REAL_AGENT_E2E=1 is required for real model calls")
	}
	require.Equal(t, "1", os.Getenv("CLAUDE_E2E"))
	require.Empty(t, os.Getenv("CI"), "real Claude model calls are forbidden in CI")
	cliPath, model := os.Getenv("CLAUDE_E2E_CLI"), os.Getenv("CLAUDE_E2E_MODEL")
	require.True(t, filepath.IsAbs(cliPath))
	require.NotEmpty(t, model)
	binary, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	digest := sha256.Sum256(binary)
	versionCtx, cancelVersion := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelVersion()
	version, err := exec.CommandContext(versionCtx, cliPath, "--version").Output()
	require.NoError(t, err)
	t.Logf("CLI version=%s sha256=%s model=%s", strings.TrimSpace(string(version)), hex.EncodeToString(digest[:]), model)
	var settings struct {
		Env map[string]string `json:"env"`
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	if data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
		require.NoError(t, json.Unmarshal(data, &settings))
	}
	isolatedHome := t.TempDir()
	env := []string{
		"HOME=" + isolatedHome, "PATH=" + os.Getenv("PATH"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(isolatedHome, ".claude"),
		"CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
	}
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		value := os.Getenv(key)
		if value == "" {
			value = settings.Env[key]
		}
		if value != "" {
			env = append(env, key+"="+value)
		}
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		env = append(env, key+"="+model)
	}
	return cliPath, model, env
}
