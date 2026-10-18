package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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
	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
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
	t.Helper()
	dir := t.TempDir()
	f := fixture{trace: filepath.Join(dir, "trace.jsonl")}
	pid := filepath.Join(dir, "fixture-owned-pid")
	// Trace exists before requests; the child replaces it before initialize.
	if err := os.WriteFile(f.trace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestFakeChild$")
	cmd.Env = append(os.Environ(), "CODEX_SDK_HELPER="+mode, "CODEX_SDK_TRACE="+f.trace, "CODEX_SDK_PID="+pid)
	inputR, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, outputW, err := os.Pipe()
	if err != nil {
		inputR.Close()
		input.Close()
		t.Fatal(err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inputR, outputW, io.Discard
	if err = cmd.Start(); err != nil {
		inputR.Close()
		input.Close()
		output.Close()
		outputW.Close()
		t.Fatal(err)
	}
	inputR.Close()
	outputW.Close()
	ended := make(chan struct{})
	var conn *websocket.Conn
	var connMu sync.Mutex
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-contract-token" || r.Header.Get("Origin") != "" || r.URL.RawQuery != "" {
			t.Error("remote authentication/framing contract violated")
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
				t.Error("client must emit one native JSON text envelope without newline or jsonrpc")
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
		{"TestOwnedStdioLifecycle", TestOwnedStdioLifecycle},
		{"TestIDExactness", TestIDExactness},
		{"TestCancellationAndLateResponse", TestCancellationAndLateResponse},
		{"TestRPCErrorNoReplay", TestRPCErrorNoReplay},
		{"TestConnectionLossIsUnknown", TestConnectionLossIsUnknown},
		{"TestMalformedFrames", TestMalformedFrames},
		{"TestInitializeFailures", TestInitializeFailures},
		{"TestPendingAdmissionLimit", TestPendingAdmissionLimit},
		{"TestOutgoingFrameLimit", TestOutgoingFrameLimit},
		{"TestInvalidOptionsAndClosedCall", TestInvalidOptionsAndClosedCall},
		{"TestTurnEarlyEventsKeepResponseIdentity", TestTurnEarlyEventsKeepResponseIdentity},
		{"TestNotificationRawAndDecodeErrors", TestNotificationRawAndDecodeErrors},
		{"TestSubscriptionOverflowIsolatedAndInspectable", TestSubscriptionOverflowIsolatedAndInspectable},
		{"TestSlowSubscriberNeverBlocksResponse", TestSlowSubscriberNeverBlocksResponse},
		{"TestSubscriptionOwnsPayloadAndNoReplay", TestSubscriptionOwnsPayloadAndNoReplay},
		{"TestCancellationDoesNotInterruptTurn", TestCancellationDoesNotInterruptTurn},
		{"TestTurnFailureNeverReplays", TestTurnFailureNeverReplays},
		{"TestSubscriptionTerminalStatus", TestSubscriptionTerminalStatus},
		{"TestSubscriptionConcurrentLifecycle", TestSubscriptionConcurrentLifecycle},
		{"TestSubscriptionAdmissionValidation", TestSubscriptionAdmissionValidation},
		{"TestResumeAcrossIndependentConnections", TestResumeAcrossIndependentConnections},
		{"TestResumeMismatchKeepsCorrelationAndNoFallback", TestResumeMismatchKeepsCorrelationAndNoFallback},
		{"TestResumeAndUnsubscribeFailureNeverReplay", TestResumeAndUnsubscribeFailureNeverReplay},
		{"TestResumeAndUnsubscribeCancellationEvidence", TestResumeAndUnsubscribeCancellationEvidence},
		{"TestResumeAndUnsubscribeTimeoutAndClose", TestResumeAndUnsubscribeTimeoutAndClose},
		{"TestUnsubscribeNativeStatusAndLocalIndependence", TestUnsubscribeNativeStatusAndLocalIndependence},
		{"TestResumeAndUnsubscribeConcurrentClose", TestResumeAndUnsubscribeConcurrentClose},
		{"TestResumeAndUnsubscribeNotSentLimits", TestResumeAndUnsubscribeNotSentLimits},
		{"TestResumeAndUnsubscribePendingAdmission", TestResumeAndUnsubscribePendingAdmission},
		{"TestServerRequestIDsAndNativeReplyEnvelopes", TestServerRequestIDsAndNativeReplyEnvelopes},
		{"TestServerRequestRawAndTypedCopies", TestServerRequestRawAndTypedCopies},
		{"TestStalledServerRequestsDoNotBlockCallsOrEvents", TestStalledServerRequestsDoNotBlockCallsOrEvents},
		{"TestAnswerPreflightFailuresLeaveHandleUsable", TestAnswerPreflightFailuresLeaveHandleUsable},
		{"TestServerRequestLocalCancellationAndExpiration", TestServerRequestLocalCancellationAndExpiration},
		{"TestResolvedServerRequestUsesExactIDAndKeepsEvent", TestResolvedServerRequestUsesExactIDAndKeepsEvent},
		{"TestServerRequestCapacityFailureIsExplicit", TestServerRequestCapacityFailureIsExplicit},
		{"TestServerRequestCloseInvalidatesAndUnblocks", TestServerRequestCloseInvalidatesAndUnblocks},
		{"TestConcurrentServerRequestAnswerIsOneShot", TestConcurrentServerRequestAnswerIsOneShot},
		{"TestServerRequestDuplicateActiveIDTerminates", TestServerRequestDuplicateActiveIDTerminates},
		{"TestServerRequestLimitDefaultsAndValidation", TestServerRequestLimitDefaultsAndValidation},
		{"TestEverySupportedServerRequestTypedNativeRoundTrip", TestEverySupportedServerRequestTypedNativeRoundTrip},
		{"TestMalformedOrUnrelatedResolutionKeepsHandle", TestMalformedOrUnrelatedResolutionKeepsHandle},
		{"TestNoncooperativeAnswerMarshalerCannotBlockReaderOrClose", TestNoncooperativeAnswerMarshalerCannotBlockReaderOrClose},
		{"TestZeroAnswerHandleFailsLocally", TestZeroAnswerHandleFailsLocally},
		{"TestConcurrentAnswerAndCancelHaveOneWinner", TestConcurrentAnswerAndCancelHaveOneWinner},
		{"TestMalformedServerRequestEnvelopeTerminates", TestMalformedServerRequestEnvelopeTerminates},
		{"TestConcurrentAnswerAndNativeResolutionHaveOneOutcome", TestConcurrentAnswerAndNativeResolutionHaveOneOutcome},
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
			if err != nil {
				t.Fatal(err)
			}
			read, err := f.c.ReadThread(ctx, types.ThreadReadParams{ThreadID: started.Thread.ID})
			if err != nil || read.Thread.ID != started.Thread.ID {
				t.Fatalf("read native thread: %#v %v", read, err)
			}
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
			if err := f.c.Call(ctx, "release-start", nil, nil); err != nil {
				t.Fatal(err)
			}
			turn := <-start
			if turn.err != nil {
				t.Fatal(turn.err)
			}
			if _, err := f.c.InterruptTurn(ctx, types.TurnInterruptParams{ThreadID: started.Thread.ID, TurnID: turn.turn.Turn.ID}); err != nil {
				t.Fatal(err)
			}
			emitRequests(t, f.c, requestFrame(`"native-approval"`, "item/commandExecution/requestApproval", approvalParams))
			request := nextRequest(t, f.c)
			if err := request.Handle().Answer(ctx, json.RawMessage(`{"decision":"decline"}`)); err != nil {
				t.Fatal(err)
			}
			responseBarrier(t, f)
			detached, err := f.c.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: started.Thread.ID})
			if err != nil || detached.Status != "unsubscribed" {
				t.Fatalf("unsubscribe: %#v %v", detached, err)
			}
			if err := f.c.Close(); err != nil {
				t.Fatal(err)
			}
			frames := trace(t, f.trace)
			methods := []string{"initialize", "initialized", "thread/start", "thread/read", "turn/start", "release-start", "turn/interrupt", "request-frames", "", "echo", "thread/unsubscribe"}
			if len(frames) != len(methods) {
				t.Fatalf("unexpected lifecycle: %#v", frames)
			}
			for i, want := range methods {
				if frames[i].Method != want {
					t.Fatalf("frame %d method=%s want=%s", i, frames[i].Method, want)
				}
			}
			next := startFake(t, "request-normal", codex.Limits{})
			resumed, err := next.c.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: started.Thread.ID})
			if err != nil || resumed.Thread.ID != started.Thread.ID {
				t.Fatalf("resume: %#v %v", resumed, err)
			}
			if err := next.c.Close(); err != nil {
				t.Fatal(err)
			}
			assertMethods(t, next, "initialize", "initialized", "thread/resume")
		})
	}
}
