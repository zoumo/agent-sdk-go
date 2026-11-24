//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package pi

import "os"

func openHeader(path string) (*os.File, error) {
	return os.Open(path)
}
