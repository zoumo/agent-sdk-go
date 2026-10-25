// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestPolicyDirectoryPresenceFailsClosedWithoutReadingOrChangingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "managed")
	require.NoError(t, recorder.CheckPolicyDirectoryAbsent(path))
	require.NoError(t, os.Mkdir(path, 0700))
	policy := filepath.Join(path, "managed-settings.json")
	content := []byte(`{"hooks":{"SessionStart":[]}}`)
	require.NoError(t, os.WriteFile(policy, content, 0000))
	require.ErrorIs(t, recorder.CheckPolicyDirectoryAbsent(path), recorder.ErrPolicyIsolation)
	require.NoError(t, os.Chmod(policy, 0600))
	got, err := os.ReadFile(policy)
	require.NoError(t, err)
	require.Equal(t, content, got)
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(filepath.Join(root, "missing"), link))
	require.ErrorIs(t, recorder.CheckPolicyDirectoryAbsent(link), recorder.ErrPolicyIsolation)
}

func TestIsolationInspectionRejectsUnknownEndpointAndBinary(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	w, err := recorder.PrepareWorkspace(filepath.Join(base, "fresh"), "s1")
	require.NoError(t, err)
	defer w.Cleanup()
	for _, endpoint := range []string{"", "https://example.com", "http://localhost:3425", "http://127.0.0.1:3425/path", "http://user:pass@127.0.0.1:3425", "http://127.0.0.1:3425?x=1"} {
		t.Run(endpoint, func(t *testing.T) { require.ErrorContains(t, recorder.ValidateProxyEndpoint(endpoint), "loopback") })
	}
	require.NoError(t, recorder.ValidateProxyEndpoint("http://127.0.0.1:3425"))
	_, err = recorder.InspectIsolation("/missing", strings.Repeat("0", 64), "http://127.0.0.1:3425", w)
	require.ErrorIs(t, err, recorder.ErrPolicyIsolation)
	require.ErrorIs(t, recorder.PreflightWithIsolation(recorder.AcceptFinancialUncertainty, nil), recorder.ErrPolicyIsolation)
}
