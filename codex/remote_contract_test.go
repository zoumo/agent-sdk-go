package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func remoteVariant(t *testing.T) string {
	for _, part := range strings.Split(t.Name(), "/") {
		if part == "ws" || part == "wss" {
			return part
		}
	}
	return ""
}

// The same original scripted app-server peer backs all transports. This fixture
// bridges its JSONL messages to actual WebSocket text messages, independently of
// SDK internals. Only the fixture owns the helper process; NewRemote never does.
// Process ownership and pipe-specific backpressure have separate transport tests.
func openRemoteFake(t *testing.T, mode string, limits codex.Limits) (fixture, error) {
	reportPeerError := collectTransportPeerErrors(t)

	t.Helper()
	dir := t.TempDir()
	f := fixture{trace: filepath.Join(dir, "trace.jsonl")}
	pid := filepath.Join(dir, "fixture-owned-pid")
	// Trace exists before requests; the child replaces it before initialize.
	{
		err := os.WriteFile(f.trace, nil, 0600)
		require.NoError(t, err)
	}
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(exe, "-test.run=^TestFakeChild$")
	cmd.Env = append(os.Environ(), "CODEX_SDK_HELPER="+mode, "CODEX_SDK_TRACE="+f.trace, "CODEX_SDK_PID="+pid)
	inputR, input, err := os.Pipe()
	require.NoError(t, err)
	output, outputW, err := os.Pipe()
	if err != nil {
		inputR.Close()
		input.Close()
		require.FailNow(t, fmt.Sprint(err))
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inputR, outputW, io.Discard
	if err = cmd.Start(); err != nil {
		inputR.Close()
		input.Close()
		output.Close()
		outputW.Close()
		require.FailNow(t, fmt.Sprint(err))
	}
	inputR.Close()
	outputW.Close()
	ended := make(chan struct{})
	var conn *websocket.Conn
	var connMu sync.Mutex
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-contract-token" || r.Header.Get("Origin") != "" || r.URL.RawQuery != "" {
			reportPeerError("remote authentication/framing contract violated")
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		conn = ws
		connMu.Unlock()
		defer ws.CloseNow()
		defer input.Close()
		ws.SetReadLimit(32 << 20)
		outbound := make(chan struct{})
		go func() {
			defer close(outbound)
			scan := bufio.NewScanner(output)
			scan.Buffer(make([]byte, 4096), 32<<20)
			for scan.Scan() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := ws.Write(ctx, websocket.MessageText, append([]byte(nil), scan.Bytes()...))
				cancel()
				if err != nil {
					break
				}
			}
			_ = ws.CloseNow()
		}()
		for {
			kind, raw, err := ws.Read(context.Background())
			if err != nil {
				break
			}
			var envelope map[string]json.RawMessage
			_ = json.Unmarshal(raw, &envelope)
			_, hasVersion := envelope["jsonrpc"]
			if kind != websocket.MessageText || bytes.HasSuffix(raw, []byte{'\n'}) || hasVersion {
				reportPeerError("client must emit one native JSON text envelope without newline or jsonrpc")
				break
			}
			if _, err = input.Write(append(raw, '\n')); err != nil {
				break
			}
		}
		_ = ws.CloseNow()
		_ = input.Close()
		<-outbound
	}))
	var tlsConfig *tls.Config
	if remoteVariant(t) == "wss" {
		server.StartTLS()
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		tlsConfig = &tls.Config{RootCAs: roots}
	} else {
		server.Start()
	}
	// Explicit pipe ownership keeps final output readable even if Wait reaps
	// the helper first. Fixture process cleanup is separate from SDK Close.
	go func() { _ = cmd.Wait(); close(ended) }()
	t.Cleanup(func() {
		connMu.Lock()
		if conn != nil {
			_ = conn.CloseNow()
		}
		connMu.Unlock()
		_ = input.Close()
		_ = cmd.Process.Kill()
		<-ended
		_ = output.Close()
		server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.c, err = codex.NewRemote(ctx, codex.RemoteOptions{URL: "ws" + strings.TrimPrefix(server.URL, "http"), BearerToken: "synthetic-contract-token", TLSConfig: tlsConfig, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "contract-test", Version: "1"}}, Limits: limits})
	return f, err
}

