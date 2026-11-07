//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package claude

import (
	"os"
)

// openDiscoveryFile confines reads to the selected config root.
func openDiscoveryFile(root *os.Root, path string) (*os.File, error) {
	return nil, ErrUnsupported
}
