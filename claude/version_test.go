package claude

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

func TestParseCLIVersionRequiresMinimumStableRelease(t *testing.T) {
	for _, test := range []struct {
		output  string
		version string
		valid   bool
	}{
		{"2.1.293 (Claude Code)\n", "2.1.293", true},
		{"v2.1.294 (Claude Code)\n", "2.1.294", true},
		{"2.2.0 (Claude Code)\n", "2.2.0", true},
		{"3.0.0 (Claude Code)\n", "3.0.0", true},
		{"2.1.293+build.7 (Claude Code)\n", "2.1.293+build.7", true},
		{"2.1.292 (Claude Code)\n", "2.1.292", false},
		{"2.1.0 (Claude Code)\n", "2.1.0", false},
		{"2.1.293-beta (Claude Code)\n", "2.1.293-beta", false},
		{"2.2.0-beta (Claude Code)\n", "2.2.0-beta", false},
		{"2.1.293.1 (Claude Code)\n", "", false},
		{"2.1.293 (Claude Code)\n2.1.294 (Claude Code)\n", "", false},
		{"prefix 2.1.293 (Claude Code)\n", "", false},
		{"Claude Code\n", "", false},
		{"", "", false},
	} {
		t.Run(test.output, func(t *testing.T) {
			version, err := parseCLIVersion(test.output)
			if !test.valid {
				require.ErrorIs(t, err, ErrUnsupported)
				var versionErr *CLIVersionError
				require.ErrorAs(t, err, &versionErr)
				if test.version != "" {
					require.Equal(t, test.version, versionErr.Version)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.version, version.String())
		})
	}
}

func TestNativeOpenRejectsCLIWithoutMinimumVersion(t *testing.T) {
	for _, test := range []struct {
		name, version string
	}{
		{"old", "2.1.292 (Claude Code)"},
		{"prerelease", "2.1.293-beta (Claude Code)"},
		{"unconfirmed", "Claude Code"},
		{"empty", " \n"},
		{"ambiguous", "2.1.293 (Claude Code)\n2.1.294 (Claude Code)"},
		{"oversized", "2.1.293 (Claude Code)" + strings.Repeat(" ", 4<<10)},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := fixtureOptions(t, "echo")
			marker := filepath.Join(t.TempDir(), "started")
			options.Env = append(options.Env,
				"CLAUDE_GO_FIXTURE_VERSION="+test.version,
				"CLAUDE_GO_FIXTURE_STARTED="+marker,
			)
			_, err := Open(t.Context(), SessionOptions(options))
			var versionErr *CLIVersionError
			require.ErrorAs(t, err, &versionErr)
			require.ErrorIs(t, err, ErrUnsupported)
			require.Equal(t, MinimumCLIVersion, versionErr.Minimum)
			require.NoFileExists(t, marker)
		})
	}
}

func TestCheckCLIVersionRejectsInvalidContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"nil", nil, queue.ErrNilContext},
		{"canceled", ctx, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CheckCLIVersion(test.ctx, Options{})
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestNativeOpenAcceptsSupportedCLIVersions(t *testing.T) {
	for _, version := range []string{"2.1.293", "2.1.294", "2.2.0", "3.0.0"} {
		t.Run(version, func(t *testing.T) {
			options := fixtureOptions(t, "echo")
			options.Env = append(options.Env, "CLAUDE_GO_FIXTURE_VERSION="+version+" (Claude Code)")
			session, err := Open(t.Context(), SessionOptions(options))
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })
			require.NoError(t, session.Close())
		})
	}
}

func TestCheckCLIVersionReportsExecutionFailure(t *testing.T) {
	_, err := CheckCLIVersion(context.Background(), Options{CLIPath: t.TempDir() + "/missing-claude"})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrUnsupported)
}
