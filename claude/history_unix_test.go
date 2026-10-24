//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package claude

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryRejectsFIFOWithoutBlockingClose(t *testing.T) {
	o := historyTranscript(t, map[string]any{"type": "user", "uuid": "u", "parentUuid": nil, "message": map[string]any{"content": "x"}})
	path := historyPath(t, o)
	require.NoError(t, os.Remove(path))
	require.NoError(t, syscall.Mkfifo(path, 0600))
	h, e := readTranscriptForTest(context.Background(), o, nil)
	require.NoError(t, e)
	done := make(chan error, 1)
	go func() { done <- h.Close() }()
	select {
	case e := <-done:
		require.NoError(t, e)
	case <-time.After(time.Second):
		t.Fatal("FIFO open blocked cancellation")
	}
	require.Error(t, h.Err())
}
