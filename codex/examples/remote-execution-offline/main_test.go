// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
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

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	require.Len(t, lines, 5)
	require.Equal(t, "methods=initialize,initialized,thread/start,thread/unsubscribe", lines[0])
	require.Equal(t, []string{
		"boundary=apply_patch may remain and local zero execution is not guaranteed",
		"isolation=ambient MCP config may merge without an isolated CODEX_HOME or outer sandbox; use external sandbox/permission policy for stronger isolation",
		"evidence=synthetic app-server params only; no Codex model or remote MCP was contacted",
	}, lines[2:])
	params, ok := strings.CutPrefix(lines[1], "thread_start_params=")
	require.True(t, ok)
	require.JSONEq(t, `{
		"ephemeral": true,
		"config": {
			"features.image_generation": false,
			"features.shell_tool": false,
			"features.view_image": false,
			"mcp_servers.remote": {
				"url": "https://example.invalid/mcp",
				"env_http_headers": {"Authorization": "REMOTE_AUTHORIZATION"}
			}
		}
	}`, params)
}
