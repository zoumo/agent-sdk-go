package replay_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	transcript "github.com/zoumo/agent-sdk-go/internal/testsupport/claude/replay"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/replaycheck"
)

func TestReplayFinalResult(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) { checkReplayFinalResult(t, provider) })
	}
}

// Exercise final strict EOF evidence at the SDK/bridge seam. Extra input after
// full consumption require.NoError remain a typed replay failure visible to the SDK.
func checkReplayFinalResult(t *testing.T, provider string) {
	for _, extra := range []bool{false, true} {
		name := "strict-eof"
		if extra {
			name = "extra-frame-after-completion"
		}
		t.Run(name, func(t *testing.T) {
			var fixture *mocktest.Session
			var args []string
			var err error
			if provider == "codex" {
				data := []byte(`{"version":1,"provenance":"synthetic","frames":[
     {"ordinal":1,"direction":"c2s","epoch":1,"channel":"protocol","wire":{"id":1,"method":"initialize","params":{"clientInfo":{"name":"integration","version":"1"}}}},
     {"ordinal":2,"direction":"s2c","epoch":1,"channel":"protocol","wire":{"id":1,"result":{"userAgent":"synthetic","codexHome":"/fixture/home","platformFamily":"unix","platformOs":"linux"}}},
     {"ordinal":3,"direction":"c2s","epoch":1,"channel":"protocol","wire":{"method":"initialized"}}
    ]}`)
				fixture, err = mocktest.Prepare(t.TempDir(), provider, data, mocktest.Options{})
			} else {
				// Only initialize is needed to reach this transport boundary.
				rows := bytes.Split(bytes.TrimSpace(transcript.TaskFailureWire), []byte{'\n'})
				fixture, err = transcript.Prepare(t.TempDir(), bytes.NewReader(bytes.Join(rows[:2], []byte{'\n'})))
				require.NoError(t, err)
				args, err = claude.NativeArgs(claude.SessionOptions{Env: []string{}})
			}
			require.NoError(t, err)
			run := replaycheck.Start(t, fixture, args)
			var closeInput, closeSDK func() error
			var sdkErr func() error
			var sdkDone <-chan struct{}
			if provider == "codex" {
				client, err := codex.NewStreams(testsupport.Deadline(t), codex.StreamOptions{
					Args: run.Start.NativeArgs, Dir: run.Start.ProtocolCwd, Env: run.Environment(),
					Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "integration", Version: "1"}},
				}, codex.StreamTransport{Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError})
				require.NoError(t, err)
				closeInput, closeSDK, sdkErr = client.CloseInput, client.Close, client.Err
				sdkDone = client.Done()
			} else {
				session, err := claude.OpenStreams(testsupport.Deadline(t), claude.SessionOptions{
					WorkDir: run.Start.Workspace, Env: run.Environment(),
				}, claude.StreamTransport{Reader: run.Reader, Writer: run.Writer, Stderr: run.Stderr, Wait: run.WaitError})
				require.NoError(t, err)
				closeInput, closeSDK, sdkErr = session.CloseInput, session.Close, session.Err
				sdkDone = session.Done()
			}
			t.Cleanup(func() { _ = closeSDK() })
			testsupport.Wait(t, testsupport.Deadline(t), run.FullConsumption())
			if extra {
				_, err = run.Writer.Write([]byte("{}\n"))
				require.NoError(t, err)
			}
			require.NoError(t, closeInput())
			if !extra {
				replaycheck.RequireSuccess(t, run)
				testsupport.Wait(t, testsupport.Deadline(t), sdkDone)
				require.NoError(t, closeSDK())
				assert.NoError(t, sdkErr())
				return
			}
			result, err := run.Wait(testsupport.Deadline(t))
			require.ErrorContains(t, err, "extra client byte after scenario")
			require.NotNil(t, result)
			require.NotNil(t, result.Execution)
			assert.EqualValues(t, "failed", result.Execution.Status)
			require.Len(t, result.Execution.Connections, 1)
			connection := result.Execution.Connections[0]
			assert.True(t, connection.FullConsumption)
			assert.True(t, connection.CleanupComplete)
			assert.False(t, connection.NativeLaunched)
			assert.Nil(t, connection.ExitCode)
			assert.Empty(t, connection.ProcessReceiptPath)
			testsupport.Wait(t, testsupport.Deadline(t), sdkDone)
			// SDK Close joins the authoritative failed testsupport.Wait rather than hiding it.
			require.Error(t, closeSDK())
			assert.Error(t, sdkErr())
		})
	}
}
