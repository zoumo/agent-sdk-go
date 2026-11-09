// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendSystemPromptArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty"},
		{name: "literal", value: "Follow the repository conventions."},
		{name: "unicode", value: "附加指令：ünïcødé 🚀 中文"},
		{name: "multi-line", value: "first line\nsecond line\r\nthird line\t"},
		{name: "dash-leading", value: "--tools=\n--model=other\n--append-system-prompt=injected"},
		{name: "surrounding spaces", value: "  keep these spaces  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := append([]string(nil), baseInteractionArgs...)
			if tc.value != "" {
				want = append(want, "--append-system-prompt="+tc.value)
			}
			argv := captureArgs(t, func(options *Options) {
				options.AppendSystemPrompt = tc.value
			})
			require.Equal(t, want, argv)
		})
	}
}

func TestAppendSystemPromptRejectedBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		sentinel string
	}{
		{
			name:     "invalid UTF-8",
			value:    "prompt-secret-" + string([]byte{0xff}),
			sentinel: "prompt-secret",
		},
		{
			name:     "NUL",
			value:    "prompt-secret\x00tail",
			sentinel: "prompt-secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fixtureOptions(t, "echo")
			opts.CLIPath = filepath.Join(opts.WorkDir, "must-not-execute")
			opts.AppendSystemPrompt = tc.value
			c, err := openTestInteraction(context.Background(), opts)
			require.Nil(t, c)
			require.ErrorIs(t, err, ErrInvalidInput)
			assert.NotContains(t, err.Error(), tc.sentinel)
		})
	}
}
