//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package pi

import (
	"os"
	"syscall"
)

func openHeader(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func openSavedSessionFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
