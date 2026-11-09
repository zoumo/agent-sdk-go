package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func TestRemoteRequiresDeclaredMainAndProbe(t *testing.T) {
	root := t.TempDir()
	scenario := `{"version":1,"provenance":"synthetic","frames":[{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"wire":{"method":"notice","type":"notice"}}]}`
	session, err := mocktest.Prepare(t.TempDir(), "codex", []byte(scenario), mocktest.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })

	service, err := startMockRemote(context.Background(), nil, root)
	require.ErrorContains(t, err, "prepared fixture")
	require.Nil(t, service)

	service, err = startMockRemote(context.Background(), session, root)
	require.ErrorContains(t, err, "main and probe")
	require.Nil(t, service)
}

func TestRemoteAuthenticatesOnlyDeclaredConnectionPaths(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	tool, err := discoverTool()
	require.NoError(t, err)
	session, err := prepareSession(t.Context(), tool, sourceSelection{
		Provider: "codex",
		Case:     "SDK-CASE-032",
	}, sessionDir, "test")
	require.NoError(t, err)
	t.Cleanup(func() {
		if session.Runner != nil {
			_ = session.Runner.Close()
		}
		_ = cleanupPreparedSession(session.Dir)
	})

	service, err := startMockRemote(context.Background(), session.Runner, t.TempDir())
	require.NoError(t, err)
	client := &http.Client{Timeout: 5 * time.Second}

	mainURL := strings.Replace(service.endpoint.Connections["main"].URL, "ws://", "http://", 1)
	unknown, err := client.Get(mainURL + "/extra")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, unknown.StatusCode)
	require.NoError(t, unknown.Body.Close())

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		mainURL, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer wrong")
	unauthorized, err := client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, unauthorized.StatusCode)
	require.NoError(t, unauthorized.Body.Close())

	require.ErrorContains(t, service.close(), "0/2 required connections")
	require.NoError(t, session.Runner.Close())
}
