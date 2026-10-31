package mocktest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Executable returns the explicit, prebuilt agent-sdk-testify binary.
// Tests never search PATH, build tools implicitly, or fall back to an Agent.
func Executable() (string, error) {
	path := os.Getenv("AGENT_SDK_TESTIFY_BIN")
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("AGENT_SDK_TESTIFY_BIN must name an absolute Go agent-sdk-testify executable; build it with scripts/with-agent-sdk-testify.sh")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("AGENT_SDK_TESTIFY_BIN: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("AGENT_SDK_TESTIFY_BIN is not an executable file: %s", path)
	}
	return path, nil
}

// Launcher binds explicit mock arguments for SDK process interfaces that only
// expose an executable path (Claude). The caller owns dir and its cleanup.
// The shell immediately execs the binary, preserving signals and exit status.
func Launcher(dir, provider, scenario, receipt string) (string, error) {
	executable, err := Executable()
	if err != nil {
		return "", err
	}
	if provider != "codex" && provider != "claude" {
		return "", fmt.Errorf("unsupported mock provider %q", provider)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := "#!/bin/sh\nexec " + quote(executable) + " protocol mock --provider " + quote(provider) + " --scenario " + quote(scenario)
	if receipt != "" {
		command += " --receipt " + quote(receipt)
	}
	command += " \"$@\"\n"
	path := filepath.Join(dir, "agent-sdk-testify-"+provider)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return "", err
	}
	_, writeErr := f.WriteString(command)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	return path, closeErr
}
