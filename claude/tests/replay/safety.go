// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"fmt"
	"regexp"
)

var secrets = regexp.MustCompile(`(?i)(sk-(?:ant-)?[a-z0-9_-]{12,}|bearer[ :]+[a-z0-9._-]{8,}|authorization|access_token|refresh_token|api[_-]?key[" ]*[:=]|/users/|/home/|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})`)

func Scan(data []byte) error {
	if secrets.Match(data) {
		return fmt.Errorf("sensitive content rejected")
	}
	return nil
}
