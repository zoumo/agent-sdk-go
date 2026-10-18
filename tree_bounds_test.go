// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
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

	claude "github.com/zoumo/claude-agent-sdk-go"
)

func openBoundedTree(t *testing.T, bounds claude.TreeOptions, configure ...func(*claude.Options)) *claude.Client {
	t.Helper()
	opts := fixtureOptions(t, "tree")
	for _, f := range configure {
		f(&opts)
	}
	c, err := openWithTree(context.Background(), opts, bounds)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if r := treeReceive(t, c); r.State != "unverified" {
		t.Fatal(r)
	}
	return c
}

const heldChild = ` {"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"spawn","message":{"content":[{"type":"text","text":"child"},{"type":"thinking","thinking":"private","signature":"sig"}],"usage":{"output_tokens":7}},"future":null} `
const rootTool = `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`

func TestTreeBufferedSourceResolvesBeforeNativeWithoutReplay(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedWait: time.Second})
	treeEmit(t, c, heldChild)
	first := treeReceive(t, c)
	if first.Kind != "SubagentAssociated" || first.Scope != "unresolved" {
		t.Fatal(first)
	}
	// The call timeout neither discards the held frame nor stops the process.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	treeEmit(t, c, rootTool)
	enriched := treeReceive(t, c)
	if enriched.Kind != "SubagentAssociated" || enriched.Scope != "child" || enriched.Reason != "identity-enriched" {
		t.Fatal(enriched)
	}
	child := treeReceive(t, c)
	if child.Kind != "Native" || child.Scope != "child" || child.Source.TaskID != "a" || child.Source.ToolUseID != "spawn" || string(child.Native.Raw) != heldChild || child.Sequence <= enriched.Sequence {
		t.Fatal(child)
	}
	parent := treeReceive(t, c)
	if parent.Native == nil || string(parent.Native.Raw) != rootTool {
		t.Fatal(parent)
	}
	treeFrame(t, c, `{"type":"marker"}`) // no replay ahead of the marker
}

func TestTreeUnresolvedExpiryPreservesRawAndOnlyEnrichesLater(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedWait: 30 * time.Millisecond})
	treeEmit(t, c, heldChild)
	treeReceive(t, c) // association
	d := treeReceive(t, c)
	if d.Kind != "ObservationState" || d.State != "incomplete" || d.Reason != "unresolved-source" || d.Scope != "unresolved" {
		t.Fatal(d)
	}
	r := treeReceive(t, c)
	if r.Scope != "unresolved" || r.Reason != "unresolved-source" || string(r.Native.Raw) != heldChild {
		t.Fatal(r)
	}
	ds, _ := treeFrame(t, c, rootTool)
	if !treeHas(ds, "SubagentAssociated", "identity-enriched") {
		t.Fatal(ds)
	}
	treeFrame(t, c, `{"type":"marker"}`)
	select {
	case <-c.Done():
		t.Fatal(c.Err())
	default:
	}
}

