package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRootPlanRejectsUnboundedOrChildInputs(t *testing.T) {
	for _, scene := range []string{"s3", "unknown", "r4"} {
		_, err := RunRoot(context.Background(), nil, RootPlan{Scene: scene}, nil)
		require.Error(t, err)
	}
}

func TestSavedQueryRequiresExplicitSyntheticScope(t *testing.T) {
	_, err := QuerySavedRoot(context.Background(), nil, "", "saved")
	require.Error(t, err)
}

func init() {
	if len(os.Args) == 3 && os.Args[1] == "--root-peer" {
		b, e := os.ReadFile(os.Args[2])
		var fs []Frame
		if e == nil {
			e = json.Unmarshal(b, &fs)
		}
		if e == nil {
			e = Peer(os.Stdin, os.Stdout, fs)
		}
		if e != nil {
			os.Exit(35)
		}
		os.Exit(0)
	}
}
func rootClient(t *testing.T, fs []Frame) (*codex.Client, context.Context) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.json")
	b, e := json.Marshal(fs)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(path, b, 0600))
	exe, e := os.Executable()
	require.NoError(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	c, e := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--root-peer", path}, Env: []string{"AGENT_SDK_TESTIFY_BIN=" + os.Getenv("AGENT_SDK_TESTIFY_BIN")}, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "synthetic-preflight", Version: "1"}, Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c, ctx
}
func rootPrefix(t *testing.T) []Frame {
	t.Helper()
	b, e := fixtures.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, e)
	fs, e := Decode(bytes.NewReader(b))
	require.NoError(t, e)
	fs = fs[:5]
	fs[0].Wire = json.RawMessage(`{"id":101,"method":"initialize","params":{"clientInfo":{"name":"synthetic-preflight","version":"1"},"capabilities":{"experimentalApi":true}}}`)
	return fs
}
func addRoot(fs *[]Frame, d, w string) { *fs = append(*fs, frame(len(*fs)+1, d, w)) }
func completeRoot(fs *[]Frame, id, turn string) {
	addRoot(fs, "s2c", `{"id":`+id+`,"result":{"turn":{"id":"`+turn+`","items":[],"status":"inProgress"}}}`)
	addRoot(fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"`+turn+`","items":[],"status":"completed","error":null}}}`)
}
func TestRootContinuationStructuredInputAndNativeReceipts(t *testing.T) {
	fs := rootPrefix(t)
	addRoot(&fs, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"Reply with the word Ready."}],"environments":[],"outputSchema":{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}}}`)
	completeRoot(&fs, "103", "turn-1")
	addRoot(&fs, "c2s", `{"id":104,"method":"thread/settings/update","params":{"threadId":"thread-001","model":"synthetic-model"}}`)
	addRoot(&fs, "s2c", `{"id":104,"result":{"futureReceipt":true}}`)
	addRoot(&fs, "c2s", `{"id":105,"method":"thread/settings/update","params":{"threadId":"thread-001","effort":"high"}}`)
	addRoot(&fs, "s2c", `{"id":105,"result":{}}`)
	addRoot(&fs, "c2s", `{"id":106,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"Reply with the word Done."}],"environments":[]}}`)
	addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"completed","error":null}}}`)
	completeRoot(&fs, "106", "turn-2")
	c, ctx := rootClient(t, fs)
	session, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = session.Close() }()
	count := 0
	out, e := RunRoot(ctx, session, RootPlan{Scene: "r1", Model: "synthetic-model", Effort: "high", Reserve: func() error { count++; return nil }}, &Handler{})
	require.NoError(t, e)
	require.Equal(t, 2, count)
	require.Len(t, out.Turns, 2)
	require.Equal(t, "turn-2", out.Turns[1].Terminal.ID)
	require.True(t, out.Model.NativeAccepted)
	require.Contains(t, out.Model.Response.UnknownFields, "futureReceipt")
	require.True(t, out.Thinking.NativeAccepted)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
func TestExactNativeRequestPolicyAndSingleAnswer(t *testing.T) {
	params := `{"threadId":"thread-001","turnId":"turn-1","itemId":"item-1","startedAtMs":1,"command":"DO NOT EXECUTE"}`
	fs := rootPrefix(t)
	addRoot(&fs, "s2c", `{"id":"native-permission","method":"item/commandExecution/requestApproval","params":`+params+`}`)
	addRoot(&fs, "c2s", `{"id":"native-permission","result":{"decision":"decline"}}`)
	c, ctx := rootClient(t, fs)
	session, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = session.Close() }()
	received, e := session.Receive(ctx)
	require.NoError(t, e)
	require.NotNil(t, received.Request)
	h := Handler{Exact: []ExactRequest{{Method: "item/commandExecution/requestApproval", Params: json.RawMessage(params), Result: json.RawMessage(`{"decision":"decline"}`)}}}
	require.NoError(t, h.Answer(ctx, received.Request.Request))
	require.NoError(t, c.Call(ctx, "replay/answer", nil, nil))
	require.Error(t, h.Answer(ctx, received.Request.Request))
	require.Equal(t, 1, h.Answered)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
func TestStrictAddedControlsDoNotPermitExtraOrChangedRequests(t *testing.T) {
	fs := []Frame{frame(1, "c2s", `{"id":1,"method":"turn/interrupt","params":{"threadId":"root","turnId":"turn"}}`), frame(2, "s2c", `{"id":1,"result":{}}`)}
	for _, input := range []string{`{"id":9,"method":"turn/interrupt","params":{"threadId":"root","turnId":"other"}}` + "\n", `{"id":9,"method":"thread/resume","params":{"threadId":"root"}}` + "\n"} {
		require.Error(t, Peer(bytes.NewBufferString(input), io.Discard, fs))
	}
}

func TestExecutionTimeInputAndExplicitInterrupt(t *testing.T) {
	fs := rootPrefix(t)
	addRoot(&fs, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"Produce a short numbered list from one to ten."}],"environments":[]}}`)
	addRoot(&fs, "s2c", `{"id":103,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
	addRoot(&fs, "s2c", `{"method":"turn/started","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
	addRoot(&fs, "c2s", `{"id":104,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"Also include the word sample."}],"environments":[]}}`)
	addRoot(&fs, "s2c", `{"id":104,"result":{"turn":{"id":"turn-2","items":[],"status":"inProgress"}}}`)
	addRoot(&fs, "c2s", `{"id":105,"method":"turn/interrupt","params":{"threadId":"thread-001","turnId":"turn-1"}}`)
	addRoot(&fs, "s2c", `{"id":105,"result":{}}`)
	addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-2","items":[],"status":"completed","error":null}}}`)
	addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"interrupted","error":null}}}`)
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	attempts := 0
	out, e := RunRoot(ctx, s, RootPlan{Scene: "r3", Reserve: func() error { attempts++; return nil }}, &Handler{})
	require.NoError(t, e)
	require.Equal(t, 2, attempts)
	require.Equal(t, types.TurnStatusInterrupted, out.Turns[0].Terminal.Status)
	require.Equal(t, "turn-1", out.Turns[0].Terminal.ID)
	require.True(t, out.InterruptAccepted)
	require.False(t, out.Untriggered)
	require.Equal(t, "turn-2", out.Interleaved.Response.Turn.ID)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
func TestIndependentSavedQueryStrictPagesAndUnknownEvidence(t *testing.T) {
	prefix := rootPrefix(t)
	var response struct {
		Result struct{ Thread json.RawMessage }
	}
	require.NoError(t, json.Unmarshal(prefix[4].Wire, &response))
	thread := string(response.Result.Thread)
	fs := prefix[:3]
	for i, call := range []struct{ method, params, result string }{
		{"thread/list", `{"cwd":"/fixture/project","limit":1,"useStateDbOnly":true}`, `{"data":[` + thread + `],"nextCursor":null,"futureScope":true}`},
		{"thread/read", `{"threadId":"thread-001","includeTurns":false}`, `{"thread":` + thread + `}`},
		{"thread/turns/list", `{"threadId":"thread-001","limit":25,"itemsView":"summary"}`, `{"data":[],"nextCursor":null}`},
		{"thread/turns/list", `{"threadId":"thread-001","limit":25,"itemsView":"full"}`, `{"data":[],"nextCursor":null}`},
		{"thread/items/list", `{"threadId":"thread-001","limit":25}`, `{"data":[],"nextCursor":"opaque/+next"}`},
		{"thread/items/list", `{"threadId":"thread-001","limit":25,"cursor":"opaque/+next"}`, `{"data":[],"nextCursor":null}`},
	} {
		addRoot(&fs, "c2s", fmt.Sprintf(`{"id":%d,"method":%q,"params":%s}`, 200+i, call.method, call.params))
		addRoot(&fs, "s2c", fmt.Sprintf(`{"id":%d,"result":%s}`, 200+i, call.result))
	}
	c, ctx := rootClient(t, fs)
	out, e := QuerySavedRoot(ctx, c, "/fixture/project", "thread-001")
	require.NoError(t, e)
	require.Len(t, out.Calls, 6)
	require.True(t, out.ExplicitEnd)
	require.Contains(t, string(out.Calls[0].Result), `"futureScope":true`)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}

func TestTwoOriginalQuestionsExactTextAndAnswers(t *testing.T) {
	params := `{"threadId":"thread-001","turnId":"turn-1","itemId":"item-1","isBlocking":true,"questions":[{"id":"color-original","header":"Color","question":"Choose Blue or Green.","isOther":false,"isSecret":false,"options":[{"label":"Blue","description":"Blue color"},{"label":"Green","description":"Green color"}]},{"id":"label-original","header":"Label","question":"Enter a short label.","isOther":false,"isSecret":false,"options":null}]}`
	answer := `{"answers":{"color-original":{"answers":["Blue"]},"label-original":{"answers":["sample"]}}}`
	fs := rootPrefix(t)
	addRoot(&fs, "s2c", `{"id":"original-question","method":"item/tool/requestUserInput","params":`+params+`}`)
	addRoot(&fs, "c2s", `{"id":"original-question","result":`+answer+`}`)
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	got, e := s.Receive(ctx)
	require.NoError(t, e)
	require.NotNil(t, got.Request)
	h := Handler{Exact: []ExactRequest{{Method: "item/tool/requestUserInput", Params: json.RawMessage(params), Result: json.RawMessage(answer)}}}
	require.NoError(t, h.Answer(ctx, got.Request.Request))
	require.NoError(t, c.Call(ctx, "replay/answer", nil, nil))
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}

func TestDecodeRejectsTrailingTrafficBeyondCaptureLimit(t *testing.T) {
	var input strings.Builder
	for i, size := range []int{ProtocolLimit / 2, ProtocolLimit/2 + 1} {
		base := fmt.Sprintf(`{"ordinal":%d,"direction":"s2c","epoch":1,"wire":{"padding":""}}`+"\n", i+1)
		input.WriteString(strings.Replace(base, `"padding":""`, `"padding":"`+strings.Repeat("x", size-len(base))+`"`, 1))
	}
	input.WriteString(`{"unexpected":"trailing"}`)
	_, err := Decode(strings.NewReader(input.String()))
	require.Error(t, err)
}

func TestHeldNativeRequestContinuesReceiveAfterNonGeneratingControl(t *testing.T) {
	params := `{"threadId":"thread-001","turnId":"turn-1","itemId":"item-1","startedAtMs":1,"grantRoot":null}`
	fs := rootPrefix(t)
	addRoot(&fs, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic deferred request"}]}}`)
	addRoot(&fs, "s2c", `{"id":103,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
	addRoot(&fs, "s2c", `{"id":"held","method":"item/fileChange/requestApproval","params":`+params+`}`)
	addRoot(&fs, "c2s", `{"id":104,"method":"thread/settings/update","params":{"threadId":"thread-001"}}`)
	addRoot(&fs, "s2c", `{"id":104,"result":{}}`)
	addRoot(&fs, "s2c", `{"method":"future/progress","params":{"threadId":"thread-001","future":true}}`)
	addRoot(&fs, "c2s", `{"id":"held","result":{"decision":"decline"}}`)
	addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-1","items":[],"status":"completed","error":null}}}`)
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	h := Handler{HoldFirst: true, replayClient: c, WhileHeld: func(ctx context.Context) error { _, err := s.SetThinking(ctx, codex.ThinkingChange{}); return err }, Exact: []ExactRequest{{Method: "item/fileChange/requestApproval", Params: json.RawMessage(params), Result: json.RawMessage(`{"decision":"decline"}`)}}}
	out, e := RunSession(ctx, s, types.TurnStartParams{Input: []types.UserInput{{Text: &types.TextUserInput{Type: "text", Text: "synthetic deferred request"}}}}, &h)
	require.NoError(t, e)
	require.True(t, out.Completed)
	require.True(t, h.heldOnce)
	require.Equal(t, 1, h.Answered)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
