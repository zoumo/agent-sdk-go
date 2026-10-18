package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--fixture" {
		if fixture() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Verify the documented transcript through the public client and fake process.
func TestOfflineTranscript(t *testing.T) {
	var output bytes.Buffer
	{
		err := run(&output)
		require.NoError(t, err)
	}

	expected, err := os.ReadFile("expected.txt")
	require.NoError(t, err)
	require.Equal(t, string(expected), output.String(),
		"transcript differs:\n%s", output.String())

}
