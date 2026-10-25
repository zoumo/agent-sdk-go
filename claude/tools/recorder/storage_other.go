// SPDX-License-Identifier: Apache-2.0
//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package recorder

import (
	"fmt"
	"os"
)

func openStorageFile(root *os.Root, name string) (*os.File, error) {
	return nil, fmt.Errorf("nonblocking no-follow storage capture unsupported on this platform")
}