func TestTreeUnresolvedCapacityFailsVisiblyWithoutPartialFanout(t *testing.T) {
	for _, mode := range []string{"count", "bytes", "single-frame"} {
		t.Run(mode, func(t *testing.T) {
			opts := claude.TreeOptions{MaxUnresolvedWait: time.Minute, MaxUnresolvedEvents: 1}
			if mode == "bytes" {
				opts.MaxUnresolvedEvents = 10
				opts.MaxUnresolvedBytes = len(heldChild)
			}
			if mode == "single-frame" {
				opts.MaxUnresolvedBytes = len(heldChild) - 1
			}
			c := openBoundedTree(t, opts)
			treeEmit(t, c, heldChild)
			expected := map[string]int{heldChild: 1}
			if mode != "single-frame" {
				if r := treeReceive(t, c); r.Kind != "SubagentAssociated" {
					t.Fatal(r)
				}
				next := `{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"overflow"}}`
				expected[next] = 1
				treeEmit(t, c, next)
			}
			d := treeReceive(t, c)
			if d.State != "incomplete" || d.Reason != "unresolved-source" {
				t.Fatal("partial fanout or missing diagnostic", d)
			}
			<-c.Done()
			if !errors.Is(c.Err(), claude.ErrResourceLimit) {
				t.Fatal(c.Err())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var nativeOrder []string
			for {
				r, err := c.Receive(ctx)
				if err != nil {
					if !errors.Is(err, claude.ErrResourceLimit) {
						t.Fatal(err)
					}
					break
				}
				if r.Kind == "SubagentAssociated" {
					t.Fatal("rejected association published", r)
				}
				if r.Native != nil {
					nativeOrder = append(nativeOrder, string(r.Native.Raw))
					expected[string(r.Native.Raw)]--
					if r.Scope != "unresolved" {
						t.Fatal(r)
					}
				}
			}
			if len(nativeOrder) == 0 || nativeOrder[0] != heldChild {
				t.Fatal("overflow overtook held native", nativeOrder)
			}
			for frame, n := range expected {
				if n != 0 {
					t.Fatalf("native delivered other than once: %d %s", n, frame)
				}
			}
		})
	}
}

func TestTreeAtomicFanoutAndHistoryBounds(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			c := openBoundedTree(t, claude.TreeOptions{MaxPendingRecords: limit})
			frame := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"1"}`
			treeEmit(t, c, frame) // association + run-observed + native = 3
			r := treeReceive(t, c)
			if r.Reason != "fanout-limit" {
				t.Fatal("partial association escaped", r)
			}
			if r = treeReceive(t, c); r.Native == nil || string(r.Native.Raw) != frame || r.Scope != "unresolved" {
				t.Fatal(r)
			}
			for {
				r, err := c.Receive(context.Background())
				if err != nil {
					if !errors.Is(err, claude.ErrResourceLimit) {
						t.Fatal(err)
					}
					break
				}
				if r.Kind == "SubagentAssociated" {
					t.Fatal(r)
				}
			}
		})
	}
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, claude.TreeOptions{MaxPendingRecords: 1, History: []claude.TreeSeed{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b"}}})
	if c != nil || !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatal(c, err)
	}
}

func TestTreeNewBoundsRejectBeforeStartup(t *testing.T) {
	for _, bounds := range []claude.TreeOptions{
		{MaxPendingRecords: -1}, {MaxPendingRecords: 131073},
		{MaxUnresolvedEvents: -1}, {MaxUnresolvedEvents: 65537},
		{MaxUnresolvedBytes: -1}, {MaxUnresolvedBytes: 64<<20 + 1},
		{MaxUnresolvedWait: -1}, {MaxUnresolvedWait: time.Minute + 1},
	} {
		opts := fixtureOptions(t, "tree")
		capture := filepath.Join(opts.WorkDir, "capture")
		opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
		c, err := openWithTree(context.Background(), opts, bounds)
		if c != nil || err == nil {
			t.Fatal(bounds, c, err)
		}
		if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("started child", err)
		}
	}
}

func TestTreeFullQueueControlResponseAndOriginalQuestionAuthority(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	c := openBoundedTree(t, claude.TreeOptions{MaxPendingRecords: 1}, func(o *claude.Options) {
		o.Limits.EventBuffer = 3
		o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	frame := `{"type":"control_request","session_id":"s","request_id":"original","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question","agent_id":"a","input":{"questions":[{"question":"Which?","options":[{"label":"A","future":null}]}],"future":42}}}`
	treeEmit(t, c, frame)
	p := awaitPermission(t, requests) // independent of Tree.Receive
	// Fill all native event slots. Interrupt reply after marker frames is the
	// deterministic stdout barrier proving dispatch crossed the full queue.
	treeEmit(t, c, `{"type":"marker","seq":1}`)
	treeEmit(t, c, `{"type":"marker","seq":2}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if r := treeReceive(t, c); r.Reason != "fanout-limit" {
		t.Fatal(r)
	} // association+native cannot fit
	awaitSignal(t, c.Done())
	if !errors.Is(context.Cause(p.Context()), claude.ErrResourceLimit) {
		t.Fatal(context.Cause(p.Context()))
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrClosed) {
		t.Fatal(err)
	}
	var raws []string
	for {
		r, err := c.Receive(ctx)
		if err != nil {
			if !errors.Is(err, claude.ErrResourceLimit) {
				t.Fatal(err)
			}
			break
		}
		if r.Native != nil {
			raws = append(raws, string(r.Native.Raw))
		}
	}
	if len(raws) != 3 || raws[0] != frame {
		t.Fatal(raws)
	}
}

