//go:build !unix

package claudesim

import "errors"

func RunMCPFilter(string, []string) error {
	return errors.New("MCP filter requires a Unix process launcher")
}
