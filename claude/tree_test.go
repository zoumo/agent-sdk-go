// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r := treeReceive(t, c)
	if r.Kind != "ObservationState" || r.State != "unverified" || r.Reason != "forwarding-not-confirmed" {
		t.Fatalf("startup: %+v", r)
	}
	return c
}
func treeReceive(t *testing.T, c *Client) relationshipRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := c.tree.receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func treeEmit(t *testing.T, c *Client, frame string) {
	t.Helper()
	if err := c.Send(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
}

func TestForwardSubagentTextHandshakeAndOptions(t *testing.T) {
	config := types.Options{}
	if got := OptionsFromTypes(config).Types(); !reflect.DeepEqual(got, config) {
		t.Fatal(got)
	}

	opts := fixtureOptions(t, "echo")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	opts.Model = "--model with ; literal"
	opts.ResumeID = "--resume with ; literal"
	c, err := openWithTree(context.Background(), opts, SubagentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertForwardCapture(t, capture, true)
	data, _ := os.ReadFile(capture)
	var captured struct{ Argv []string }
	json.Unmarshal(data, &captured)
	want := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages", "--model=" + opts.Model, "--resume=" + opts.ResumeID}
	if !reflect.DeepEqual(captured.Argv, want) || c.RequestedResumeID() != opts.ResumeID {
		t.Fatal(captured)
	}
}
func assertForwardCapture(t *testing.T, path string, enabled bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Init struct{ Request map[string]json.RawMessage }
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	value, present := got.Init.Request["forwardSubagentText"]
	if enabled && string(value) != "true" || !enabled && present {
		t.Fatalf("initialize: %s", data)
	}
}

func TestTreeSingleLayerNativeLosslessAndAssociationOrder(t *testing.T) {
	c := openTreeFixture(t)
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
			if r.Sequence != seq || r.Provider != "claude" || r.Epoch == 0 || r.Provenance != "live" || r.UsageScope != "unknown" {
				t.Fatalf("metadata: %+v", r)
			}
			if r.Kind == "SubagentCreated" {
				t.Fatal("resume misreported as creation")
			}
			if r.Kind == "SubagentAssociated" {
				associations++
				if r.Source.TaskID != "child" || r.Parent == nil || r.Parent.SessionID != "root" || r.Scope != "child" {
					t.Fatalf("association: %+v", r)
				}
				// Records must not share mutable parent storage with the graph/pending native.
				r.Parent.SessionID = "tampered"
				continue
			}
			if r.Kind != "Native" || r.Native == nil || string(r.Native.Raw) != frame {
				t.Fatalf("native %d: %+v", i, r)
			}
			if i == 0 || i == 6 {
				if r.Scope != "root" || r.Source.AgentID != "" {
					t.Fatal(r)
				}
			} else {
				if associations == 0 || r.Scope != "child" || r.Parent == nil || r.Parent.SessionID != "root" {
					t.Fatalf("child: %+v", r)
				}
			}
			if i == 2 && (r.Native.AgentID == nil || *r.Native.AgentID != "child" || r.Native.SubagentType == nil || *r.Native.SubagentType != "Explore" || len(r.Native.Assistant.Content.Blocks) != 4) {
				t.Fatal(r)
			}
			if i == 6 && r.Native.Result.Result != "parent only" {
				t.Fatal("mixed parent answer")
			}
			break
		}
	}
	if associations != 2 {
		t.Fatalf("associations = %d, want first-observed + identity enrichment", associations)
	}
	if id, ok := c.SessionID(); !ok || id != "root" {
		t.Fatal(id, ok)
	}
	if err := c.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.tree.receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	treeEmit(t, c, `{"type":"assistant","session_id":"root","message":{"content":"still open"}}`)
	if r := treeReceive(t, c); r.Native == nil {
		t.Fatal(r)
	}
}

func TestTreeUnresolvedAndNonAgentTask(t *testing.T) {
	c := openTreeFixture(t)
	for _, frame := range []string{
		`{"type":"system","subtype":"task_started","task_id":"bash","task_type":"local_bash","session_id":"root"}`,
		`{"type":"assistant","agent_id":"unscoped","parent_tool_use_id":"missing","message":{"content":"unknown session"}}`,
		`{"type":"assistant","parent_tool_use_id":"legacy-tool","session_id":"root","message":{"content":"legacy"}}`,
		`{"type":"future","future":null}`,
	} {
		treeEmit(t, c, frame)
		r := treeReceive(t, c)
		if r.Kind == "SubagentAssociated" {
			if r.Source.AgentID != "" || r.Source.TaskID != "" || r.Source.ToolUseID != "legacy-tool" {
				t.Fatal(r)
			}
			r = treeReceive(t, c)
		}

		if r.Kind == "ObservationState" && r.Reason == "unresolved-source" {
			r = treeReceive(t, c)
		}
		if r.Kind != "Native" || string(r.Native.Raw) != frame {
			t.Fatal(r)
		}
		if r.Source.AgentID != "" && r.Scope != "unresolved" {
			t.Fatal(r)
		}
	}
}

