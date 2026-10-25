// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
	"strings"
	"testing"
)

func TestVersionAndFlagEvidenceIsExact(t *testing.T) {
	help := "--print --restricted --safe-mode --no-session-persistence --disable-slash-commands --strict-mcp-config --mcp-config --no-chrome --setting-sources --settings --tools --max-budget-usd --system-prompt"
	require.NoError(t, recorder.ValidateVersion([]byte("2.1.258 (Claude Code)\n"), []byte(help)))
	require.Error(t, recorder.ValidateVersion([]byte("2.1.310 (Claude Code)\n"), []byte(help)))
	require.Error(t, recorder.ValidateVersion([]byte("2.1.258 (Claude Code)\n"), []byte(strings.Replace(help, "--safe-mode", "--safe-mode-other", 1))))
}
