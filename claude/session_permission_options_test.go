// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeArgsSessionPermissions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options SessionOptions
		want    []string
	}{
		{name: "zero"},
		{name: "empty sandbox", options: SessionOptions{Sandbox: &SandboxOptions{}}},
		{
			name:    "opt in only",
			options: SessionOptions{AllowDangerouslySkipPermissions: true},
			want:    []string{"--allow-dangerously-skip-permissions"},
		},
		{
			name: "keep initial mode when opting in",
			options: SessionOptions{
				PermissionMode:                  PermissionModeDefault,
				AllowDangerouslySkipPermissions: true,
			},
			want: []string{"--permission-mode=default", "--allow-dangerously-skip-permissions"},
		},
		{
			name: "initial bypass with explicit sandbox disable",
			options: SessionOptions{
				PermissionMode: PermissionModeBypassPermissions,
				Sandbox:        &SandboxOptions{Enabled: new(false)},
			},
			want: []string{"--permission-mode=bypassPermissions", `--settings={"sandbox":{"enabled":false}}`},
		},
		{
			name: "sandbox keys set independently",
			options: SessionOptions{
				Sandbox: &SandboxOptions{
					FailIfUnavailable:        new(false),
					AllowUnsandboxedCommands: new(true),
				},
			},
			want: []string{`--settings={"sandbox":{"failIfUnavailable":false,"allowUnsandboxedCommands":true}}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := NativeArgs(tc.options)
			require.NoError(t, err)
			assert.Equal(t, append(slices.Clone(baseInteractionArgs), tc.want...), args)
			options := SessionOptions(OptionsFromTypes(Options(tc.options).Types()))
			assert.Equal(t, tc.options, options)
		})
	}
}

func TestOpenLaunchesNativePermissionArgs(t *testing.T) {
	const resumeID = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name       string
		resumeID   string
		mode       PermissionMode
		sandbox    *SandboxOptions
		permission []string
	}{
		{name: "omitted on start"},
		{name: "omitted on resume", resumeID: resumeID},
		{
			name:       "explicit on start",
			mode:       PermissionModePlan,
			permission: []string{"--permission-mode=plan"},
		},
		{
			name:     "explicit on resume",
			resumeID: resumeID,
			mode:     PermissionModeAuto,
			sandbox: &SandboxOptions{
				Enabled:                  new(true),
				FailIfUnavailable:        new(true),
				AllowUnsandboxedCommands: new(false),
			},
			permission: []string{
				"--permission-mode=auto",
				`--settings={"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := fixtureOptions(t, "echo")
			path := filepath.Join(options.WorkDir, "argv.json")
			options.Env = append(options.Env, "CLAUDE_GO_CAPTURE="+path)
			options.ResumeID = tc.resumeID
			options.PermissionMode = tc.mode
			options.Sandbox = tc.sandbox

			session, err := Open(t.Context(), SessionOptions(options))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, session.Close()) })
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var launched struct {
				Argv []string `json:"argv"`
			}
			require.NoError(t, json.Unmarshal(data, &launched))

			want := append(slices.Clone(baseInteractionArgs), tc.permission...)
			if tc.resumeID != "" {
				want = append(want, "--resume="+tc.resumeID)
			}
			assert.Equal(t, want, launched.Argv)
		})
	}
}

func TestInvalidInitialPermissionModeRejectedBeforeLaunch(t *testing.T) {
	options := SessionOptions{
		CLIPath:        filepath.Join(t.TempDir(), "must-not-execute"),
		PermissionMode: "futureMode",
	}
	args, err := NativeArgs(options)
	require.ErrorIs(t, err, ErrInvalidInput)
	assert.Nil(t, args)
	session, err := Open(context.Background(), options)
	require.ErrorIs(t, err, ErrInvalidInput)
	assert.Nil(t, session)
}
