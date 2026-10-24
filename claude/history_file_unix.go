//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package claude

import (
	"os"
	"syscall"
)

// A FIFO/device substitution must not block open before regularity is checked.
// Do not follow a final-component symlink into another session source.
func openHistoryFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

// openDiscoveryFile confines reads to the selected config root.
func openDiscoveryFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
