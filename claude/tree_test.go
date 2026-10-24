// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

// Original synthetic stream-json peer; never executes an installed model/CLI.
func runTreeFixture(reader *bufio.Reader) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var w struct {
			Type      string
			RequestID string `json:"request_id"`
			Message   struct{ Content string }
		}
		if json.Unmarshal(line, &w) != nil {
			os.Exit(12)
		}
		switch w.Type {
		case "user":
			if w.Message.Content == "disconnect" {
				return
			}
			if !json.Valid([]byte(w.Message.Content)) {
				os.Exit(13)
			}
			fmt.Fprintln(os.Stdout, w.Message.Content)
		case "control_request":
			sessionReply(w.RequestID, "")
		case "control_response":
			fixtureEmit(map[string]any{"type": "reply_seen", "wire": json.RawMessage(line)})
		}
	}
}
func openTreeFixture(t *testing.T, configure ...func(*Options)) *Client {
	t.Helper()
	opts := fixtureOptions(t, "tree")
	for _, f := range configure {
		f(&opts)
	}
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxUnresolvedWait: time.Nanosecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	r := treeReceive(t, c)
	require.Falsef(t, r.Kind != "ObservationState" || r.State != "unverified" || r.Reason != "forwarding-not-confirmed", "startup: %+v", r)
	return c
}
func treeReceive(t *testing.T, c *Client) relationshipRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := c.tree.receive(ctx)
	require.NoError(t, err)
	return r
}
func treeEmit(t *testing.T, c *Client, frame string) {
	t.Helper()
	{
		err := c.Send(context.Background(), frame)
		require.NoError(t, err)
	}
}

func (s *treeObservationSuite) TestForwardSubagentTextHandshakeAndOptions() {
	t := s.T()
	config := types.Options{}
	{
		got := OptionsFromTypes(config).Types()
		require.True(t, reflect.DeepEqual(got, config), got)
	}

	opts := fixtureOptions(t, "echo")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	opts.Model = "--model with ; literal"
	opts.ResumeID = "--resume with ; literal"
	c, err := openWithTree(context.Background(), opts, SubagentOptions{})
	require.NoError(t, err)
	defer c.Abort()
	assertForwardCapture(t, capture, true)
	data, _ := os.ReadFile(capture)
	var captured struct{ Argv []string }
	json.Unmarshal(data, &captured)
	want := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages", "--model=" + opts.Model, "--resume=" + opts.ResumeID}
	require.False(t, !reflect.DeepEqual(captured.Argv, want) || c.RequestedResumeID() != opts.ResumeID, captured)
}
func assertForwardCapture(t *testing.T, path string, enabled bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got struct {
		Init struct{ Request map[string]json.RawMessage }
	}
	{
		err := json.Unmarshal(data, &got)
		require.NoError(t, err)
	}
	value, present := got.Init.Request["forwardSubagentText"]
	require.Falsef(t, enabled && string(value) != "true" || !enabled && present, "initialize: %s", data)
}

