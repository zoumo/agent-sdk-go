// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var baseInteractionArgs = []string{
	"--output-format", "stream-json",
	"--verbose",
	"--input-format", "stream-json",
	"--permission-prompt-tool", "stdio",
	"--include-partial-messages",
}

func captureArgs(t *testing.T, configure func(*Options)) []string {
	t.Helper()
	opts := fixtureOptions(t, "echo")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	configure(&opts)
	c, err := openTestInteraction(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var launched struct {
		Argv []string
	}
	require.NoError(t, json.Unmarshal(data, &launched))
	return launched.Argv
}

func TestToolsControlledLauncherArguments(t *testing.T) {
	for _, tc := range []struct {
		name            string
		tools           []string
		disallowedTools []string
		wantExtra       []string
	}{
		{name: "nil", wantExtra: nil},
		{name: "empty", tools: []string{}, wantExtra: nil},
		{name: "all built-ins disabled", tools: []string{""}, wantExtra: []string{"--tools="}},
		{name: "allowlist", tools: []string{"Bash", "Read"}, wantExtra: []string{"--tools=Bash,Read"}},
		{name: "unknown name passed literally", tools: []string{"FutureTool"}, wantExtra: []string{"--tools=FutureTool"}},
		{
			name:            "allowlist with deny rules",
			tools:           []string{"Read"},
			disallowedTools: []string{"mcp__*", "Bash(git:*)"},
			wantExtra:       []string{"--tools=Read", "--disallowedTools=mcp__*,Bash(git:*)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := append(append([]string(nil), baseInteractionArgs...), tc.wantExtra...)
			argv := captureArgs(t, func(options *Options) {
				options.Tools = tc.tools
				options.DisallowedTools = tc.disallowedTools
			})
			require.Equal(t, want, argv)
		})
	}
}

func TestToolsRejectInvalidAllowlistsBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []string
	}{
		{name: "leading empty", tools: []string{"", "Bash"}},
		{name: "trailing empty", tools: []string{"Bash", ""}},
		{name: "multiple empty", tools: []string{"", ""}},
		{name: "invalid UTF-8", tools: []string{string([]byte{0xff})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fixtureOptions(t, "echo")
			opts.CLIPath = filepath.Join(opts.WorkDir, "must-not-exist")
			opts.Tools = tc.tools
			c, err := openTestInteraction(context.Background(), opts)
			require.Nil(t, c)
			assert.ErrorIs(t, err, ErrInvalidInput)
		})
	}
}

func TestToolsFreezeCallerSlice(t *testing.T) {
	tools := []string{"Bash"}
	opts := fixtureOptions(t, "echo")
	opts.Tools = tools
	c, err := openTestInteraction(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })

	tools[0] = "Read"
	require.Equal(t, []string{"Bash"}, c.opts.Tools)
}

func TestToolsIsolatedAcrossRepeatedOpen(t *testing.T) {
	tools := []string{"Bash"}
	captureTools := func(options *Options) { options.Tools = tools }
	first := captureArgs(t, captureTools)
	tools[0] = "Read"
	second := captureArgs(t, captureTools)

	firstWant := append(append([]string(nil), baseInteractionArgs...), "--tools=Bash")
	secondWant := append(append([]string(nil), baseInteractionArgs...), "--tools=Read")
	assert.Equal(t, firstWant, first)
	assert.Equal(t, secondWant, second)
}