func TestTreePermissionUsesOriginalHandleAfterParentResult(t *testing.T) {
	requests := make(chan *PermissionRequest, 1)
	c := openTreeFixture(t, func(o *Options) { o.PermissionHandler = func(p *PermissionRequest) { requests <- p } })
	treeEmit(t, c, `{"type":"result","subtype":"success","session_id":"root","result":"parent"}`)
	treeReceive(t, c)
	frame := `{"type":"control_request","request_id":"child-question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","agent_id":"child","input":{"questions":[{"question":"Choose?","header":"Choice","options":[{"label":"A"}]}],"future":42}}}`
	treeEmit(t, c, frame)
	r := treeReceive(t, c)
	if r.Native == nil || r.Native.Control.RequestID != "child-question" || r.Source.AgentID != "child" || r.Scope != "unresolved" {
		t.Fatal(r)
	}
	p := awaitPermission(t, requests)
	metadata, err := p.Metadata()
	if err != nil || metadata.AgentID != "child" || p.RequestID() != "child-question" {
		t.Fatal(metadata, err)
	}
	if err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "fixture denial"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny}); !errors.Is(err, ErrPermissionUsed) {
		t.Fatal(err)
	}
	reply := treeReceive(t, c)
	var wire struct{ Wire types.ControlResponse }
	json.Unmarshal(reply.Native.Raw, &wire)
	if wire.Wire.Response.RequestID != "child-question" {
		t.Fatal(string(reply.Native.Raw))
	}
	ds, killed := treeFrame(t, c, `{"type":"system","subtype":"task_updated","session_id":"root","task_id":"child","run_id":"1","patch":{"status":"killed"}}`)
	if !killed.RunTerminal || killed.RunStatus != "killed" || !treeHas(ds, "ObservationState", "native-task-terminal") {
		t.Fatal(ds, killed)
	}
}

