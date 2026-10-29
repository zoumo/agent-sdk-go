package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
)

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
		c, err = codex.NewStdio(deadline(t), stdio)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		opts.Client = c
	}
	s, err := codex.Open(deadline(t), opts)
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
	require.NoError(t, c.Call(deadline(t), "fixture/emit", frames, nil))
}
func drainChildDetail(t *testing.T, s *codex.Session) []codex.Received {
	t.Helper()
	require.NoError(t, s.Close())
	var values []codex.Received
	for {
		r, err := s.Receive(deadline(t))
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		values = append(values, r)
	}
	return values
}
func (s *NativeIntegrationSuite) TestOptionalChildDetail() {
	s.Run("codex", func() {
		s.Run("F1-default-options", func() { s.checkCodexChildDetailOptions() })
		s.Run("F2-pure-detail", func() { s.checkCodexPureChildDetail() })
		s.Run("F3-unknown-and-mixed", func() { s.checkCodexMixedChildDetail() })
		s.Run("F4-authority", func() { s.checkCodexChildDetailAuthority() })
		s.Run("F5-lifecycle", func() { s.checkCodexChildDetailLifecycle() })
	})
	s.Run("claude", func() {
		s.Run("F1-default-options", func() { checkClaudeChildDetailOptions(s) })
		s.Run("F2-pure-detail", func() { checkClaudePureDetail(s) })
		s.Run("F3-unknown-and-mixed", func() { checkClaudeMixedDetail(s) })
		s.Run("F4-authority", func() { checkClaudeDetailAuthority(s) })
		s.Run("F5-lifecycle", func() { checkClaudeDetailLifecycle(s) })
	})
}
