package mocktest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testifyModulePath = "github.com/zoumo/agent-sdk-testify"

func TestTestifyIdentityMatchesModulePin(t *testing.T) {
	root := moduleRoot(t)
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	require.NoError(t, err)
	require.Contains(
		t,
		string(goMod),
		testifyModulePath+" "+testifyModuleVersion,
		"embedded identity version must match go.mod",
	)

	goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	require.NoError(t, err)
	require.Contains(
		t,
		string(goSum),
		testifyModulePath+" "+testifyModuleVersion+" "+testifyModuleSum,
		"embedded identity sum must match go.sum",
	)
	if strings.HasPrefix(testifyModuleVersion, "v0.0.0-") {
		require.True(
			t,
			strings.HasPrefix(testifyModuleRevision, pseudoRevision(testifyModuleVersion)),
			"embedded revision must match the pseudo-version suffix",
		)
	} else {
		require.Regexp(
			t,
			`^[0-9a-f]{40}$`,
			testifyModuleRevision,
			"embedded revision must be a full commit ID",
		)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "repository go.mod not found")
		dir = parent
	}
}

func pseudoRevision(version string) string {
	return version[strings.LastIndexByte(version, '-')+1:]
}