func (s *treeObservationSuite) TestTreeSingleLayerNativeLosslessAndAssociationOrder() {
	t := s.T()
	c := s.client
	frames := []string{
		` {"type":"assistant","session_id":"root","uuid":"parent","message":{"role":"assistant","content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}} `,
		`{"type":"system","subtype":"task_started","session_id":"root","task_id":"child","task_type":"local_agent","tool_use_id":"spawn","future":null}`,
		` {"type":"assistant","session_id":"root","uuid":"child-msg","agent_id":"child","subagent_type":"Explore","parent_tool_use_id":"spawn","message":{"role":"assistant","content":[{"type":"text","text":"child text"},{"type":"thinking","thinking":"child thinking","signature":"sig"},{"type":"tool_use","id":"read","name":"Read","input":{"path":"fixture"}},{"type":"future_block","future":42}],"extra":null},"unknown":{"keep":true}} `,
		`{"type":"user","session_id":"root","agent_id":"child","parent_tool_use_id":"spawn","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"read","content":"result","future":null}]}}`,
		`{"type":"future_delta","session_id":"root","agent_id":"child","delta":"same","future":null}`,
		`{"type":"future_delta","session_id":"root","agent_id":"child","delta":"same","future":null}`,
		`{"type":"result","session_id":"root","subtype":"success","result":"parent only","usage":{"output_tokens":3}}`,
		`{"type":"assistant","session_id":"root","agent_id":"child","parent_tool_use_id":"spawn","message":{"content":"child after parent"}}`,
	}
	var seq uint64 = 1
	associations := 0
	for i, frame := range frames {
		treeEmit(t, c, frame)
		for {
			r := treeReceive(t, c)
			seq++
			require.Falsef(t, r.Sequence != seq || r.Provider != "claude" || r.Epoch == 0 || r.Provenance != "live" || r.UsageScope != "unknown", "metadata: %+v", r)
			require.NotEqualValues(t, "SubagentCreated", r.Kind, "resume misreported as creation")
			if r.Kind == "SubagentAssociated" {
				associations++
				require.Falsef(t, r.Source.TaskID != "child" || r.Parent == nil || r.Parent.SessionID != "root" || r.Scope != "child", "association: %+v", r)
				// Records must not share mutable parent storage with the graph/pending native.
				r.Parent.SessionID = "tampered"
				continue
			}
			require.Falsef(t, r.Kind != "Native" || r.Native == nil || string(r.Native.Raw) != frame, "native %d: %+v", i, r)
			if i == 0 || i == 6 {
				require.False(t, r.Scope != "root" || r.Source.AgentID != "", r)
			} else {
				require.Falsef(t, associations == 0 || r.Scope != "child" || r.Parent == nil || r.Parent.SessionID != "root", "child: %+v", r)
			}
			require.False(t, i == 2 && (r.Native.AgentID == nil || *r.Native.AgentID != "child" || r.Native.SubagentType == nil || *r.Native.SubagentType != "Explore" || len(r.Native.Assistant.Content.Blocks) != 4), r)
			require.False(t, i == 6 && r.Native.Result.Result != "parent only", "mixed parent answer")
			break
		}
	}
	require.EqualValuesf(t, 2, associations, "associations = %d, want first-observed + identity enrichment", associations)
	{
		id, ok := c.SessionID()
		require.False(t, !ok || id != "root", id, ok)
	}
	{
		err := c.Interrupt(context.Background())
		require.NoError(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		_, err := c.tree.receive(ctx)
		require.ErrorIs(t, err, context.Canceled, err)
	}
	treeEmit(t, c, `{"type":"assistant","session_id":"root","message":{"content":"still open"}}`)
	{
		r := treeReceive(t, c)
		require.NotNil(t, r.Native, r)
	}
}

func (s *treeObservationSuite) TestTreeUnresolvedAndNonAgentTask() {
	t := s.T()
	c := s.client
	for _, frame := range []string{
		`{"type":"system","subtype":"task_started","task_id":"bash","task_type":"local_bash","session_id":"root"}`,
		`{"type":"assistant","agent_id":"unscoped","parent_tool_use_id":"missing","message":{"content":"unknown session"}}`,
		`{"type":"assistant","parent_tool_use_id":"legacy-tool","session_id":"root","message":{"content":"legacy"}}`,
		`{"type":"future","future":null}`,
	} {
		treeEmit(t, c, frame)
		r := treeReceive(t, c)
		if r.Kind == "SubagentAssociated" {
			require.False(t, r.Source.AgentID != "" || r.Source.TaskID != "" || r.Source.ToolUseID != "legacy-tool", r)
			r = treeReceive(t, c)
		}

		if r.Kind == "ObservationState" && r.Reason == "unresolved-source" {
			r = treeReceive(t, c)
		}
		require.False(t, r.Kind != "Native" || string(r.Native.Raw) != frame, r)
		require.False(t, r.Source.AgentID != "" && r.Scope != "unresolved", r)
	}
}

func (s *treeObservationSuite) TestTreePermissionUsesOriginalHandleAfterParentResult() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := openTreeFixture(t, func(o *Options) { o.PermissionHandler = func(p *PermissionRequest) { requests <- p } })
	treeEmit(t, c, `{"type":"result","subtype":"success","session_id":"root","result":"parent"}`)
	treeReceive(t, c)
	frame := `{"type":"control_request","request_id":"child-question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","agent_id":"child","input":{"questions":[{"question":"Choose?","header":"Choice","options":[{"label":"A"}]}],"future":42}}}`
	treeEmit(t, c, frame)
	r := treeReceive(t, c)
	require.False(t, r.Native == nil || r.Native.Control.RequestID != "child-question" || r.Source.AgentID != "child" || r.Scope != "unresolved", r)
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	require.False(t, err != nil || metadata.AgentID != "child" || p.RequestID() != "child-question", metadata, err)
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "fixture denial"})
		require.NoError(t, err)
	}
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.ErrorIs(t, err, ErrPermissionUsed, err)
	}
	reply := treeReceive(t, c)
	var wire struct{ Wire types.ControlResponse }
	json.Unmarshal(reply.Native.Raw, &wire)
	require.EqualValues(t, "child-question", wire.Wire.Response.RequestID, string(reply.Native.Raw))
	ds, killed := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"root","task_id":"child","run_id":"1","patch":{"status":"killed"}}`)
	require.False(t, !killed.RunTerminal || killed.RunStatus != "killed" || !treeHas(ds, "ObservationState", "native-task-terminal"), ds, killed)
}

func (s *treeObservationSuite) TestTreeBoundsRejectionAndCloseRace() {
	t := s.T()
	{
		_, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), SubagentOptions{MaxNodes: -1})
		require.NotNil(t, err, "invalid bound accepted")
	}
	{
		c, err := openWithTree(context.Background(), fixtureOptions(t, "init_error"), SubagentOptions{})
		require.False(t, err == nil || c != nil, "rejection accepted", c, err)
	}
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxUnresolvedWait: time.Nanosecond, MaxNodes: 1})
	require.NoError(t, err)
	defer c.Abort()
	treeReceive(t, c)
	for _, id := range []string{"a", "b"} {
		frame := fmt.Sprintf(`{"type":"assistant","session_id":"root","agent_id":%q,"message":{"content":"fixture"}}`, id)
		treeEmit(t, c, frame)
		r := treeReceive(t, c)
		if id == "a" {
			require.EqualValues(t, "SubagentAssociated", r.Kind, r)

			{
				d := treeReceive(t, c)
				require.EqualValues(t, "unresolved-source", d.Reason, d)
			}
			treeReceive(t, c)
		} else {
			require.False(t, r.State != "incomplete" || r.Reason != "association-limit", r)
			{
				r = treeReceive(t, c)
				require.False(t, r.Native == nil || string(r.Native.Raw) != frame, "overflow frame lost", r)
			}
		}
	}
	// The native client terminal ResourceLimit remains visible after draining.
	for {
		_, err = c.tree.receive(context.Background())
		if err != nil {
			break
		}
	}
	require.ErrorIs(t, err, ErrResourceLimit, err)
	c2 := openTreeFixture(t)
	var wg sync.WaitGroup
	wg.Go(func() { _ = c2.Close() })
	wg.Go(func() { _ = c2.Close() })
	for {
		_, err = c2.tree.receive(context.Background())
		if err != nil {
			break
		}
	}
	wg.Wait()
	require.ErrorIs(t, err, io.EOF, err)
}

func TestSubagentEnvelopeNullAbsentAndTypedEdits(t *testing.T) {
	for _, frame := range []string{`{"type":"assistant","message":{"content":[]}}`, `{"type":"assistant","agent_id":null,"subagent_type":null,"message":{"content":[]}}`, `{"type":"assistant","agent_id":"child","subagent_type":"Explore","message":{"content":[]},"unknown":42}`} {
		e, err := ParseEvent([]byte(frame))
		require.NoError(t, err)
		encoded, err := json.Marshal(e)
		require.NoError(t, err)
		var a, b any
		json.Unmarshal([]byte(frame), &a)
		json.Unmarshal(encoded, &b)
		require.Truef(t, reflect.DeepEqual(a, b), "round trip %s => %s", frame, encoded)
		value := "edited"
		e.AgentID = &value
		e.Fields = map[string]FieldState{"subagent_type": Present}
		encoded, err = json.Marshal(e)
		require.NoError(t, err)
		var obj map[string]json.RawMessage
		json.Unmarshal(encoded, &obj)
		require.EqualValues(t, `"edited"`, string(obj["agent_id"]), string(encoded))
	}
}

const earlyTreeFrame = ` {"type":"assistant","session_id":"root","agent_id":"early","parent_tool_use_id":"not-observed","message":{"content":"before initialize ack"},"extra":null} `

func (s *treeObservationSuite) TestTreeForwardingBeforeFirstNativeAndDisconnect() {
	t := s.T()
	c, err := openWithTree(context.Background(), fixtureOptions(t, "tree_early"), SubagentOptions{MaxUnresolvedWait: time.Nanosecond})
	require.NoError(t, err)
	defer c.Abort()
	{
		r := treeReceive(t, c)
		require.EqualValues(t, "unverified", r.State, r)
	}
	{
		r := treeReceive(t, c)
		require.False(t, r.Kind != "SubagentAssociated" || r.Scope != "unresolved" || r.Parent != nil, r)
	}

	{
		r := treeReceive(t, c)
		require.EqualValues(t, "unresolved-source", r.Reason, r)
	}
	{
		r := treeReceive(t, c)
		require.False(t, r.Native == nil || string(r.Native.Raw) != earlyTreeFrame, r)
	}
	c2 := openTreeFixture(t)
	{
		err := c2.Send(context.Background(), "disconnect")
		require.NoError(t, err)
	}
	{
		r := treeReceive(t, c2)
		require.False(t, r.State != "incomplete" || r.Reason != "stream-ended", r)
	}
	{
		_, err := c2.tree.receive(context.Background())
		require.ErrorIs(t, err, ErrUnexpectedEOF, err)
	}
}

// Unknown source-less system notifications must not acquire the root agent's
// identity merely because their envelope names the same native session.
func (s *treeObservationSuite) TestTreeUnknownSystemSubtypeNeedsSourceEvidence() {
	t := s.T()
	c := s.client
	for _, frame := range []string{
		`{"type":"system","subtype":"future_notice","session_id":"s","uuid":"unknown-id","future":null}`,
		`{"type":"system","subtype":"future_notice","session_id":"s","uuid":"unknown-id","future":null}`,
		`{"type":"system","session_id":"s","future":42}`,
		`{"type":"future_notice","session_id":"s","future":null}`,
	} {
		derived, r := treeFrame(t, c, frame)
		require.Falsef(t, len(derived) != 0 || r.Scope != "unscoped" || r.Source.AssociationID != "", "unknown event acquired source: %+v %+v", derived, r)
	}
	_, init := treeFrame(t, c, `{"type":"system","subtype":"init","session_id":"s"}`)
	require.EqualValues(t, "root", init.Scope, init)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","parent_tool_use_id":"spawn","message":{"content":"child"}}`)
	_, child := treeFrame(t, c, `{"type":"system","subtype":"future_notice","session_id":"s","agent_id":"child","future":null}`)
	require.Falsef(t, child.Scope != "child" || child.Source.AgentID != "child" || child.Parent == nil || child.Parent.SessionID != "s", "known source lost attribution: %+v", child)
}

