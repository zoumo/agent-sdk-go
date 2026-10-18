// SPDX-License-Identifier: Apache-2.0

package claude

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

	"github.com/stretchr/testify/require"
)

func openBoundedTree(t *testing.T, bounds SubagentOptions, configure ...func(*Options)) *Client {
	t.Helper()
	opts := fixtureOptions(t, "tree")
	for _, f := range configure {
		f(&opts)
	}
	c, err := openWithTree(context.Background(), opts, bounds)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	{
		r := treeReceive(t, c)
		require.EqualValues(t, "unverified", r.State, r)
	}
	return c
}

const heldChild = ` {"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"spawn","message":{"content":[{"type":"text","text":"child"},{"type":"thinking","thinking":"private","signature":"sig"}],"usage":{"output_tokens":7}},"future":null} `
const rootTool = `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`

func (s *treeObservationSuite) TestTreeBufferedSourceResolvesBeforeNativeWithoutReplay() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedWait: time.Second})
	treeEmit(t, c, heldChild)
	first := treeReceive(t, c)
	require.False(t, first.Kind != "SubagentAssociated" || first.Scope != "unresolved", first)
	// The call timeout neither discards the held frame nor stops the process.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	{
		_, err := c.tree.receive(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded, err)
	}
	treeEmit(t, c, rootTool)
	enriched := treeReceive(t, c)
	require.False(t, enriched.Kind != "SubagentAssociated" || enriched.Scope != "child" || enriched.Reason != "identity-enriched", enriched)
	child := treeReceive(t, c)
	require.False(t, child.Kind != "Native" || child.Scope != "child" || child.Source.TaskID != "a" || child.Source.ToolUseID != "spawn" || string(child.Native.Raw) != heldChild || child.Sequence <= enriched.Sequence, child)
	parent := treeReceive(t, c)
	require.False(t, parent.Native == nil || string(parent.Native.Raw) != rootTool, parent)
	treeFrame(t, c, `{"type":"marker"}`) // no replay ahead of the marker
}

func (s *treeObservationSuite) TestTreeUnresolvedExpiryPreservesRawAndOnlyEnrichesLater() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedWait: 30 * time.Millisecond})
	treeEmit(t, c, heldChild)
	treeReceive(t, c) // association
	d := treeReceive(t, c)
	require.False(t, d.Kind != "ObservationState" || d.State != "incomplete" || d.Reason != "unresolved-source" || d.Scope != "unresolved", d)
	r := treeReceive(t, c)
	require.False(t, r.Scope != "unresolved" || r.Reason != "unresolved-source" || string(r.Native.Raw) != heldChild, r)
	ds, _ := treeFrame(t, c, rootTool)
	require.True(t, treeHas(ds, "SubagentAssociated", "identity-enriched"), ds)
	treeFrame(t, c, `{"type":"marker"}`)
	select {
	case <-c.Done():
		require.FailNow(t, "verification failed", c.Err())
	default:
	}
}

func (s *treeObservationSuite) TestTreeUnresolvedCapacityFailsVisiblyWithoutPartialFanout() {
	t := s.T()
	for _, mode := range []string{"count", "bytes", "single-frame"} {
		t.Run(mode, func(t *testing.T) {
			opts := SubagentOptions{MaxUnresolvedWait: time.Minute, MaxUnresolvedEvents: 1}
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
				{
					r := treeReceive(t, c)
					require.EqualValues(t, "SubagentAssociated", r.Kind, r)
				}
				next := `{"type":"assistant","session_id":"s","agent_id":"b","message":{"content":"overflow"}}`
				expected[next] = 1
				treeEmit(t, c, next)
			}
			d := treeReceive(t, c)
			require.False(t, d.State != "incomplete" || d.Reason != "unresolved-source", "partial fanout or missing diagnostic", d)
			<-c.Done()
			require.ErrorIs(t, c.Err(), ErrResourceLimit, c.Err())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var nativeOrder []string
			for {
				r, err := c.tree.receive(ctx)
				if err != nil {
					require.ErrorIs(t, err, ErrResourceLimit, err)
					break
				}
				require.NotEqualValues(t, "SubagentAssociated", r.Kind, "rejected association published", r)
				if r.Native != nil {
					nativeOrder = append(nativeOrder, string(r.Native.Raw))
					expected[string(r.Native.Raw)]--
					require.EqualValues(t, "unresolved", r.Scope, r)
				}
			}
			require.False(t, len(nativeOrder) == 0 || nativeOrder[0] != heldChild, "overflow overtook held native", nativeOrder)
			for frame, n := range expected {
				require.EqualValuesf(t, 0, n, "native delivered other than once: %d %s", n, frame)
			}
		})
	}
}

