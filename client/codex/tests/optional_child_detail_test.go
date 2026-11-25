package codex_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
)

type codexChildDetailSuite struct{ suite.Suite }

func TestOptionalChildDetail(t *testing.T) { suite.Run(t, new(codexChildDetailSuite)) }

// This peer only emits caller-supplied synthetic frames; trace retains exact
// source bytes, independently of the SDK's delivery selection.
func childDetailPeer() error {
	trace, err := os.Create(os.Getenv("SDK_INTEGRATION_TRACE"))
	if err != nil {
		return err
	}
	defer trace.Close()
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 16<<20)
	starts := 0
	for scan.Scan() {
		var f struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if err := json.Unmarshal(scan.Bytes(), &f); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(trace, scan.Text()); err != nil {
			return err
		}
		reply := func(v string) { fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", f.ID, v) }
		switch f.Method {
		case "initialize":
			reply(`{"userAgent":"synthetic","codexHome":"/synthetic-home","platformFamily":"unix","platformOs":"linux"}`)
		case "initialized":
		case "thread/start":
			starts++
			reply(fmt.Sprintf(`{"thread":{"id":"root-%d","cwd":"/synthetic","sessionId":"native-session","projectId":null,"cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"ephemeral":false,"modelProvider":"synthetic","preview":"","source":"appServer","status":{"type":"idle"},"turns":[]},"model":"creation-model","modelProvider":"synthetic","cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"readOnly"}}`, starts))
		case "fixture/emit":
			var frames []string
			if err := json.Unmarshal(f.Params, &frames); err != nil {
				return err
			}
			for _, raw := range frames {
				fmt.Fprintln(os.Stdout, raw)
				fmt.Fprintln(trace, "source:"+raw)
			}
			reply(`{}`)
		case "thread/unsubscribe":
			reply(`{"status":"unsubscribed"}`)
		case "turn/start":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			if err := json.Unmarshal(f.Params, &p); err != nil {
				return err
			}
			for _, raw := range append([]string{detailRelation("child", p.ThreadID)}, pureDetails("child")...) {
				fmt.Fprintln(os.Stdout, raw)
				fmt.Fprintln(trace, "source:"+raw)
			}
			fmt.Fprintln(os.Stdout, detailFrame("fixture/turn-barrier", fmt.Sprintf(`{"threadId":%q}`, p.ThreadID)))
			reply(`{"turn":{"id":"next","items":[],"status":"inProgress"}}`)
		case "turn/interrupt":
			reply(`{}`)
		default:
			if len(f.ID) > 0 && f.Method != "" {
				return fmt.Errorf("unexpected fixture RPC %s", f.Method)
			}
		}
	}
	return scan.Err()
}

func openChildDetail(t *testing.T, policy codex.ChildDetailPolicy, owned bool) (*codex.Session, *codex.Client, string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "trace")
	stdio := codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_INTEGRATION_PEER=codex", "SDK_INTEGRATION=child-detail", "SDK_INTEGRATION_TRACE=" + path, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "child-detail", Version: "synthetic"}}}
	opts := codex.SessionOptions{ChildDetailPolicy: policy}
	var c *codex.Client
	if owned {
		opts.Stdio = &stdio
	} else {
		c, err = codex.NewStdio(testsupport.Deadline(t), stdio)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		opts.Client = c
	}
	s, err := codex.Open(testsupport.Deadline(t), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Abort() })
	return s, c, path
}

func detailFrame(method, params string) string {
	return fmt.Sprintf(`{"method":%q,"params":%s}`, method, params)
}

func detailRelation(child, parent string) string {
	return detailFrame("thread/started", fmt.Sprintf(`{"thread":{"id":%q,"parentThreadId":%q}}`, child, parent))
}

