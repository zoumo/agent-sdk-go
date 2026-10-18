// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrivatePeerCannotBypassCIGuard(t *testing.T) {
	for _, ci := range []string{"1", "true"} {
		t.Run(ci, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			fake := filepath.Join(dir, "fake-cli")
			require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nprintf started > '"+marker+"'\n"), 0700))
			t.Setenv("TRANSCRIPT_MODE", "record")
			t.Setenv("SDK_REAL_AGENT_E2E", "1")
			t.Setenv("TRANSCRIPT_CLI", fake)
			t.Setenv("TRANSCRIPT_STORY", "s1")
			t.Setenv("CI", ci)
			require.Error(t, proxy(), "CI peer bypass")
			_, err := os.Stat(marker)
			require.ErrorIs(t, err, os.ErrNotExist, "CLI was started")
		})
	}
}