func (s *treeObservationSuite) TestTreeAtomicFanoutAndHistoryBounds() {
	t := s.T()
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			c := openBoundedTree(t, SubagentOptions{MaxPendingRecords: limit})
			frame := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","run_id":"1"}`
			treeEmit(t, c, frame) // association + run-observed + native = 3
			r := treeReceive(t, c)
			require.EqualValues(t, "fanout-limit", r.Reason, "partial association escaped", r)
			{
				r = treeReceive(t, c)
				require.False(t, r.Native == nil || string(r.Native.Raw) != frame || r.Scope != "unresolved", r)
			}
			for {
				r, err := c.tree.receive(context.Background())
				if err != nil {
					require.ErrorIs(t, err, ErrResourceLimit, err)
					break
				}
				require.NotEqualValues(t, "SubagentAssociated", r.Kind, r)
			}
		})
	}
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxPendingRecords: 1, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b"}}})
	require.False(t, c != nil || !errors.Is(err, ErrResourceLimit), c, err)
}

func TestTreeNewBoundsRejectBeforeStartup(t *testing.T) {
	for _, bounds := range []SubagentOptions{
		{MaxPendingRecords: -1}, {MaxPendingRecords: 131073},
		{MaxUnresolvedEvents: -1}, {MaxUnresolvedEvents: 65537},
		{MaxUnresolvedBytes: -1}, {MaxUnresolvedBytes: 64<<20 + 1},
		{MaxUnresolvedWait: -1}, {MaxUnresolvedWait: time.Minute + 1},
	} {
		opts := fixtureOptions(t, "tree")
		capture := filepath.Join(opts.WorkDir, "capture")
		opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
		c, err := openWithTree(context.Background(), opts, bounds)
		require.False(t, c != nil || err == nil, bounds, c, err)
		{
			_, err := os.Stat(capture)
			require.ErrorIs(t, err, os.ErrNotExist, "started child", err)
		}
	}
}

func (s *treeObservationSuite) TestTreeFullQueueControlResponseAndOriginalQuestionAuthority() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := openBoundedTree(t, SubagentOptions{MaxPendingRecords: 1}, func(o *Options) {
		o.Limits.EventBuffer = 3
		o.PermissionHandler = func(p *PermissionRequest) { requests <- p }
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
	{
		err := c.Interrupt(ctx)
		require.NoError(t, err)
	}
	{
		r := treeReceive(t, c)
		require.EqualValues(t, "fanout-limit", r.Reason, r)
	} // association+native cannot fit
	awaitSignal(t, c.Done())
	require.ErrorIs(t, context.Cause(p.Context()), ErrResourceLimit, context.Cause(p.Context()))
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIs(t, err, ErrClosed, err)
	}
	var raws []string
	for {
		r, err := c.tree.receive(ctx)
		if err != nil {
			require.ErrorIs(t, err, ErrResourceLimit, err)
			break
		}
		if r.Native != nil {
			raws = append(raws, string(r.Native.Raw))
		}
	}
	require.False(t, len(raws) != 3 || raws[0] != frame, raws)
}