func pureDetails(id string) []string {
	base := fmt.Sprintf(`"threadId":%q,"turnId":"turn","itemId":"item"`, id)
	return []string{
		detailFrame("item/agentMessage/delta", `{`+base+`,"delta":"text"}`),
		detailFrame("item/reasoning/textDelta", `{`+base+`,"delta":"thought","contentIndex":0}`),
		detailFrame("item/reasoning/summaryTextDelta", `{`+base+`,"delta":"summary","summaryIndex":0}`),
		detailFrame("item/reasoning/summaryPartAdded", `{`+base+`,"summaryIndex":0}`),
		detailFrame("item/commandExecution/outputDelta", `{`+base+`,"delta":"output"}`),
		detailFrame("item/fileChange/outputDelta", `{`+base+`,"delta":"patch"}`),
		detailFrame("item/mcpToolCall/progress", `{`+base+`,"message":"progress"}`),
	}
}

func emitChildDetail(t *testing.T, c *codex.Client, frames []string) {
	t.Helper()
	require.NoError(t, c.Call(testsupport.Deadline(t), "fixture/emit", frames, nil))
}

func drainChildDetail(t *testing.T, s *codex.Session) []codex.Received {
	t.Helper()
	require.NoError(t, s.Close())
	var values []codex.Received
	for {
		r, err := s.Receive(testsupport.Deadline(t))
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		values = append(values, r)
	}
	return values
}

func (s *codexChildDetailSuite) TestDeliveryPolicy() {
	s.Run("F1-default-options", func() { s.checkCodexChildDetailOptions() })
	s.Run("F2-pure-detail", func() { s.checkCodexPureChildDetail() })
	s.Run("F3-unknown-and-mixed", func() { s.checkCodexMixedChildDetail() })
	s.Run("F4-authority", func() { s.checkCodexChildDetailAuthority() })
	s.Run("F5-lifecycle", func() { s.checkCodexChildDetailLifecycle() })
}

func (s *codexChildDetailSuite) checkCodexPureChildDetail() {
	for _, policy := range []codex.ChildDetailPolicy{"", codex.ChildDetailPreserve, codex.ChildDetailSuppress} {
		s.Run(string(policy), func() {
			t := s.T()
			session, client, path := openChildDetail(t, policy, false)
			frames := []string{detailRelation("child", "root-1")}
			frames = append(frames, pureDetails("child")...)
			frames = append(frames, pureDetails("root-1")...)
			frames = append(frames, pureDetails("root-1")[0]) // identical native facts are not deduplicated
			emitChildDetail(t, client, frames)
			got := drainChildDetail(t, session)
			var raws []string
			for _, r := range got {
				if r.Event == nil {
					continue
				}
				raws = append(raws, string(r.Raw))
				assert.Equal(t, r.Raw, r.Event.Raw)
				assert.Equal(t, r.Params, r.Event.Params)
				assert.Equal(t, "live", r.Origin)
				var source struct {
					ThreadID string `json:"threadId"`
				}
				require.NoError(t, json.Unmarshal(r.Params, &source))
				if source.ThreadID != "" {
					assert.Equal(t, source.ThreadID, r.Source.ThreadID)
				}
			}
			want := frames
			if policy == codex.ChildDetailSuppress {
				want = append([]string{frames[0]}, frames[8:]...)
			}
			assert.Equal(t, want, raws)
			wire, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, raw := range frames {
				assert.Contains(t, string(wire), "source:"+raw+"\n")
			}
			assert.NotContains(t, string(wire), "childDetailPolicy")
		})
	}
}