// Tests exercise the sole public entry point with resource/history configuration.
func openWithTree(ctx context.Context, options Options, bounds SubagentOptions) (*Client, error) {
	options.Subagents = bounds
	return NewClient(ctx, options)
}

func (s *treeObservationSuite) TestDefaultManagedOpenAndNativeOriginBoundary() {
	t := s.T()
	opts := fixtureOptions(t, "tree")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	c, err := NewClient(context.Background(), opts)
	require.NoError(t, err)
	defer c.Abort()
	assertForwardCapture(t, capture, true)
	first := treeReceive(t, c)
	require.False(t, first.Kind != "ObservationState" || first.State != "unverified" || first.Native != nil, first)
	for _, origin := range []string{"", `,"origin":null`, `,"origin":{"kind":"future","extra":{"keep":true}}`} {
		frame := ` {"type":"result","session_id":"s","subtype":"success","future":7` + origin + `} `
		_, record := treeFrame(t, c, frame)
		native := record.Native
		var official *types.MessageOrigin = native.Origin
		var resultOrigin *types.MessageOrigin = native.Result.Origin
		require.False(t, string(native.Raw) != frame || !reflect.DeepEqual(official, resultOrigin), record)
		encoded, err := json.Marshal(native)
		require.NoError(t, err)
		var before, after any
		require.Falsef(t, json.Unmarshal([]byte(frame), &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after), "native changed: %s", encoded)
	}
}

func TestDefaultManagedRejectsTreeBeforeSpawn(t *testing.T) {
	for _, bounds := range []SubagentOptions{
		{MaxNodes: -1}, {History: []SubagentHistory{{SessionID: "s"}}},
		{MaxPendingRecords: 1, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b"}}},
	} {
		opts := fixtureOptions(t, "echo")
		capture := filepath.Join(opts.WorkDir, "capture.json")
		opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
		opts.Subagents = bounds
		if c, err := NewClient(context.Background(), opts); err == nil || c != nil {
			if c != nil {
				c.Close()
			}
			require.FailNow(t, "verification failed", "invalid tree accepted", bounds)
		}
		{
			_, err := os.Stat(capture)
			require.ErrorIs(t, err, os.ErrNotExist, "process started", err)
		}
	}
}
