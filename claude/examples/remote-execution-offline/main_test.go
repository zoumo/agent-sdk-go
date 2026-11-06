// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv(peerEnvironment) == "1" {
		if err := peer(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOfflineRemoteExecutionProfile(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, run(&output))

	require.Equal(t, `argv=["--output-format","stream-json","--verbose","--input-format","stream-json","--permission-prompt-tool","stdio","--include-partial-messages","--mcp-config={\"mcpServers\":{\"remote\":{\"headers\":{\"Authorization\":\"Bearer ${REMOTE_TOKEN}\"},\"type\":\"http\",\"url\":\"https://example.invalid/mcp\"}}}","--strict-mcp-config","--tools="]
boundary=EndConversation may remain when MCP tools are present; --tools does not deny MCP tools
evidence=synthetic launcher argv only; no Claude model or remote MCP was contacted
`, output.String())
}