func (s *codexChildDetailSuite) checkCodexChildDetailOptions() {
	for _, owned := range []bool{false, true} {
		for _, policy := range []codex.ChildDetailPolicy{"", codex.ChildDetailPreserve, codex.ChildDetailSuppress} {
			s.Run(fmt.Sprintf("owned=%v/%s", owned, policy), func() {
				t := s.T()
				session, _, path := openChildDetail(t, policy, owned)
				for range 2 {
					_, err := session.Submit(testsupport.Deadline(t), codex.Input{})
					require.NoError(t, err)
				}
				got := drainChildDetail(t, session)
				details, barriers := 0, 0
				for _, r := range got {
					if r.Event != nil {
						if r.Event.Method == "fixture/turn-barrier" {
							barriers++
						}
						if strings.HasPrefix(r.Event.Method, "item/") {
							details++
						}
					}
				}
				assert.Equal(t, 2, barriers)
				want := 14
				if policy == codex.ChildDetailSuppress {
					want = 0
				}
				assert.Equal(t, want, details)
				wire, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.NotContains(t, string(wire), "childDetailPolicy")
			})
		}
	}
	t := s.T()
	first, client, _ := openChildDetail(t, codex.ChildDetailSuppress, false)
	opts := codex.SessionOptions{Client: client, ChildDetailPolicy: codex.ChildDetailPreserve}
	second, err := codex.Open(testsupport.Deadline(t), opts)
	require.NoError(t, err)
	defer second.Abort()
	opts.ChildDetailPolicy = codex.ChildDetailSuppress
	frames := []string{detailRelation("shared-child", first.ThreadID()), detailRelation("shared-child", second.ThreadID()), pureDetails("shared-child")[0]}
	emitChildDetail(t, client, frames)
	a, b := drainChildDetail(t, first), drainChildDetail(t, second)
	count := func(values []codex.Received) int {
		n := 0
		for _, r := range values {
			if r.Event != nil && r.Event.Method == "item/agentMessage/delta" {
				n++
			}
		}
		return n
	}
	assert.Zero(t, count(a))
	assert.Equal(t, 1, count(b), "one Session's fixed policy require.NoError not affect a shared observer")
}

func (s *codexChildDetailSuite) checkCodexMixedChildDetail() {
	t := s.T()
	session, client, _ := openChildDetail(t, codex.ChildDetailSuppress, false)
	// Early and foreign records stay diagnostic, including after late confirmation.
	early := pureDetails("child")[0]
	foreign := pureDetails("foreign")[0]
	frames := []string{early, foreign, detailRelation("child", "root-1"), detailRelation("nested", "child"), detailRelation("sibling", "root-1")}
	want := append([]string(nil), frames[2:]...)
	for _, id := range []string{"child", "nested", "sibling"} {
		frames = append(frames, pureDetails(id)...)
	}
	for _, pure := range pureDetails("child") {
		// Unknown or essential semantics added at either envelope or params level.
		for _, suffix := range []string{`,"future":null`, `,"parentThreadId":"root-1"`, `,"status":"failed"`, `,"error":{"code":1}`} {
			mixed := strings.TrimSuffix(pure, "}}") + suffix + "}}"
			frames = append(frames, mixed)
			want = append(want, mixed)
		}
		extra := strings.TrimSuffix(pure, "}") + `,"futureEnvelope":null}`
		frames = append(frames, extra)
		want = append(want, extra)
		// A fixed schema requires non-null strings, required fields and exact int64 indices.
		for _, bad := range []string{
			strings.Replace(pure, `"itemId":"item",`, "", 1),
			strings.Replace(pure, `"itemId":"item"`, `"future":null`, 1),
			strings.Replace(pure, `"itemId":"item"`, `"itemId":null`, 1),
			strings.Replace(pure, `"itemId":"item"`, `"itemId":[]`, 1),
		} {
			frames = append(frames, bad)
			want = append(want, bad)
		}
	}
	extra := []string{
		`{ "method" : "item/agentMessage/delta", "params" : {"threadId":"child","turnId":"turn","itemId":"item","delta":"text","future":[null,9007199254740993]} }`,
		detailFrame("item/reasoning/textDelta", `{"threadId":"child","turnId":"turn","itemId":"item","delta":"thought","contentIndex":null}`),
		detailFrame("item/reasoning/summaryTextDelta", `{"threadId":"child","turnId":"turn","itemId":"item","delta":"summary","summaryIndex":9223372036854775808}`),
		detailFrame("item/reasoning/summaryPartAdded", `{"threadId":"child","turnId":"turn","itemId":"item","summaryIndex":0.5}`),
		detailFrame("item/agentMessage/delta", `{"threadId":"child","turnId":"turn","itemId":"item","delta":null}`),
		detailFrame("item/future/delta", `{"threadId":"child","delta":"future"}`),
		detailFrame("item/completed", `{"threadId":"child","turnId":"turn","completedAtMs":1,"item":{"type":"futureItem","id":"item","text":"mixed","future":null}}`),
		detailFrame("item/started", `{"threadId":"child","turnId":"turn","startedAtMs":1,"item":{"type":"agentMessage","id":"item","text":"visible full item"}}`),
	}
	frames = append(frames, extra...)
	want = append(want, extra...)
	duplicate := strings.Replace(pureDetails("child")[0], `"itemId":"item"`, `"itemId":"item","itemId":"duplicate"`, 1)
	frames = append(frames, duplicate)
	emitChildDetail(t, client, frames)
	got := drainChildDetail(t, session)
	var raws []string
	for _, r := range got {
		if r.Event != nil {
			raws = append(raws, string(r.Raw))
			assert.Equal(t, r.Raw, r.Event.Raw)
		}
	}
	assert.Equal(t, want, raws)
	q := session.Unattributed()
	require.Len(t, q.Records, 3)
	assert.Equal(t, duplicate, string(q.Records[2].Raw))
	assert.Equal(t, early, string(q.Records[0].Raw))
	assert.Equal(t, foreign, string(q.Records[1].Raw))
	assert.True(t, q.CoverageGap)
}