func TestTreeBoundsRejectionAndCloseRace(t *testing.T) {
	if _, err := openWithTree(context.Background(), fixtureOptions(t, "tree"), SubagentOptions{MaxNodes: -1}); err == nil {
		t.Fatal("invalid bound accepted")
	}
	if c, err := openWithTree(context.Background(), fixtureOptions(t, "init_error"), SubagentOptions{}); err == nil || c != nil {
		t.Fatal("rejection accepted", c, err)
	}
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxUnresolvedWait: time.Nanosecond, MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	treeReceive(t, c)
	for _, id := range []string{"a", "b"} {
		frame := fmt.Sprintf(`{"type":"assistant","session_id":"root","agent_id":%q,"message":{"content":"fixture"}}`, id)
		treeEmit(t, c, frame)
		r := treeReceive(t, c)
		if id == "a" {
			if r.Kind != "SubagentAssociated" {
				t.Fatal(r)
			}

			if d := treeReceive(t, c); d.Reason != "unresolved-source" {
				t.Fatal(d)
			}
			treeReceive(t, c)
		} else {
			if r.State != "incomplete" || r.Reason != "association-limit" {
				t.Fatal(r)
			}
			if r = treeReceive(t, c); r.Native == nil || string(r.Native.Raw) != frame {
				t.Fatal("overflow frame lost", r)
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
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatal(err)
	}
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
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestSubagentEnvelopeNullAbsentAndTypedEdits(t *testing.T) {
	for _, frame := range []string{`{"type":"assistant","message":{"content":[]}}`, `{"type":"assistant","agent_id":null,"subagent_type":null,"message":{"content":[]}}`, `{"type":"assistant","agent_id":"child","subagent_type":"Explore","message":{"content":[]},"unknown":42}`} {
		e, err := ParseEvent([]byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		json.Unmarshal([]byte(frame), &a)
		json.Unmarshal(encoded, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("round trip %s => %s", frame, encoded)
		}
		value := "edited"
		e.AgentID = &value
		e.Fields = map[string]FieldState{"subagent_type": Present}
		encoded, err = json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]json.RawMessage
		json.Unmarshal(encoded, &obj)
		if string(obj["agent_id"]) != `"edited"` {
			t.Fatal(string(encoded))
		}
	}
}

const earlyTreeFrame = ` {"type":"assistant","session_id":"root","agent_id":"early","parent_tool_use_id":"not-observed","message":{"content":"before initialize ack"},"extra":null} `

func TestTreeForwardingBeforeFirstNativeAndDisconnect(t *testing.T) {
	c, err := openWithTree(context.Background(), fixtureOptions(t, "tree_early"), SubagentOptions{MaxUnresolvedWait: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if r := treeReceive(t, c); r.State != "unverified" {
		t.Fatal(r)
	}
	if r := treeReceive(t, c); r.Kind != "SubagentAssociated" || r.Scope != "unresolved" || r.Parent != nil {
		t.Fatal(r)
	}

	if r := treeReceive(t, c); r.Reason != "unresolved-source" {
		t.Fatal(r)
	}
	if r := treeReceive(t, c); r.Native == nil || string(r.Native.Raw) != earlyTreeFrame {
		t.Fatal(r)
	}
	c2 := openTreeFixture(t)
	if err := c2.Send(context.Background(), "disconnect"); err != nil {
		t.Fatal(err)
	}
	if r := treeReceive(t, c2); r.State != "incomplete" || r.Reason != "stream-ended" {
		t.Fatal(r)
	}
	if _, err := c2.tree.receive(context.Background()); !errors.Is(err, ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

// Unknown source-less system notifications must not acquire the root agent's
// identity merely because their envelope names the same native session.
func TestTreeUnknownSystemSubtypeNeedsSourceEvidence(t *testing.T) {
	c := openTreeFixture(t)
	for _, frame := range []string{
		`{"type":"system","subtype":"future_notice","session_id":"s","uuid":"unknown-id","future":null}`,
		`{"type":"system","subtype":"future_notice","session_id":"s","uuid":"unknown-id","future":null}`,
		`{"type":"system","session_id":"s","future":42}`,
		`{"type":"future_notice","session_id":"s","future":null}`,
	} {
		derived, r := treeFrame(t, c, frame)
		if len(derived) != 0 || r.Scope != "unscoped" || r.Source.AssociationID != "" {
			t.Fatalf("unknown event acquired source: %+v %+v", derived, r)
		}
	}
	_, init := treeFrame(t, c, `{"type":"system","subtype":"init","session_id":"s"}`)
	if init.Scope != "root" {
		t.Fatal(init)
	}
	treeFrame(t, c, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"child","parent_tool_use_id":"spawn","message":{"content":"child"}}`)
	_, child := treeFrame(t, c, `{"type":"system","subtype":"future_notice","session_id":"s","agent_id":"child","future":null}`)
	if child.Scope != "child" || child.Source.AgentID != "child" || child.Parent == nil || child.Parent.SessionID != "s" {
		t.Fatalf("known source lost attribution: %+v", child)
	}
}

// Tests exercise the sole public entry point with resource/history configuration.
func openWithTree(ctx context.Context, options Options, bounds SubagentOptions) (*Client, error) {
	options.Subagents = bounds
	return Open(ctx, options)
}

func TestDefaultManagedOpenAndNativeOriginBoundary(t *testing.T) {
	opts := fixtureOptions(t, "tree")
	capture := filepath.Join(opts.WorkDir, "capture.json")
	opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
	c, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertForwardCapture(t, capture, true)
	first := treeReceive(t, c)
	if first.Kind != "ObservationState" || first.State != "unverified" || first.Native != nil {
		t.Fatal(first)
	}
	for _, origin := range []string{"", `,"origin":null`, `,"origin":{"kind":"future","extra":{"keep":true}}`} {
		frame := ` {"type":"result","session_id":"s","subtype":"success","future":7` + origin + `} `
		_, record := treeFrame(t, c, frame)
		native := record.Native
		var official *types.MessageOrigin = native.Origin
		var resultOrigin *types.MessageOrigin = native.Result.Origin
		if string(native.Raw) != frame || !reflect.DeepEqual(official, resultOrigin) {
			t.Fatal(record)
		}
		encoded, err := json.Marshal(native)
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		if json.Unmarshal([]byte(frame), &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("native changed: %s", encoded)
		}
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
		if c, err := Open(context.Background(), opts); err == nil || c != nil {
			if c != nil {
				c.Close()
			}
			t.Fatal("invalid tree accepted", bounds)
		}
		if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("process started", err)
		}
	}
}
