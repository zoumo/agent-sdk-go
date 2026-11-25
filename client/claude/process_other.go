//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

// SPDX-License-Identifier: Apache-2.0
package claude

import "os/exec"

func prepareProcess(cmd *exec.Cmd)                     {}
func terminateProcess(cmd *exec.Cmd, force bool) error { return cmd.Process.Kill() }