// Every transport-independent P0 behavior uses the exact same public test body.
// Native malformed JSON, disconnection, identity, early event and answer cases
// cross real loopback sockets in both plaintext and synthetic-trust TLS variants.
func TestRemotePublicContractParity(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"TestOwnedStdioLifecycle", func(t *testing.T) { runOwnedStdioLifecycle(t, startFake(t, "normal", codex.Limits{})) }},
		{"TestIDExactness", TestIDExactness},
		{"TestCancellationAndLateResponse", func(t *testing.T) { runCancellationAndLateResponse(t, startFake(t, "normal", codex.Limits{})) }},
		{"TestRPCErrorNoReplay", func(t *testing.T) { runRPCErrorNoReplay(t, startFake(t, "normal", codex.Limits{})) }},
		{"TestConnectionLossIsUnknown", func(t *testing.T) { runConnectionLossIsUnknown(t, startFake(t, "normal", codex.Limits{})) }},
		{"TestMalformedFrames", TestMalformedFrames},
		{"TestInitializeFailures", TestInitializeFailures},
		{"TestPendingAdmissionLimit", TestPendingAdmissionLimit},
		{"TestOutgoingFrameLimit", TestOutgoingFrameLimit},
		{"TestInvalidOptionsAndClosedCall", TestInvalidOptionsAndClosedCall},
		{"TestTurnEarlyEventsKeepResponseIdentity", observationCase((*observationSuite).TestTurnEarlyEventsKeepResponseIdentity)},
		{"TestNotificationRawAndDecodeErrors", observationCase((*observationSuite).TestNotificationRawAndDecodeErrors)},
		{"TestSubscriptionOverflowIsolatedAndInspectable", observationCase((*observationSuite).TestSubscriptionOverflowIsolatedAndInspectable)},
		{"TestSlowSubscriberNeverBlocksResponse", observationCase((*observationSuite).TestSlowSubscriberNeverBlocksResponse)},
		{"TestSubscriptionOwnsPayloadAndNoReplay", observationCase((*observationSuite).TestSubscriptionOwnsPayloadAndNoReplay)},
		{"TestCancellationDoesNotInterruptTurn", observationCase((*observationSuite).TestCancellationDoesNotInterruptTurn)},
		{"TestTurnFailureNeverReplays", observationCase((*observationSuite).TestTurnFailureNeverReplays)},
		{"TestSubscriptionTerminalStatus", observationCase((*observationSuite).TestSubscriptionTerminalStatus)},
		{"TestSubscriptionConcurrentLifecycle", TestSubscriptionConcurrentLifecycle},
		{"TestSubscriptionAdmissionValidation", observationCase((*observationSuite).TestSubscriptionAdmissionValidation)},
		{"TestResumeAcrossIndependentConnections", observationCase((*observationSuite).TestResumeAcrossIndependentConnections)},
		{"TestResumeMismatchKeepsCorrelationAndNoFallback", observationCase((*observationSuite).TestResumeMismatchKeepsCorrelationAndNoFallback)},
		{"TestResumeAndUnsubscribeFailureNeverReplay", observationCase((*observationSuite).TestResumeAndUnsubscribeFailureNeverReplay)},
		{"TestResumeAndUnsubscribeCancellationEvidence", observationCase((*observationSuite).TestResumeAndUnsubscribeCancellationEvidence)},
		{"TestResumeAndUnsubscribeTimeoutAndClose", observationCase((*observationSuite).TestResumeAndUnsubscribeTimeoutAndClose)},
		{"TestUnsubscribeNativeStatusAndLocalIndependence", observationCase((*observationSuite).TestUnsubscribeNativeStatusAndLocalIndependence)},
		{"TestResumeAndUnsubscribeConcurrentClose", TestResumeAndUnsubscribeConcurrentClose},
		{"TestResumeAndUnsubscribeNotSentLimits", observationCase((*observationSuite).TestResumeAndUnsubscribeNotSentLimits)},
		{"TestResumeAndUnsubscribePendingAdmission", observationCase((*observationSuite).TestResumeAndUnsubscribePendingAdmission)},
		{"TestServerRequestIDsAndNativeReplyEnvelopes", func(t *testing.T) {
			runServerRequestIDsAndNativeReplyEnvelopes(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestServerRequestRawAndTypedCopies", func(t *testing.T) {
			runServerRequestRawAndTypedCopies(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestStalledServerRequestsDoNotBlockCallsOrEvents", TestStalledServerRequestsDoNotBlockCallsOrEvents},
		{"TestAnswerPreflightFailuresLeaveHandleUsable", TestAnswerPreflightFailuresLeaveHandleUsable},
		{"TestServerRequestLocalCancellationAndExpiration", TestServerRequestLocalCancellationAndExpiration},
		{"TestResolvedServerRequestUsesExactIDAndKeepsEvent", func(t *testing.T) {
			runResolvedServerRequestUsesExactIDAndKeepsEvent(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestServerRequestCapacityFailureIsExplicit", TestServerRequestCapacityFailureIsExplicit},
		{"TestServerRequestCloseInvalidatesAndUnblocks", func(t *testing.T) {
			runServerRequestCloseInvalidatesAndUnblocks(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestConcurrentServerRequestAnswerIsOneShot", func(t *testing.T) {
			runConcurrentServerRequestAnswerIsOneShot(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestServerRequestDuplicateActiveIDTerminates", func(t *testing.T) {
			runServerRequestDuplicateActiveIDTerminates(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestServerRequestLimitDefaultsAndValidation", TestServerRequestLimitDefaultsAndValidation},
		{"TestEverySupportedServerRequestTypedNativeRoundTrip", TestEverySupportedServerRequestTypedNativeRoundTrip},
		{"TestMalformedOrUnrelatedResolutionKeepsHandle", func(t *testing.T) {
			runMalformedOrUnrelatedResolutionKeepsHandle(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestNoncooperativeAnswerMarshalerCannotBlockReaderOrClose", TestNoncooperativeAnswerMarshalerCannotBlockReaderOrClose},
		{"TestZeroAnswerHandleFailsLocally", TestZeroAnswerHandleFailsLocally},
		{"TestConcurrentAnswerAndCancelHaveOneWinner", func(t *testing.T) {
			runConcurrentAnswerAndCancelHaveOneWinner(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestMalformedServerRequestEnvelopeTerminates", TestMalformedServerRequestEnvelopeTerminates},
		{"TestConcurrentAnswerAndNativeResolutionHaveOneOutcome", func(t *testing.T) {
			runConcurrentAnswerAndNativeResolutionHaveOneOutcome(t, startFake(t, "request-normal", codex.Limits{}))
		}},
		{"TestReusedServerRequestIDDoesNotReviveOldHandle", TestReusedServerRequestIDDoesNotReviveOldHandle},
		{"TestExpiredBufferedRequestsRemainInspectableAfterClose", TestExpiredBufferedRequestsRemainInspectableAfterClose},
		{"TestLegacyRequestResolutionUsesConversationIdentity", TestLegacyRequestResolutionUsesConversationIdentity},
		{"TestServerRequestsArrivingDuringInitializeAreRetained", TestServerRequestsArrivingDuringInitializeAreRetained},
		{"TestServerRequestOverflowDuringInitializeCleansUp", TestServerRequestOverflowDuringInitializeCleansUp},
	}
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, tc.run)
			}
		})
	}
}

// A complete public P0 lifecycle stays on one authenticated connection. A later
// explicit connection initializes independently and resumes only the native ID.
func TestRemoteNativeWorkflow(t *testing.T) {
	for _, transport := range []string{"ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{EventQueue: 16})
			ctx := context.Background()
			started, err := f.c.StartThread(ctx, types.ThreadStartParams{})
			require.NoError(t, err)
			read, err := f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: started.Thread.ID})
			require.NoError(t, err)
			require.False(t, read.Thread.ID != started.Thread.ID, "read native thread: %#v %v", read, err)
			events := subscribe(t, f.c, ctx)
			type outcome struct {
				turn types.TurnStartResponse
				err  error
			}
			start := make(chan outcome, 1)
			go func() { turn, err := f.c.StartTurn(ctx, startParams()); start <- outcome{turn, err} }()
			for range 8 {
				_ = nextEvent(t, events)
			}
			{
				err := f.c.Call(ctx, "release-start", nil, nil)
				require.NoError(t, err)
			}
			turn := <-start
			require.NoError(t, turn.err)
			{
				_, err := f.c.InterruptTurn(ctx, types.TurnInterruptParams{ThreadID: started.Thread.ID, TurnID: turn.turn.Turn.ID})
				require.NoError(t, err)
			}
			emitRequests(t, f.c, requestFrame(`"native-approval"`, "item/commandExecution/requestApproval", approvalParams))
			request := nextRequest(t, f.c)
			{
				err := request.Handle().Answer(ctx, json.RawMessage(`{"decision":"decline"}`))
				require.NoError(t, err)
			}
			responseBarrier(t, f)
			detached, err := f.c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: started.Thread.ID})
			require.NoError(t, err)
			require.False(t, detached.Status != "unsubscribed", "unsubscribe: %#v %v", detached, err)
			{
				err := f.c.Close()
				require.NoError(t, err)
			}
			frames := trace(t, f.trace)
			methods := []string{"initialize", "initialized", "thread/start", "thread/read", "turn/start", "release-start", "turn/interrupt", "request-frames", "", "echo", "thread/unsubscribe"}
			require.Len(t, frames, len(methods), "unexpected lifecycle: %#v", frames)
			for i, want := range methods {
				require.Equal(t, want, frames[i].Method, "frame %d method=%s want=%s", i, frames[i].Method, want)
			}
			next := startFake(t, "request-normal", codex.Limits{})
			resumed, err := next.c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: started.Thread.ID})
			require.NoError(t, err)
			require.False(t, resumed.Thread.ID != started.Thread.ID, "resume: %#v %v", resumed, err)
			{
				err := next.c.Close()
				require.NoError(t, err)
			}
			assertMethods(t, next, "initialize", "initialized", "thread/resume")
		})
	}
}
