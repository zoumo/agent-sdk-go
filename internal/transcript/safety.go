// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// LiveGuard cannot be enabled through the legacy initialization smoke flag.
func LiveGuard(mode, optin, ci, cli string) error {
	if ci != "" {
		return fmt.Errorf("real recording/E2E is forbidden in CI")
	}
	if (mode != "record" && mode != "e2e") || optin != "1" {
		return fmt.Errorf("explicit record/e2e mode and SDK_REAL_AGENT_E2E=1 required")
	}
	if !filepath.IsAbs(cli) {
		return fmt.Errorf("absolute CLI path required")
	}
	return nil
}

var secrets = regexp.MustCompile(`(?i)(sk-(?:ant-)?[a-z0-9_-]{12,}|bearer[ :]+[a-z0-9._-]{8,}|authorization|access_token|refresh_token|api[_-]?key[" ]*[:=]|/users/|/home/|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})`)

func Scan(data []byte) error {
	if secrets.Match(data) {
		return fmt.Errorf("sensitive content rejected")
	}
	return nil
}

// SafeFile permits only an exact, non-symlink synthetic input/output path.
func SafeFile(root, path, name string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	if abs != filepath.Join(realRoot, name) {
		return false
	}
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		p, e := filepath.EvalSymlinks(filepath.Dir(abs))
		return e == nil && p == realRoot
	}
	return err == nil && info.Mode().IsRegular() && !strings.Contains(name, string(filepath.Separator))
}
