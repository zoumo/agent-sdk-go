package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestClaimSessionBindsNativeIdentityBeforeInput(t *testing.T) {
	const claimID = "22222222-2222-4222-8222-222222222222"
	options := fixtureOptions(t, "claim")
	options.Env = append(options.Env, "CLAUDE_GO_CLAIM_ID="+claimID)
	options.ClaimSession = true
	args, err := NativeArgs(SessionOptions(options))
	require.NoError(t, err)
	assert.Contains(t, args, "--await-claim")
	for _, arg := range args {
		assert.False(t, strings.HasPrefix(arg, "--session-id="))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := Open(ctx, SessionOptions(options))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	id, known := session.SessionID()
	require.True(t, known)
	require.Equal(t, claimID, id)

	coverage, err := session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, ReceivedObservation, coverage.Kind)
	require.NotNil(t, coverage.Observation)
	require.Equal(t, "forwarding-not-confirmed", coverage.Observation.Reason)
	require.Equal(t, "initialize-forwardSubagentText-is-a-request", coverage.Observation.Evidence)

	warmup, err := session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "status", warmup.Event.Subtype)
	require.Equal(t, claimID, warmup.Event.SessionID)

	init, err := session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "init", init.Event.Subtype)
	require.Equal(t, claimID, init.Event.SessionID)

	_, err = session.Submit(ctx, Input{Text: "hello"})
	require.NoError(t, err)
	seen := map[string]bool{}
	for !seen["result"] {
		received, err := session.Receive(ctx)
		require.NoError(t, err)
		if received.Event == nil || received.Event.SessionID == "" {
			continue
		}
		require.Equal(t, claimID, received.Event.SessionID)
		if received.Event.Subtype != "" {
			seen[received.Event.Subtype] = true
		}
		if received.Event.Assistant != nil {
			seen["assistant"] = true
		}
		if received.Event.Result != nil {
			seen["result"] = true
		}
	}
	require.True(t, seen["assistant"])
	require.NoError(t, session.Close())
}

func TestClaimSessionRejectionFailsOpen(t *testing.T) {
	options := fixtureOptions(t, "claim_reject")
	options.ClaimSession = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Open(ctx, SessionOptions(options))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_spare")
}