func (s *treeObservationSuite) TestTreeQuestionWithHeldChildUsesSoleHandleAndKeepsInput() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedEvents: 1, MaxUnresolvedWait: time.Minute}, func(o *Options) { o.PermissionHandler = func(p *PermissionRequest) { requests <- p } })
	treeEmit(t, c, heldChild)
	treeReceive(t, c) // fill held queue
	input := `{"questions":[{"question":"Which?","header":"Format","options":[{"label":"A","future":null}]}],"future":42}`
	frame := `{"type":"control_request","session_id":"s","request_id":"original","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question","agent_id":"a","input":` + input + `}}`
	treeEmit(t, c, frame)
	p := awaitPermission(t, requests)
	for {
		r := treeReceive(t, c)
		if r.Native != nil {
			require.False(t, string(r.Native.Raw) != frame || r.Source.AgentID != "a" || r.Source.ToolUseID != "question", r)
			break
		}
	}
	require.False(t, p.RequestID() != "original" || p.ToolUseID() != "question" || string(p.Input()) != input, p.RequestID(), p.ToolUseID(), string(p.Input()))
	var q AskUserQuestionInput
	{
		err := json.Unmarshal(p.Input(), &q)
		require.NoError(t, err)
	}
	answer := "custom"
	updated, err := q.WithAnswers(map[string]QuestionAnswer{"Which?": {Text: &answer}})
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := p.Answer(cancelled, PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: updated})
		require.ErrorIs(t, err, context.Canceled, err)
	}
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: updated})
		require.NoError(t, err)
	}
	copyHandle := *p
	{
		err := copyHandle.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.ErrorIs(t, err, ErrPermissionUsed, err)
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
	{
		err := json.Unmarshal(echo.Native.Raw, &wire)
		require.NoError(t, err)
	}
	require.False(t, wire.Wire.Response.RequestID != "original" || wire.Wire.Response.Response.Behavior != "allow", string(echo.Native.Raw))
	var got, want any
	json.Unmarshal(wire.Wire.Response.Response.UpdatedInput, &got)
	json.Unmarshal(updated, &want)
	require.True(t, reflect.DeepEqual(got, want), got, want)
	// No queued reply is minted from any displayed or copied metadata.
	select {
	case <-requests:
		require.FailNow(t, "verification failed", "second authority")
	default:
	}
}

func (s *treeObservationSuite) TestTreeCancelledQuestionOldEpochAndCloseRace() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	config := func(o *Options) { o.PermissionHandler = func(p *PermissionRequest) { requests <- p } }
	c := openBoundedTree(t, SubagentOptions{}, config)
	frame := `{"type":"control_request","request_id":"same","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tool","agent_id":"a","input":{"questions":[]}}}`
	treeEmit(t, c, frame)
	treeReceive(t, c)
	old := awaitPermission(t, requests)
	treeEmit(t, c, `{"type":"control_cancel_request","request_id":"same"}`)
	treeReceive(t, c)
	{
		err := old.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIs(t, err, ErrPermissionCancelled, err)
	}
	{
		err := c.Close()
		require.NoError(t, err)
	}
	fresh := openBoundedTree(t, SubagentOptions{}, config)
	treeEmit(t, fresh, frame)
	r := treeReceive(t, fresh)
	p := awaitPermission(t, requests)
	require.False(t, old.Epoch() == p.Epoch() || r.Epoch != p.Epoch(), "epoch reused")
	{
		err := old.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIs(t, err, ErrPermissionCancelled, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = fresh.Close() })
	}
	for {
		_, err := fresh.tree.receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF, err)
			break
		}
	}
	wg.Wait()
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIs(t, err, ErrClosed, err)
	}
}

func (s *treeObservationSuite) TestTreeParentResultChildThinkingAndUsageStaySeparate() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{})
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
		require.False(t, string(r.Native.Raw) != frame || r.UsageScope != "unknown", r)
		if i == 4 {
			require.False(t, r.Scope != "root" || r.Source.AgentID != "" || r.Native.Result.Result != "parent only", r)
		} else {
			require.EqualValues(t, "child", r.Scope, r)
		}
	}
}

