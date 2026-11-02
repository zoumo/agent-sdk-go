package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRemoteMissingProbeCancelsAndReapsOwnedPeer(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "peer.pid")
	peer := command{Command: "/bin/sh", Args: []string{"-c", "echo $$ > '" + pidPath + "'; cat; sleep 60"}}
	service, err := startMockRemote(context.Background(), map[string]command{"main": peer, "probe": peer}, root)
	require.NoError(t, err)
	defer service.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	main := service.endpoint.Connections["main"]
	conn, _, err := websocket.Dial(ctx, main.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + main.BearerToken}}}) //nolint:bodyclose // websocket.Dial owns the handshake body.
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.Eventually(t, func() bool { _, e := os.Stat(pidPath); return e == nil }, time.Second, 5*time.Millisecond)
	data, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	cleanupErr := service.close()
	require.ErrorContains(t, cleanupErr, "1/2 required connections")
	require.NotContains(t, cleanupErr.Error(), "cleanup did not complete")
	require.NotContains(t, cleanupErr.Error(), "completion unavailable")
	require.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH, "owned process was not reaped")
}

func TestRemoteRoutesByDeclaredConnectionID(t *testing.T) {
	root := t.TempDir()
	marker := func(name string) command {
		return command{Command: "/bin/sh", Args: []string{"-c", "printf %s > " + filepath.Join(root, name) + ".called; cat"}}
	}
	service, err := startMockRemote(context.Background(), map[string]command{"main": marker("main"), "probe": marker("probe")}, root)
	require.NoError(t, err)
	defer service.close()

	dial := func(id string) *websocket.Conn {
		endpoint := service.endpoint.Connections[id]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, endpoint.URL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + endpoint.BearerToken}}}) //nolint:bodyclose // websocket.Dial owns the handshake body.
		require.NoError(t, err)
		return conn
	}

	probe := dial("probe")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "probe.called"))
		return err == nil
	}, time.Second, 5*time.Millisecond)
	require.NoFileExists(t, filepath.Join(root, "main.called"))
	require.NoError(t, probe.Close(websocket.StatusNormalClosure, ""))

	main := dial("main")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "main.called"))
		return err == nil
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, main.Close(websocket.StatusNormalClosure, ""))
	require.NoError(t, service.close())
}
