// SPDX-License-Identifier: Apache-2.0
//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package recorder

import (
	"os"
	"syscall"
)

func openStorageFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
