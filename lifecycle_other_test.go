//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

// SPDX-License-Identifier: Apache-2.0
package claude

import "bufio"

// Process-group fixtures are intentionally absent on unsupported platforms.
func runLifecycleDescendant()                   {}
func runLifecycleFixture(string, *bufio.Reader) {}