func TestTreeQuestionWithHeldChildUsesSoleHandleAndKeepsInput(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedEvents: 1, MaxUnresolvedWait: time.Minute}, func(o *claude.Options) { o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p } })
	treeEmit(t, c, heldChild)
	treeReceive(t, c) // fill held queue
	input := `{"questions":[{"question":"Which?","header":"Format","options":[{"label":"A","future":null}]}],"future":42}`
	frame := `{"type":"control_request","session_id":"s","request_id":"original","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question","agent_id":"a","input":` + input + `}}`
	treeEmit(t, c, frame)
	p := awaitPermission(t, requests)
	for {
		r := treeReceive(t, c)
		if r.Native != nil {
			if string(r.Native.Raw) != frame || r.Source.AgentID != "a" || r.Source.ToolUseID != "question" {
				t.Fatal(r)
			}
			break
		}
	}
	if p.RequestID() != "original" || p.ToolUseID() != "question" || string(p.Input()) != input {
		t.Fatal(p.RequestID(), p.ToolUseID(), string(p.Input()))
	}
	var q claude.AskUserQuestionInput
	if err := json.Unmarshal(p.Input(), &q); err != nil {
		t.Fatal(err)
	}
	answer := "custom"
	updated, err := q.WithAnswers(map[string]claude.QuestionAnswer{"Which?": {Text: &answer}})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Answer(cancelled, claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}); err != nil {
		t.Fatal(err)
	}
	copyHandle := *p
	if err := copyHandle.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); !errors.Is(err, claude.ErrPermissionUsed) {
		t.Fatal(err)
	}
	echo := treeReceive(t, c)
	var wire struct {
		Wire struct {
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior     string
					UpdatedInput json.RawMessage `json:"updatedInput"`
				}
			}
		}
	}
	if err := json.Unmarshal(echo.Native.Raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Wire.Response.RequestID != "original" || wire.Wire.Response.Response.Behavior != "allow" {
		t.Fatal(string(echo.Native.Raw))
	}
	var got, want any
	json.Unmarshal(wire.Wire.Response.Response.UpdatedInput, &got)
	json.Unmarshal(updated, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	// No queued reply is minted from any displayed or copied metadata.
	select {
	case <-requests:
		t.Fatal("second authority")
	default:
	}
}

func TestTreeCancelledQuestionOldEpochAndCloseRace(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	config := func(o *claude.Options) { o.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p } }
	c := openBoundedTree(t, claude.TreeOptions{}, config)
	frame := `{"type":"control_request","request_id":"same","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tool","agent_id":"a","input":{"questions":[]}}}`
	treeEmit(t, c, frame)
	treeReceive(t, c)
	old := awaitPermission(t, requests)
	treeEmit(t, c, `{"type":"control_cancel_request","request_id":"same"}`)
	treeReceive(t, c)
	if err := old.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrPermissionCancelled) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	fresh := openBoundedTree(t, claude.TreeOptions{}, config)
	treeEmit(t, fresh, frame)
	r := treeReceive(t, fresh)
	p := awaitPermission(t, requests)
	if old.Epoch() == p.Epoch() || r.Epoch != p.Epoch() {
		t.Fatal("epoch reused")
	}
	if err := old.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrPermissionCancelled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = fresh.Close() })
	}
	for {
		_, err := fresh.Receive(context.Background())
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
	}
	wg.Wait()
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrClosed) {
		t.Fatal(err)
	}
}

func TestTreeParentResultChildThinkingAndUsageStaySeparate(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{})
	treeFrame(t, c, rootTool)
	frames := []string{
		heldChild,
		`{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"message_delta","usage":{"output_tokens":7},"delta":{"text":"child"}}}`,
		heldChild, // complete snapshot is another native frame, never accumulated
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed","usage":{"total_tokens":10}}`,
		` {"type":"result","subtype":"success","session_id":"s","result":"parent only","usage":{"output_tokens":20},"total_cost_usd":0.25} `,
	}
	for i, frame := range frames {
		_, r := treeFrame(t, c, frame)
		if string(r.Native.Raw) != frame || r.UsageScope != "unknown" {
			t.Fatal(r)
		}
		if i == 4 {
			if r.Scope != "root" || r.Source.AgentID != "" || r.Native.Result.Result != "parent only" {
				t.Fatal(r)
			}
		} else if r.Scope != "child" {
			t.Fatal(r)
		}
	}
}

