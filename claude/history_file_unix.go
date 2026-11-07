//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package claude

import (
	"os"
	"syscall"
)

// openDiscoveryFile confines reads to the selected config root.
func openDiscoveryFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
