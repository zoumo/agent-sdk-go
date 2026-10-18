// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivatePeerCannotBypassCIGuard(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	fake := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf started > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRANSCRIPT_MODE", "record")
	t.Setenv("SDK_REAL_AGENT_E2E", "1")
	t.Setenv("TRANSCRIPT_CLI", fake)
	t.Setenv("TRANSCRIPT_STORY", "s1")
	t.Setenv("CI", "1")
	if proxy() == nil {
		t.Fatal("CI peer bypass")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("CLI was started")
	}
}