func TestClaimSessionAndAssignedIdentityAreMutuallyExclusive(t *testing.T) {
	options := SessionOptions{CLIPath: "/nonexistent", ClaimSession: true, SessionID: "assigned"}
	_, err := Open(context.Background(), options)
	require.ErrorIs(t, err, ErrInvalidInput)
	options.SessionID = ""
	options.ResumeID = "resumed"
	_, err = Open(context.Background(), options)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestExplicitSessionIDStillUsesSessionIDFlag(t *testing.T) {
	options := SessionOptions{CLIPath: "/nonexistent", SessionID: "assigned"}
	args, err := NativeArgs(options)
	require.NoError(t, err)
	assert.Contains(t, args, "--session-id=assigned")
	assert.False(t, slices.Contains(args, "--await-claim"))
}

func TestStreamClaimValidatesBindingAndPreservesOptions(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*claimReply)
	}{
		{name: "native binding"},
		{name: "missing session ID", change: func(reply *claimReply) { reply.SessionID = "" }},
		{name: "missing directory", change: func(reply *claimReply) { reply.CWD = "" }},
		{name: "different directory", change: func(reply *claimReply) { reply.CWD = "/different-claim-workspace" }},
		{name: "unsuccessful status", change: func(reply *claimReply) { reply.Status = "refused" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cwd := t.TempDir()
			reader, peerOutput := io.Pipe()
			peerInput, writer := io.Pipe()
			t.Cleanup(func() {
				_ = reader.Close()
				_ = writer.Close()
				_ = peerInput.Close()
				_ = peerOutput.Close()
			})
			requests := make(chan claimRequest, 1)
			peerErrors := make(chan error, 1)
			go func() {
				defer peerOutput.Close()
				scanner := bufio.NewScanner(peerInput)
				encoder := json.NewEncoder(peerOutput)
				for scanner.Scan() {
					var frame struct {
						RequestID string       `json:"request_id"`
						Request   claimRequest `json:"request"`
					}
					if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
						peerErrors <- err
						return
					}
					var response any = struct{}{}
					if frame.Request.Subtype == "claim_session" {
						requests <- frame.Request
						reply := claimReply{Status: "ok", SessionID: "native-claimed-id", CWD: cwd}
						if test.change != nil {
							test.change(&reply)
						}
						response = reply
					}
					if err := encoder.Encode(map[string]any{
						"type": "control_response", "response": map[string]any{
							"subtype": "success", "request_id": frame.RequestID, "response": response,
						},
					}); err != nil {
						peerErrors <- err
						return
					}
				}
				peerErrors <- scanner.Err()
			}()
			agents := json.RawMessage(`{"reviewer":{"description":"review","prompt":"inspect"}}`)
			session, err := OpenStreams(ctx, SessionOptions{
				WorkDir: cwd, ClaimSession: true, AppendSystemPrompt: "extra instructions",
				SDKMCPServers: []string{"fixture"}, Initialize: &types.InitializeOptions{Agents: agents},
			}, StreamTransport{Reader: reader, Writer: writer})
			if test.change == nil {
				require.NoError(t, err)
				id, known := session.SessionID()
				assert.True(t, known)
				assert.Equal(t, "native-claimed-id", id)
				require.NoError(t, session.Close())
			} else {
				require.ErrorIs(t, err, ErrProtocol)
				require.Nil(t, session)
			}
			select {
			case request := <-requests:
				assert.Equal(t, cwd, request.CWD)
				assert.Equal(t, "extra instructions", request.AppendSystemPrompt)
				assert.JSONEq(t, string(agents), string(request.Agents))
				assert.Equal(t, []string{"fixture"}, request.SDKMCPServers)
			case <-ctx.Done():
				t.Fatal("claim request did not arrive")
			}
			select {
			case err := <-peerErrors:
				assert.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("claim peer did not stop")
			}
		})
	}
}

func TestClaimNormalizesRelativeWorkingDirectory(t *testing.T) {
	options := Options{ClaimSession: true, WorkDir: "."}
	require.NoError(t, options.defaults())
	assert.True(t, filepath.IsAbs(options.WorkDir))
}

func TestClaimStartupFrameBufferIsBounded(t *testing.T) {
	client := newInteraction(Options{ClaimSession: true}, nil)
	require.NoError(t, client.bufferClaimedFrame(Event{Raw: make(json.RawMessage, maxClaimBufferedBytes)}))
	assert.ErrorIs(t, client.bufferClaimedFrame(Event{Raw: json.RawMessage(`{}`)}), ErrResourceLimit)
}

func TestClaimCannotBindTerminatedSession(t *testing.T) {
	options := Options{ClaimSession: true}
	require.NoError(t, options.defaults())
	session, err := newSession(options)
	require.NoError(t, err)
	session.finish(ErrUnexpectedEOF)
	assert.ErrorIs(t, session.confirmClaimedRoot("native-id"), ErrClosed)
}

func TestClaimProcessesStartupMCPBeforeBindingCompletes(t *testing.T) {
	options := fixtureOptions(t, "claim_mcp")
	options.ClaimSession = true
	options.SDKMCPServers = []string{"fixture"}
	calls := make(chan error, 1)
	options.HandleSDKMCP = func(ctx context.Context, request IncomingRequest) error {
		err := request.ReplyMCP(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
		calls <- err
		return err
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := Open(ctx, SessionOptions(options))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	select {
	case err := <-calls:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("startup MCP handler did not complete")
	}
	id, known := session.SessionID()
	assert.True(t, known)
	assert.Equal(t, "claimed-native", id)
}
