package agentclaude_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if scenario := os.Getenv(claudeResumePeerEnv); scenario != "" {
		os.Exit(runClaudeResumePeer(scenario, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func readClaudeArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var argv []string
	require.NoError(t, json.Unmarshal(data, &argv))
	return argv
}