func TestTreeTransportOverflowWakesPendingInterruptAndQuestion(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	opts := fixtureOptions(t, "overload_controls")
	opts.Limits.EventBuffer = 3
	opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	c, err := openWithTree(context.Background(), opts, claude.TreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	treeReceive(t, c)
	treeEmit(t, c, "permission")
	if r := treeReceive(t, c); r.Native.Control == nil {
		t.Fatal(r)
	}
	p := awaitPermission(t, requests)
	pending := make(chan error, 1)
	go func() { pending <- c.Interrupt(context.Background()) }()
	if r := treeReceive(t, c); r.Native.Type != "control_seen" {
		t.Fatal(r)
	}
	treeEmit(t, c, "fill")
	treeEmit(t, c, "overflow")
	awaitSignal(t, c.Done())
	if !errors.Is(c.Err(), claude.ErrResourceLimit) || !errors.Is(context.Cause(p.Context()), claude.ErrResourceLimit) {
		t.Fatal(c.Err(), context.Cause(p.Context()))
	}
	select {
	case err := <-pending:
		if !errors.Is(err, claude.ErrResourceLimit) || !errors.Is(err, claude.ErrUnknownOutcome) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt was not woken")
	}
	for i := 0; i < 3; i++ {
		r := treeReceive(t, c)
		var wire struct{ Seq int }
		json.Unmarshal(r.Native.Raw, &wire)
		if wire.Seq != i || r.Native.Type != "sequence" {
			t.Fatal(r)
		}
	}
	if r := treeReceive(t, c); r.State != "incomplete" {
		t.Fatal(r)
	}
	if _, err := c.Receive(context.Background()); !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatal(err)
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrClosed) {
		t.Fatal(err)
	}
}

func TestTreeHeldFramesDoNotRelaxDenyOrUnknownControlPolicy(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedWait: time.Minute})
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	for _, subtype := range []string{"can_use_tool", "future_control"} {
		frame := fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":%q,"tool_name":"Read","agent_id":"a","input":{}}}`, subtype, subtype)
		treeEmit(t, c, frame)
		if r := treeReceive(t, c); r.Native == nil || r.Native.Control.RequestID != subtype {
			t.Fatal(r)
		}
		echo := treeReceive(t, c)
		var wire struct {
			Wire struct {
				Response struct {
					Subtype   string
					RequestID string `json:"request_id"`
					Response  struct{ Behavior string }
					Error     string
				}
			}
		}
		if echo.Native == nil {
			t.Fatal(echo)
		}
		if err := json.Unmarshal(echo.Native.Raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Wire.Response.RequestID != subtype {
			t.Fatal(string(echo.Native.Raw))
		}
		if subtype == "can_use_tool" {
			if wire.Wire.Response.Response.Behavior != "deny" {
				t.Fatal(string(echo.Native.Raw))
			}
		} else if wire.Wire.Response.Subtype != "error" || wire.Wire.Response.Error == "" {
			t.Fatal(string(echo.Native.Raw))
		}
	}
}

func TestTreeHeldFrameCloseDoesNotWaitForAssociationDeadline(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedWait: time.Minute})
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	natives := 0
	for {
		r, err := c.Receive(ctx)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
		if r.Native != nil {
			natives++
			if string(r.Native.Raw) != heldChild || r.Scope != "unresolved" {
				t.Fatal(r)
			}
		}
		if r.RunTerminal {
			t.Fatal("close inferred child completion", r)
		}
	}
	awaitSignal(t, closed)
	if natives != 1 {
		t.Fatal(natives)
	}
}

func TestTreeBufferedSourceOrderAcrossUnrelatedUnresolvedSource(t *testing.T) {
	c := openBoundedTree(t, claude.TreeOptions{MaxUnresolvedWait: time.Minute})
	unrelated := `{"type":"assistant","session_id":"s","agent_id":"other","message":{"content":"unresolved"}}`
	treeEmit(t, c, unrelated)
	treeReceive(t, c)
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	// One later same-source native frame supplies the missing root-tool evidence
	// via already observed parent ownership. It must not overtake the early frame,
	// even though an unrelated unresolved frame is at the head of the held queue.
	treeEmit(t, c, rootTool)
	if r := treeReceive(t, c); r.Kind != "SubagentAssociated" || r.Source.TaskID != "a" || r.Scope != "child" {
		t.Fatal(r)
	}
	if r := treeReceive(t, c); r.Native == nil || string(r.Native.Raw) != heldChild {
		t.Fatal(r)
	}
	if r := treeReceive(t, c); r.Native == nil || string(r.Native.Raw) != rootTool {
		t.Fatal(r)
	}
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"later"}}`)
}
