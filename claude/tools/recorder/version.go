// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

var requiredFlags = []string{"--model", "--print", "--restricted", "--safe-mode", "--no-session-persistence", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", "--no-chrome", "--setting-sources", "--settings", "--tools", "--max-budget-usd", "--system-prompt"}

// ValidateVersion checks exact chosen source/flag evidence, without invoking a
// CLI. Help/version claims alone do not prove behavior or launch authorization.
func ValidateVersion(version, help []byte) error {
	if string(version) != CLIVersion+" (Claude Code)\n" {
		return fmt.Errorf("unreviewed CLI version; pin is %s", CLIVersion)
	}
	if len(help) > 256<<10 {
		return fmt.Errorf("help byte limit")
	}
	flags := map[string]bool{}
	for _, word := range strings.Fields(string(help)) {
		flags[strings.TrimRight(word, ",=")] = true
	}
	for _, name := range requiredFlags {
		if !flags[name] {
			return fmt.Errorf("reviewed CLI flag missing: %s", name)
		}
	}
	return nil
}

// ValidateCLIBinary verifies the bounded regular executable and exact approved
// digest without executing it or inspecting credentials/configuration. Native
// Linux 2.1.258 is 215473560 bytes; the reviewed ceiling is 256 MiB.
func ValidateCLIBinary(path, approvedDigest string) error {
	if err := canonicalPath(path); err != nil {
		return err
	}
	b, err := hex.DecodeString(approvedDigest)
	if err != nil || len(b) != 32 || strings.ToLower(approvedDigest) != approvedDigest {
		return fmt.Errorf("approved CLI SHA256 required")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > 256<<20 {
		f.Close()
		return fmt.Errorf("bounded regular executable required")
	}
	h := sha256.New()
	_, err = io.Copy(h, io.LimitReader(f, (256<<20)+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || !bytes.Equal(h.Sum(nil), b) {
		return fmt.Errorf("CLI digest mismatch or unreadable")
	}
	return nil
}
func inspectCLI(ctx context.Context, path, approvedDigest string, workspace *Workspace) (string, error) {
	if err := ValidateCLIBinary(path, approvedDigest); err != nil {
		return "", err
	}
	version, err := probeCLI(ctx, path, "--version", workspace)
	if err != nil {
		return "", err
	}
	help, err := probeCLI(ctx, path, "--help", workspace)
	if err != nil {
		return "", err
	}
	return approvedDigest, ValidateVersion(version, help)
}
func probeCLI(ctx context.Context, path, arg string, workspace *Workspace) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	// A replacement environment avoids implicit auth/config lookup. Even these
	// probes are forbidden until Preflight establishes policy isolation.
	cmd.Env = workspace.Environment()
	cmd.Dir = workspace.Project
	var out bytes.Buffer
	cmd.Stdout = &observationWriter{destination: &out, remaining: 256 << 10}
	cmd.Stderr = &observationWriter{destination: io.Discard, remaining: 256 << 10}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bounded CLI probe failed")
	}
	return out.Bytes(), nil
}
