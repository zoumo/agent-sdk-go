// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDisallowedToolsUseNativeOption(t *testing.T) {
	args, err := interactionArgs(Options{DisallowedTools: []string{"AskUserQuestion"}})
	require.NoError(t, err)
	require.Contains(t, args, "--disallowedTools=AskUserQuestion")

	args, err = interactionArgs(Options{DisallowedTools: []string{"AskUserQuestion", "Bash(git:*)"}})
	require.NoError(t, err)
	require.Contains(t, args, "--disallowedTools=AskUserQuestion,Bash(git:*)")
}

func TestDisallowedToolsOmittedWhenEmpty(t *testing.T) {
	args, err := interactionArgs(Options{})
	require.NoError(t, err)
	for _, arg := range args {
		require.NotContains(t, arg, "--disallowedTools")
	}
}
