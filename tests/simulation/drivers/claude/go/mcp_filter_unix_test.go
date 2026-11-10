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
	runRecordedContinuation(t, true)
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