func (s *treeObservationSuite) TestTreeTransportOverflowWakesPendingInterruptAndQuestion() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	opts := fixtureOptions(t, "overload_controls")
	opts.Limits.EventBuffer = 3
	opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	c, err := openWithTree(context.Background(), opts, SubagentOptions{})
	require.NoError(t, err)
	defer c.Close()
	treeReceive(t, c)
	treeEmit(t, c, "permission")
	{
		r := treeReceive(t, c)
		require.NotNil(t, r.Native.Control, r)
	}
	p := awaitPermission(t, requests)
	pending := make(chan error, 1)
	go func() { pending <- c.Interrupt(context.Background()) }()
	{
		r := treeReceive(t, c)
		require.EqualValues(t, "control_seen", r.Native.Type, r)
	}
	treeEmit(t, c, "fill")
	treeEmit(t, c, "overflow")
	awaitSignal(t, c.Done())
	require.False(t, !errors.Is(c.Err(), ErrResourceLimit) || !errors.Is(context.Cause(p.Context()), ErrResourceLimit), c.Err(), context.Cause(p.Context()))
	select {
	case err := <-pending:
		require.False(t, !errors.Is(err, ErrResourceLimit) || !errors.Is(err, ErrUnknownOutcome), err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "verification failed", "interrupt was not woken")
	}
	for i := 0; i < 3; i++ {
		r := treeReceive(t, c)
		var wire struct{ Seq int }
		json.Unmarshal(r.Native.Raw, &wire)
		require.False(t, wire.Seq != i || r.Native.Type != "sequence", r)
	}
	{
		r := treeReceive(t, c)
		require.EqualValues(t, "incomplete", r.State, r)
	}
	{
		_, err := c.tree.receive(context.Background())
		require.ErrorIs(t, err, ErrResourceLimit, err)
	}
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIs(t, err, ErrClosed, err)
	}
}

func (s *treeObservationSuite) TestTreeHeldFramesDoNotRelaxDenyOrUnknownControlPolicy() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedWait: time.Minute})
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	for _, subtype := range []string{"can_use_tool", "future_control"} {
		frame := fmt.Sprintf(`{"type":"control_request","request_id":%q,"request":{"subtype":%q,"tool_name":"Read","agent_id":"a","input":{}}}`, subtype, subtype)
		treeEmit(t, c, frame)
		{
			r := treeReceive(t, c)
			require.False(t, r.Native == nil || r.Native.Control.RequestID != subtype, r)
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
		require.NotNil(t, echo.Native, echo)
		{
			err := json.Unmarshal(echo.Native.Raw, &wire)
			require.NoError(t, err)
		}
		require.EqualValues(t, subtype, wire.Wire.Response.RequestID, string(echo.Native.Raw))
		if subtype == "can_use_tool" {
			require.EqualValues(t, "deny", wire.Wire.Response.Response.Behavior, string(echo.Native.Raw))
		} else {
			require.False(t, wire.Wire.Response.Subtype != "error" || wire.Wire.Response.Error == "", string(echo.Native.Raw))
		}
	}
}

func (s *treeObservationSuite) TestTreeHeldFrameCloseDoesNotWaitForAssociationDeadline() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedWait: time.Minute})
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	natives := 0
	for {
		r, err := c.tree.receive(ctx)
		if err != nil {
			require.ErrorIs(t, err, io.EOF, err)
			break
		}
		if r.Native != nil {
			natives++
			require.False(t, string(r.Native.Raw) != heldChild || r.Scope != "unresolved", r)
		}
		require.False(t, r.RunTerminal, "close inferred child completion", r)
	}
	awaitSignal(t, closed)
	require.EqualValues(t, 1, natives, natives)
}

func (s *treeObservationSuite) TestTreeBufferedSourceOrderAcrossUnrelatedUnresolvedSource() {
	t := s.T()
	c := openBoundedTree(t, SubagentOptions{MaxUnresolvedWait: time.Minute})
	unrelated := `{"type":"assistant","session_id":"s","agent_id":"other","message":{"content":"unresolved"}}`
	treeEmit(t, c, unrelated)
	treeReceive(t, c)
	treeEmit(t, c, heldChild)
	treeReceive(t, c)
	// One later same-source native frame supplies the missing root-tool evidence
	// via already observed parent ownership. It must not overtake the early frame,
	// even though an unrelated unresolved frame is at the head of the held queue.
	treeEmit(t, c, rootTool)
	{
		r := treeReceive(t, c)
		require.False(t, r.Kind != "SubagentAssociated" || r.Source.TaskID != "a" || r.Scope != "child", r)
	}
	{
		r := treeReceive(t, c)
		require.False(t, r.Native == nil || string(r.Native.Raw) != heldChild, r)
	}
	{
		r := treeReceive(t, c)
		require.False(t, r.Native == nil || string(r.Native.Raw) != rootTool, r)
	}
	treeFrame(t, c, `{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"later"}}`)
}
