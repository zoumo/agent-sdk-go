//go:build unix

package claudesim

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// RunMCPFilter validates the SDK's synthetic declaration and replaces the shim
// process with the prepared native mock launcher.
func RunMCPFilter(target string, args []string) error {
	if !filepath.IsAbs(target) {
		return errors.New("MCP filter launcher must be absolute")
	}
	filtered, err := FilterMCPConfig(args)
	if err != nil {
		return err
	}
	argv := append([]string{target}, filtered...)
	return syscall.Exec(target, argv, os.Environ())
}
