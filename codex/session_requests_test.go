package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func sessionRequest(t *testing.T, s *codex.Session) *codex.IncomingRequest {
	t.Helper()
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, codex.ReceivedRequest, got.Kind)
	require.NotNil(t, got.Request)
	require.Equal(t, got.Request.Raw(), got.Raw)
	return got.Request
}

func TestSessionRequestFIFOAndOriginalRaw(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{RequestQueue: 1})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	raw := `{"id":"future","method":"future/approval","params":{"threadId":"native-thread-42","n":9007199254740993},"future":{"nullable":null}}`
	emitRequests(t, f.c, `{"method":"future/before","params":{"threadId":"native-thread-42"}}`, raw,
		`{"method":"future/after","params":{"threadId":"native-thread-42"}}`)
	before, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/before", before.Event.Method)
	r := sessionRequest(t, s)
	require.Equal(t, raw, string(r.Raw()))
	copy := r.Raw()
	copy[0] = 'x'
	require.Equal(t, raw, string(r.Raw()))
	require.NoError(t, r.Context().Err())
	after, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/after", after.Event.Method)
	select {
	case <-f.c.Requests():
		t.Fatal("Session request also entered legacy queue")
	default:
	}
	require.NoError(t, r.Handle().Answer(context.Background(), json.RawMessage(`{"future":true}`)))
	require.ErrorIs(t, r.Context().Err(), context.Canceled)
	responseBarrier(t, f)
	require.Len(t, answerFrames(t, f.trace), 1)
}