func TestNativeStructuredInputRetainsTextElementsAndImageVariantOffline(t *testing.T) {
	fs := rootPrefix(t)
	input := `[{"type":"text","text":"Synthetic [image] input.","text_elements":[{"byteRange":{"start":10,"end":17},"placeholder":"[image]"}]},{"type":"image","url":"data:image/png;base64,synthetic-only"}]`
	addRoot(&fs, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":`+input+`}}`)
	completeRoot(&fs, "103", "turn-structured")
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	var native []types.UserInput
	require.NoError(t, json.Unmarshal([]byte(input), &native))
	out, e := RunSession(ctx, s, types.TurnStartParams{Input: native}, &Handler{})
	require.NoError(t, e)
	require.Equal(t, "turn-structured", out.Terminal.ID)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
	// This verifies native serialization only. The deliberately synthetic image
	// bytes are never submitted to a provider or claimed as accepted media.
}

func TestNativeTaskFailureStopsBeforeConfigurationAndSecondInput(t *testing.T) {
	fs := rootPrefix(t)
	addRoot(&fs, "c2s", `{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"Reply with the word Ready."}],"environments":[],"outputSchema":{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}}}`)
	addRoot(&fs, "s2c", `{"id":103,"result":{"turn":{"id":"failed-first","items":[],"status":"inProgress"}}}`)
	addRoot(&fs, "s2c", `{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"failed-first","items":[],"status":"failed","error":null}}}`)
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	attempts := 0
	out, e := RunRoot(ctx, s, RootPlan{Scene: "r1", Model: "synthetic-model", Effort: "high", Reserve: func() error { attempts++; return nil }}, &Handler{})
	require.ErrorContains(t, e, "failed")
	require.Equal(t, 1, attempts)
	require.Len(t, out.Turns, 1)
	require.Equal(t, types.TurnStatusFailed, out.Turns[0].Terminal.Status)
	require.False(t, out.Model.NativeAccepted)
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
func TestR2PolicyCannotHideMissingQuestionOutcome(t *testing.T) {
	denial := ExactRequest{Method: "item/fileChange/requestApproval", Params: json.RawMessage(`{}`), Result: json.RawMessage(`{"decision":"decline"}`)}
	require.Error(t, ValidateR2Policy([]ExactRequest{denial}))
	require.Error(t, ValidateR2Policy([]ExactRequest{denial, denial}))
}

func TestOriginalDynamicPolicyRejectsAndStopsOnExtraArguments(t *testing.T) {
	params := `{"threadId":"thread-001","turnId":"turn-1","callId":"item-1","namespace":null,"tool":"fixture_read","arguments":{"path":"numbers.txt","extra":"not allowed"}}`
	fs := rootPrefix(t)
	addRoot(&fs, "s2c", `{"id":"invalid-tool","method":"item/tool/call","params":`+params+`}`)
	addRoot(&fs, "c2s", `{"id":"invalid-tool","error":{"code":-32602,"message":"operation denied"}}`)
	c, ctx := rootClient(t, fs)
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, e)
	defer func() { _ = c.Close(); _ = s.Close() }()
	got, e := s.Receive(ctx)
	require.NoError(t, e)
	require.NotNil(t, got.Request)
	h := Handler{Scenario: "dynamic-tools", Root: t.TempDir()}
	require.ErrorContains(t, h.Answer(ctx, got.Request.Request), "out-of-policy")
	require.Zero(t, h.Calls)
	require.NoError(t, c.Call(ctx, "replay/answer", nil, nil))
	require.NoError(t, c.Call(ctx, "replay/assert", nil, nil))
	require.NoError(t, c.Close())
}
