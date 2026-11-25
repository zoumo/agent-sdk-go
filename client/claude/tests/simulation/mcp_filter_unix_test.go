//go:build unix

package claudesim

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "--filter-mcp" {
		if err := RunMCPFilter(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	os.Exit(m.Run())
}

func TestRecordedContinuationWithStartupMCP(t *testing.T) {
	t.Run("legacy handshake", func(t *testing.T) {
		runRecordedContinuation(t, `{"subtype":"initialize","hooks":null}`,
			`<{"type":"control_request","request_id":"mcp","request":{"subtype":"mcp_message","server_name":"fixture","message":{"jsonrpc":"2.0","id":0,"method":"initialize"}}}`,
			`>{"type":"control_response","response":{"subtype":"success","request_id":"mcp","response":{"mcp_response":{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fixture","version":"1.0.0"}}}}}}`)
	})
	t.Run("manifest with live fallback", func(t *testing.T) {
		runRecordedContinuation(t,
			`{"subtype":"initialize","sdkMcpServers":["fixture"],"sdkMcpServerManifests":{"fixture":{"initializeResult":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"fixture","version":"1.0.0"}},"toolsListResult":{"tools":[{"name":"fixture_read","description":"Read only the synthetic numbers.txt file.","inputSchema":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"path":{"type":"string","const":"numbers.txt"}},"required":["path"]},"execution":{"taskSupport":"forbidden"}}]}}}}`,
			`<{"type":"control_request","request_id":"mcp","request":{"subtype":"mcp_message","server_name":"fixture","message":{"jsonrpc":"2.0","id":0,"method":"initialize"}}}`,
			`>{"type":"control_response","response":{"subtype":"success","request_id":"mcp","response":{"mcp_response":{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"fixture","version":"1.0.0"}}}}}}`)
	})
}

func TestMCPSessionShimFiltersOnlySyntheticConfig(t *testing.T) {
	args := []string{
		"--output-format", "stream-json",
		`--mcp-config={"mcpServers":{"fixture":{"type":"sdk","name":"fixture"}}}`,
		"--verbose",
	}
	filtered, err := FilterMCPConfig(args)
	require.NoError(t, err)
	require.Equal(t, []string{"--output-format", "stream-json", "--verbose"}, filtered)

	_, err = FilterMCPConfig([]string{`--mcp-config={"mcpServers":{"fixture":{"type":"stdio","name":"fixture"}}}`})
	require.ErrorContains(t, err, "not the SDK synthetic declaration")
}