func TestSessionNativeApprovalAndQuestionAnswers(t *testing.T) {
	cases := []struct {
		name, method, params, result string
		deny                         bool
	}{
		{"command-scope", "item/commandExecution/requestApproval", approvalParams, `{"decision":"acceptForSession","future":null}`, false},
		{"command-policy", "item/commandExecution/requestApproval", approvalParams, `{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}},"future":true}`, false},
		{"network-deny", "item/commandExecution/requestApproval", approvalParams, `{"decision":{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"example.test","action":"deny"}}}}`, true},
		{"file-deny", "item/fileChange/requestApproval", approvalParams, `{"decision":"decline","future":null}`, true},
		{"permissions-scope", "item/permissions/requestApproval", `{"threadId":"native-thread-42"}`, `{"permissions":{"network":{"enabled":true}},"scope":"session","strictAutoReview":false,"future":null}`, false},
		{"permissions-deny", "item/permissions/requestApproval", `{"threadId":"native-thread-42"}`, `{"permissions":{},"scope":"turn"}`, true},
		{"legacy-exec", "execCommandApproval", `{"conversationId":"native-thread-42"}`, `{"decision":{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":["git"]}}}`, false},
		{"legacy-patch", "applyPatchApproval", `{"conversationId":"native-thread-42"}`, `{"decision":{"denied":{"rejection":"user declined"}}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			emitRequests(t, f.c, requestFrame(`"approval"`, tc.method, tc.params))
			r := sessionRequest(t, s)
			opts := codex.ApprovalOptions{}
			switch tc.method {
			case "item/commandExecution/requestApproval":
				opts.CommandExecution = new(types.CommandExecutionRequestApprovalResponse)
				require.NoError(t, json.Unmarshal([]byte(tc.result), opts.CommandExecution))
			case "item/fileChange/requestApproval":
				opts.FileChange = new(types.FileChangeRequestApprovalResponse)
				require.NoError(t, json.Unmarshal([]byte(tc.result), opts.FileChange))
			case "item/permissions/requestApproval":
				opts.Permissions = new(types.PermissionsRequestApprovalResponse)
				require.NoError(t, json.Unmarshal([]byte(tc.result), opts.Permissions))
			case "execCommandApproval":
				opts.ExecCommand = new(types.ExecCommandApprovalResponse)
				require.NoError(t, json.Unmarshal([]byte(tc.result), opts.ExecCommand))
			case "applyPatchApproval":
				opts.ApplyPatch = new(types.ApplyPatchApprovalResponse)
				require.NoError(t, json.Unmarshal([]byte(tc.result), opts.ApplyPatch))
			}
			require.ErrorIs(t, r.Answer(context.Background(), types.ToolRequestUserInputResponse{}), codex.ErrInvalidInput)
			if tc.deny {
				require.NoError(t, r.Deny(context.Background(), opts))
			} else {
				require.NoError(t, r.Approve(context.Background(), opts))
			}
			responseBarrier(t, f)
			frames := answerFrames(t, f.trace)
			require.Len(t, frames, 1)
			require.JSONEq(t, tc.result, string(frames[0]["result"]))
			require.NotContains(t, frames[0], "error")
		})
	}
	t.Run("question", func(t *testing.T) {
		f := startFake(t, "request-normal", codex.Limits{})
		s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		emitRequests(t, f.c, requestFrame(`"question"`, "item/tool/requestUserInput", `{"threadId":"native-thread-42","turnId":"t","itemId":"i","questions":[{"id":"q-1","header":"h","question":"Which?","isOther":false,"isSecret":false,"options":[]}]}`))
		r := sessionRequest(t, s)
		require.ErrorIs(t, r.Deny(context.Background(), codex.ApprovalOptions{}), codex.ErrInvalidInput)
		var answer types.ToolRequestUserInputResponse
		const result = `{"answers":{"q-1":{"answers":["one","two"],"future":null}},"future":true}`
		require.NoError(t, json.Unmarshal([]byte(result), &answer))
		require.NoError(t, r.Answer(context.Background(), answer))
		responseBarrier(t, f)
		frames := answerFrames(t, f.trace)
		require.Len(t, frames, 1)
		require.JSONEq(t, result, string(frames[0]["result"]))
	})
}

func TestSessionChildRequestAuthorityDuplicateAndClose(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{RequestQueue: 1})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	// The observation alone has no request capability.
	emitRequests(t, f.c, `{"method":"thread/started","params":{"thread":{"id":"child","parentThreadId":"native-thread-42"}}}`)
	for range 3 {
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Nil(t, got.Request)
	}
	raw := requestFrame(`"child-request"`, "item/commandExecution/requestApproval", `{"threadId":"child"}`)
	emitRequests(t, f.c, raw, raw)
	first := sessionRequest(t, s)
	duplicate := sessionRequest(t, s)
	require.Equal(t, first.Context(), duplicate.Context())
	require.NoError(t, first.Approve(context.Background(), codex.ApprovalOptions{}))
	require.ErrorIs(t, duplicate.Deny(context.Background(), codex.ApprovalOptions{}), codex.ErrRequestAnswered)
	emitRequests(t, f.c, raw)
	late := sessionRequest(t, s)
	require.Equal(t, first.Context(), late.Context())
	require.ErrorIs(t, late.Approve(context.Background(), codex.ApprovalOptions{}), codex.ErrRequestAnswered)
	emitRequests(t, f.c, requestFrame(`"closing"`, "item/commandExecution/requestApproval", `{"threadId":"child"}`))
	closing := sessionRequest(t, s)
	require.NoError(t, s.Close())
	require.ErrorIs(t, closing.Context().Err(), context.Canceled)
	require.ErrorIs(t, closing.Approve(context.Background(), codex.ApprovalOptions{}), codex.ErrClosed)
	responseBarrier(t, f)
	require.Len(t, answerFrames(t, f.trace), 1)
}

func TestSessionUnknownRequestsRetainRegistryBeyondDiagnosticRetention(t *testing.T) {
	f := startFake(t, "request-normal", codex.Limits{ActiveRequests: 2, RequestQueue: 1, QuarantineRecords: 1, QuarantineRetention: 20 * time.Millisecond, RequestTimeout: time.Millisecond})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	emitRequests(t, f.c, requestFrame(`"unknown-1"`, "future/request", `{"threadId":"unowned"}`), requestFrame(`"unknown-2"`, "future/request", `{"threadId":"unowned"}`))
	require.Eventually(t, func() bool { return f.c.Unattributed().Expired == 1 }, time.Second, time.Millisecond)
	require.NoError(t, f.c.Err())
	require.Empty(t, answerFrames(t, f.trace))
	d := f.c.Unattributed()
	require.Empty(t, d.Records)
	require.EqualValues(t, 1, d.Evicted)
	select {
	case <-f.c.Requests():
		t.Fatal("unknown request escaped to legacy consumer")
	default:
	}
	// Expiring diagnostics has not released the native requests' admission slots.
	err = f.c.Call(context.Background(), "request-frames", []string{requestFrame(`"third"`, "future/request", `{"threadId":"native-thread-42"}`)}, nil)
	require.ErrorIs(t, err, codex.ErrRequestOverflow)
	require.ErrorIs(t, s.Err(), codex.ErrRequestOverflow)
}

func TestSessionAmbiguousRequestScopeDoesNotGrantAuthority(t *testing.T) {
	f := startFake(t, "session-normal", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	b, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	for _, s := range []*codex.Session{a, b} {
		got, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Nil(t, got.Request)
	}
	emitRequests(t, f.c,
		`{"method":"thread/started","params":{"thread":{"id":"overlap","parentThreadId":"main-1"}}}`,
		`{"method":"thread/started","params":{"thread":{"id":"overlap","parentThreadId":"main-2"}}}`,
		requestFrame(`"ambiguous"`, "future/request", `{"threadId":"overlap"}`),
		requestFrame(`"direct"`, "item/fileChange/requestApproval", `{"threadId":"main-2"}`))
	require.Len(t, f.c.Unattributed().Records, 1)
	require.Contains(t, string(f.c.Unattributed().Records[0].Raw), "ambiguous")
	require.NoError(t, a.Close())
	require.NoError(t, b.Close())
	for _, s := range []*codex.Session{a, b} {
		var requests []*codex.IncomingRequest
		for {
			got, err := s.Receive(context.Background())
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			if got.Request != nil {
				requests = append(requests, got.Request)
			}
		}
		if s == a {
			require.Empty(t, requests)
		} else {
			require.Len(t, requests, 1)
			require.Equal(t, codex.StringID("direct"), requests[0].ID())
			require.ErrorIs(t, requests[0].Context().Err(), context.Canceled)
		}
	}
	require.Empty(t, answerFrames(t, f.trace))
}

func TestSessionRequestPrewriteRevocationAndConcurrentAnswers(t *testing.T) {
	for _, stop := range []string{"resolved", "close", "concurrent"} {
		t.Run(stop, func(t *testing.T) {
			f := startFake(t, "request-normal", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			emitRequests(t, f.c, requestFrame(`"request"`, "item/commandExecution/requestApproval", approvalParams))
			r := sessionRequest(t, s)
			if stop == "concurrent" {
				results := make(chan error, 2)
				go func() { results <- r.Approve(context.Background(), codex.ApprovalOptions{}) }()
				go func() { results <- r.Deny(context.Background(), codex.ApprovalOptions{}) }()
				first, second := <-results, <-results
				if first == nil {
					require.ErrorIs(t, second, codex.ErrRequestAnswered)
				} else {
					require.ErrorIs(t, first, codex.ErrRequestAnswered)
					require.NoError(t, second)
				}
				responseBarrier(t, f)
				require.Len(t, answerFrames(t, f.trace), 1)
				return
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			result := make(chan error, 1)
			go func() {
				result <- r.Handle().Answer(context.Background(), heldAnswerMarshaler{entered: entered, release: release})
			}()
			<-entered
			cause := codex.ErrRequestResolved
			if stop == "close" {
				cause = codex.ErrClosed
				require.NoError(t, s.Close())
			} else {
				emitRequests(t, f.c, `{"method":"serverRequest/resolved","params":{"threadId":"native-thread-42","requestId":"request"}}`)
			}
			require.ErrorIs(t, r.Context().Err(), context.Canceled)
			once.Do(func() { close(release) })
			var ae *codex.AnswerError
			err = <-result
			require.ErrorAs(t, err, &ae)
			require.Equal(t, codex.NotSent, ae.Dispatch)
			require.ErrorIs(t, err, cause)
			require.ErrorIs(t, r.Approve(context.Background(), codex.ApprovalOptions{}), cause)
			responseBarrier(t, f)
			require.Empty(t, answerFrames(t, f.trace))
		})
	}
}

func TestSessionCloseDuringAnswerWriteKeepsOtherSession(t *testing.T) {
	f := startFake(t, "session-block-answer", codex.Limits{})
	a, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	b, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.WriteFile(f.pid+".release", nil, 0600); _ = a.Abort(); _ = b.Abort() })
	_, err = a.Receive(context.Background())
	require.NoError(t, err)
	r := sessionRequest(t, a)
	result := make(chan error, 1)
	go func() { result <- r.Handle().Answer(context.Background(), strings.Repeat("x", 256<<10)) }()
	require.Eventually(t, func() bool { _, err := os.Stat(f.pid + ".writing"); return err == nil }, time.Second, time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-result:
		var ae *codex.AnswerError
		require.ErrorAs(t, err, &ae)
		require.Equal(t, codex.OutcomeUnknown, ae.Dispatch)
		require.ErrorIs(t, err, codex.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("Session answer waiter did not wake")
	}
	require.ErrorIs(t, r.Context().Err(), context.Canceled)
	require.ErrorIs(t, r.Handle().Answer(context.Background(), "retry"), codex.ErrClosed)
	require.NoError(t, f.c.Err())
	select {
	case <-b.Done():
		t.Fatal("Close terminated unrelated Session")
	default:
	}
	require.NoError(t, os.WriteFile(f.pid+".release", nil, 0600))
	require.NoError(t, <-closed)
	_, err = b.Submit(context.Background(), codex.Input{Input: []types.UserInput{}})
	require.NoError(t, err)
	require.NoError(t, f.c.Err())
}
