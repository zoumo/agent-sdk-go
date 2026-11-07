package agentcodex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

const codexProtocolPeerMode = "AGENT_CODEX_PROTOCOL_PEER"

func TestDuplicateNativeRequestDoesNotRetargetAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, stream, _ := openProtocolInteractionSession(t, ctx, "duplicate-request")

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)

	gap := nextEvent(t, ctx, stream)
	require.Equalf(
		t,
		agent.EventSessionError,
		gap.Type(),
		"payload: %#v, meta: %#v",
		gap.Payload,
		gap.Meta,
	)
	assert.Equal(t, "duplicate_native_request", sessionErrorReason(t, gap))
	assert.NotContains(t, eventCodexMeta(t, gap), "requestRejected")

	receipt, err := session.Answer(ctx, requested.Interaction, agent.PermissionAnswer{
		Outcome:  agent.PermissionSelected,
		OptionID: "allow-once",
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.Sent)

	require.NoError(t, session.Close(ctx))
	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionAnswered, closed.Outcome)
	assert.Equal(t, "session_closed", closed.Reason)
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
}

func TestTerminalNativeRequestFailureClosesPendingInteraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, stream, signalPath := openProtocolInteractionSession(t, ctx, "conflicting-request")

	submitInteractionTurn(t, ctx, session)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	requested := nextInteractionRequested(t, ctx, stream)
	require.NoError(t, os.WriteFile(signalPath, nil, 0o600))

	closed := nextInteractionClosed(t, ctx, stream)
	assert.Equal(t, requested.Interaction, closed.Interaction)
	assert.Equal(t, agent.InteractionCancelled, closed.Outcome)
	assert.Equal(t, "session_failed", closed.Reason)

	_, err := stream.Next(ctx)
	require.Error(t, err)
	assert.Equal(t, agent.ErrorUnavailable, agent.CodeOf(err))

	var classified *agent.Error
	require.ErrorAs(t, err, &classified)
	assert.Equal(t, "session_terminated", classified.Reason)
	require.Equal(t, err, session.Err())
	assert.NoError(t, session.Close(ctx))
}

func openProtocolInteractionSession(
	t *testing.T,
	ctx context.Context,
	mode string,
) (testSession, agent.EventStream, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, codexProtocolPeerMode+"=") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, codexProtocolPeerMode+"="+mode)
	signalPath := filepath.Join(t.TempDir(), "protocol-peer.signal")
	environment = append(environment, "AGENT_CODEX_PROTOCOL_SIGNAL="+signalPath)
	provider := agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
		Executable: executable,
		Args:       []string{"-test.run=^TestAgentCodexProtocolPeer$"},
		Env:        environment,
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{Name: "agent-codex-protocol-test", Version: "1"},
		},
	}})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err = provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	t.Cleanup(func() {
		closeErr := session.Close(context.Background())
		if closeErr != nil {
			assert.Equal(t, agent.ErrorClosed, agent.CodeOf(closeErr))
		}
	})
	stream, err := session.Events()
	require.NoError(t, err)
	concrete, ok := session.(testSession)
	require.True(t, ok)
	return concrete, stream, signalPath
}

// TestAgentCodexProtocolPeer is an independently authored app-server peer. It
// only runs in a child test process selected by openProtocolInteractionSession.
func TestAgentCodexProtocolPeer(t *testing.T) {
	mode := os.Getenv(codexProtocolPeerMode)
	if mode == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			os.Exit(81)
		}
		switch message.Method {
		case "initialize":
			replyProtocolPeer(message.ID, `{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`)
			fmt.Fprintln(os.Stdout, `{"method":"initialized"}`)
		case "initialized":
		case "thread/start":
			replyProtocolPeer(message.ID, codexThreadStartResult("root-a"))
		case "turn/start":
			replyProtocolPeer(message.ID, `{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}`)
			fmt.Fprintln(os.Stdout, codexTurnStartedFrame[1:])
			switch mode {
			case "duplicate-request":
				fmt.Fprintln(os.Stdout, commandApprovalRequest("approval-1")[1:])
				fmt.Fprintln(os.Stdout, commandApprovalRequest("approval-1")[1:])
			case "conflicting-request":
				fmt.Fprintln(os.Stdout, commandApprovalRequest("approval-1")[1:])
				waitProtocolPeerSignal()
				fmt.Fprintln(os.Stdout, `{"id":"approval-1","method":"item/fileChange/requestApproval","params":{"threadId":"root-a","turnId":"turn-a","itemId":"file-1","startedAtMs":1700000200000}}`)
			}
		case "thread/unsubscribe":
			replyProtocolPeer(message.ID, `{"status":"unsubscribed"}`)
			return
		case "":
			if len(message.ID) == 0 {
				os.Exit(82)
			}
		default:
			os.Exit(83)
		}
	}
	if err := scanner.Err(); err != nil {
		os.Exit(84)
	}
}

func waitProtocolPeerSignal() {
	path := os.Getenv("AGENT_CODEX_PROTOCOL_SIGNAL")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(86)
}

func replyProtocolPeer(id json.RawMessage, result string) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(result)); err != nil {
		os.Exit(85)
	}
	fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", id, compact.Bytes())
}