func (s *codexChildDetailSuite) checkCodexChildDetailAuthority() {
	for _, action := range []string{"approve", "deny", "answer", "resolved", "close"} {
		s.Run(action, func() {
			t := s.T()
			session, client, path := openChildDetail(t, codex.ChildDetailSuppress, false)
			method := "item/commandExecution/requestApproval"
			params := `{ "threadId":"child","turnId":"turn","itemId":"item","command":"synthetic only","cwd":"/synthetic","future":null }`
			if action == "answer" {
				method = "item/tool/requestUserInput"
				params = `{"threadId":"child","turnId":"turn","itemId":"item","isBlocking":false,"questions":[{"id":"q","header":"H","question":"Choose","isOther":false,"isSecret":false,"options":[]}]}`
			}
			request := fmt.Sprintf(`{"id":"original","method":%q,"params":%s,"futureEnvelope":null}`, method, params)
			// Backlogged detail does not block the control path or change the original request.
			frames := []string{detailRelation("child", "root-1")}
			for range 256 {
				frames = append(frames, pureDetails("child")...)
			}
			frames = append(frames, request)
			emitChildDetail(t, client, frames)
			var incoming *codex.IncomingRequest
			for incoming == nil {
				r, err := session.Receive(testsupport.Deadline(t))
				require.NoError(t, err)
				if r.Request != nil {
					incoming = r.Request
					assert.Equal(t, request, string(r.Raw))
					assert.Equal(t, params, string(r.Params))
					assert.Equal(t, "child", r.Source.ThreadID)
				}
			}
			originalCtx := incoming.Context()
			require.NoError(t, originalCtx.Err())
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, incoming.Handle().Answer(canceled, json.RawMessage(`{}`)), context.Canceled)
			require.NoError(t, originalCtx.Err(), "a canceled answer attempt require.NoError not revoke the request")
			switch action {
			case "approve":
				require.NoError(t, incoming.Approve(testsupport.Deadline(t), codex.ApprovalOptions{}))
			case "deny":
				require.NoError(t, incoming.Deny(testsupport.Deadline(t), codex.ApprovalOptions{}))
			case "answer":
				require.NoError(t, incoming.Answer(testsupport.Deadline(t), ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{"q": {Answers: []string{"A"}}}}))
			case "resolved":
				emitChildDetail(t, client, []string{detailFrame("serverRequest/resolved", `{"threadId":"child","requestId":"original"}`)})
			case "close":
				require.NoError(t, session.Close())
			}
			require.ErrorIs(t, originalCtx.Err(), context.Canceled)
			require.Error(t, incoming.Handle().Answer(testsupport.Deadline(t), json.RawMessage(`{}`)), "original capability is single use")
			// A client barrier makes the peer's recorded answer count deterministic.
			emitChildDetail(t, client, nil)
			wire, err := os.ReadFile(path)
			require.NoError(t, err)
			answers := 0
			for line := range strings.SplitSeq(string(wire), "\n") {
				if strings.HasPrefix(line, "source:") {
					continue
				}
				var f map[string]json.RawMessage
				if json.Unmarshal([]byte(line), &f) == nil && string(f["id"]) == `"original"` {
					if _, ok := f["result"]; ok {
						answers++
					}
				}
			}
			want := 1
			if action == "resolved" || action == "close" {
				want = 0
			}
			assert.Equal(t, want, answers)
		})
	}
}

