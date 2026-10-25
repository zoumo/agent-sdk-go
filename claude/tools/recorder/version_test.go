// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionAndFlagEvidenceIsExact(t *testing.T) {
	help := "--model --print --restricted --safe-mode --no-session-persistence --disable-slash-commands --strict-mcp-config --mcp-config --no-chrome --setting-sources --settings --tools --max-budget-usd --system-prompt"
	require.NoError(t, recorder.ValidateVersion([]byte("2.1.258 (Claude Code)\n"), []byte(help)))
	require.Error(t, recorder.ValidateVersion([]byte("2.1.310 (Claude Code)\n"), []byte(help)))
	require.Error(t, recorder.ValidateVersion([]byte("2.1.258 (Claude Code)\n"), []byte(strings.Replace(help, "--safe-mode", "--safe-mode-other", 1))))
}

// Linux's reviewed native distribution exceeds the historical 128 MiB ceiling.
// Validation must remain bounded and must not execute the supplied file.
func TestNativeExecutableValidationIsBoundedAndNeverExecutes(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	path := filepath.Join(base, "native-cli")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(129<<20))
	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	digest := hex.EncodeToString(h.Sum(nil))
	require.NoError(t, recorder.ValidateCLIBinary(path, digest))
	require.Error(t, recorder.ValidateCLIBinary(path, strings.Repeat("0", 64)))
	require.NoError(t, os.Truncate(path, (256<<20)+1))
	require.Error(t, recorder.ValidateCLIBinary(path, digest))
}
