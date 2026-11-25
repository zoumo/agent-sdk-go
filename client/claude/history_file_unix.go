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

func childFileIdentityOf(info os.FileInfo) (childFileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return childFileIdentity{}, false
	}
	return childFileIdentity{device: int64(stat.Dev), inode: int64(stat.Ino)}, true
}
