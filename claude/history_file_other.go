//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package claude

import (
	"errors"
	"os"
)

func openHistoryFile(path string) (*os.File, error) {
	// The local transcript reader is currently validated only on Unix.
	return nil, errors.New("claude history: local transcript loading unsupported on this platform")
}
