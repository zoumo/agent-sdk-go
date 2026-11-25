package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStdioRequestRejectsInvalidConnectionBeforeBusinessOperations(t *testing.T) {
	for _, connection := range []string{"", "probe", "unknown"} {
		t.Run(connection, func(t *testing.T) {
			id, err := acceptRequest(context.Background(), "codex", "structured-input", t.TempDir(), driverPeer{FixtureDir: t.TempDir(), Connection: connection}, "preserve")
			require.Empty(t, id)
			require.ErrorContains(t, err, "stdio connection must be main")
		})
	}
}
