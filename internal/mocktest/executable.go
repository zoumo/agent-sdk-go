package mocktest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Executable returns the explicit, prebuilt agent-sdk-testify binary.
// Tests never search PATH, build tools implicitly, or fall back to an Agent.
func Executable() (string, error) {
	path := os.Getenv("AGENT_SDK_TESTIFY_BIN")
	if !filepath.IsAbs(path) {
		return "", errors.New("AGENT_SDK_TESTIFY_BIN must name an absolute separately built agent-sdk-testify executable")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("AGENT_SDK_TESTIFY_BIN: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("AGENT_SDK_TESTIFY_BIN is not an executable file: %s", path)
	}
	return path, nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
