//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package pi

import "os"

func openHeader(path string) (*os.File, error) {
	return os.Open(path)
}

func openSavedSessionFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