func (s *codexChildDetailSuite) checkCodexChildDetailLifecycle() {
	for _, stop := range []string{"close", "abort"} {
		s.Run(stop, func() {
			t := s.T()
			session, client, _ := openChildDetail(t, codex.ChildDetailSuppress, false)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := session.Receive(canceled)
			require.ErrorIs(t, err, context.Canceled)
			parentResult := detailFrame("item/completed", `{"threadId":"root-1","turnId":"turn","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","status":"completed","senderThreadId":"root-1","receiverThreadIds":["child"],"agentsStates":{}}}`)
			frames := []string{parentResult}
			want := []string{parentResult}
			for range 2048 {
				frames = append(frames, pureDetails("child")[0])
				retained := detailFrame("fixture/ordered", fmt.Sprintf(`{"threadId":"child","ordinal":%d}`, len(want)))
				frames = append(frames, retained)
				want = append(want, retained)
			}
			terminal := []string{
				detailFrame("item/completed", `{"threadId":"root-1","turnId":"turn","completedAtMs":2,"item":{"type":"collabAgentToolCall","id":"testsupport.Wait","tool":"testsupport.Wait","status":"completed","senderThreadId":"root-1","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"completed","message":"parent tool result"}}}}`),
				detailFrame("turn/completed", `{"threadId":"root-1","turn":{"id":"turn","items":[],"status":"completed","error":null}}`),
				detailFrame("item/completed", `{"threadId":"child","turnId":"turn","completedAtMs":3,"item":{"type":"agentMessage","id":"item","text":"full child item remains"}}`),
				detailFrame("error", `{"threadId":"child","turnId":"turn","willRetry":false,"error":{"message":"synthetic failure","codexErrorInfo":{}}}`),
				detailFrame("turn/completed", `{"threadId":"child","turn":{"id":"turn","items":[],"status":"failed","error":{"message":"synthetic failure","codexErrorInfo":{}}}}`),
				detailFrame("turn/completed", `{"threadId":"child","turn":{"id":"next","items":[],"status":"interrupted","error":null}}`),
			}
			frames = append(frames, terminal...)
			frames = append(frames, pureDetails("child")...)
			want = append(want, terminal...)
			emitChildDetail(t, client, frames) // reply arrives with no Receive consumer
			select {
			case <-session.Done():
				require.FailNow(t, "root terminal ended child observation")
			default:
			}
			require.NoError(t, session.Interrupt(testsupport.Deadline(t), codex.InterruptOptions{TurnID: "turn"}))
			if stop == "abort" {
				require.NoError(t, session.Abort())
				_, err := session.Receive(testsupport.Deadline(t))
				require.ErrorIs(t, err, io.EOF)
				return
			}
			got := drainChildDetail(t, session)
			var raws []string
			members, parents := 0, 0
			for _, r := range got {
				if r.Event != nil {
					raws = append(raws, string(r.Raw))
				}
				if r.Observation != nil {
					if r.Observation.Reason == "member-confirmed" {
						members++
					}
					if r.Observation.Reason == "parent-confirmed" {
						parents++
					}
				}
			}
			assert.Equal(t, want, raws)
			assert.Equal(t, 1, members)
			assert.Equal(t, 1, parents)
		})
	}
}
