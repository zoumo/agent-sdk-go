// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const nativeChildSnapshot = ` {"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"spawn","message":{"content":[{"type":"text","text":"child"},{"type":"thinking","thinking":"private","signature":"sig"}],"usage":{"output_tokens":7}},"future":null} `
const nativeRootTool = `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`

func TestSessionNativeUsageTerminalAndSnapshotsStaySeparate(t *testing.T) {
	s := openSessionFixture(t)
	frames := []string{creationInit, nativeRootTool, creationTask("a", "spawn", "false"),
		nativeChildSnapshot,
		`{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"message_delta","usage":{"output_tokens":7},"delta":{"text":"child"}}}`,
		nativeChildSnapshot,
		`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed","usage":{"total_tokens":10}}`,
		` {"type":"result","subtype":"success","session_id":"s","result":"parent only","usage":{"output_tokens":20},"total_cost_usd":0.25} `,
		`{"type":"system","subtype":"session_state_changed","session_id":"s","state":"idle","future":null}`,
		`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[{"task_id":"a","run_id":null,"task_type":"local_agent","ambient":false,"future":42}]}`,
		nativeChildSnapshot,
	}
	for i, frame := range frames {
		submitFrame(t, s, frame)
		r := sessionNative(t, s)
		require.Equal(t, frame, string(r.Raw))
		require.Equal(t, frame, string(r.Event.Raw))
		if i == 3 || i == 5 || i == 10 {
			require.Equal(t, "a", r.Source.TaskID)
			require.Equal(t, "spawn", r.Source.ToolUseID)
			require.NotNil(t, r.Parent)
			require.Equal(t, "s", r.Parent.SessionID)
			require.Equal(t, "child", r.Event.Assistant.Content.Blocks[0].Text.Text)
			require.Equal(t, "private", r.Event.Assistant.Content.Blocks[1].Thinking.Thinking)
			require.Equal(t, "sig", r.Event.Assistant.Content.Blocks[1].Thinking.Signature)
			r.Parent.SessionID = "caller-mutated"
		}
		if i == 7 {
			require.Empty(t, r.Source.AgentID)
			require.Empty(t, r.Source.TaskID)
			require.Equal(t, "parent only", r.Event.Result.Result)
			require.NotNil(t, r.Event.Result.TotalCostUSD)
			require.Equal(t, 0.25, *r.Event.Result.TotalCostUSD)
		}
	}
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	for {
		r, err := s.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		require.Nil(t, r.Event, "invented aggregate output")
	}
}

func TestSessionNativeTaskTerminalMatrixAndNoRunOrdering(t *testing.T) {
	s := openSessionFixture(t)
	for _, frame := range []string{creationInit, creationTask("a", "spawn", "false")} {
		submitFrame(t, s, frame)
		sessionNative(t, s)
	}
	for _, kind := range []string{"task_updated", "task_notification"} {
		statuses := []string{"pending", "running", "paused", "future", "completed", "failed", "killed", "stopped"}
		for _, status := range statuses {
			frame := fmt.Sprintf(`{"type":"system","subtype":%q,"session_id":"s","task_id":"a","run_id":"001","status":%q,"patch":{"status":%q,"is_backgrounded":true,"future":null}}`, kind, status, status)
			submitFrame(t, s, frame)
			r := sessionNative(t, s)
			require.Equal(t, frame, string(r.Raw))
			require.Equal(t, "a", r.Source.TaskID)
			require.Equal(t, "001", *r.Event.System.RunID)
		}
	}
	// Repeated partial/complete, UUID-less streams and older lexical run IDs all survive.
	for _, frame := range []string{
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn","run_id":"z"}`,
		`{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"a"}`,
		`{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"message_delta","usage":{"output_tokens":1}}}`,
		`{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"message_delta","usage":{"output_tokens":1}}}`,
	} {
		submitFrame(t, s, frame)
		require.Equal(t, frame, string(sessionNative(t, s).Raw))
	}
}

func TestSessionQuarantineByteBoundsAndSnapshotCopies(t *testing.T) {
	for _, kind := range []string{"bytes", "single-frame"} {
		t.Run(kind, func(t *testing.T) {
			limit := len(nativeChildSnapshot)
			if kind == "single-frame" {
				limit--
			}
			s := openSessionFixture(t, func(o *SessionOptions) {
				o.Subagents.MaxUnresolvedEvents = 10
				o.Subagents.MaxUnresolvedBytes = limit
				o.Subagents.MaxUnresolvedWait = time.Minute
			})
			submitFrame(t, s, nativeChildSnapshot)
			second := `{"type":"future_event","session_id":"s","agent_id":"b","message":{"content":"overflow"}}`
			if kind == "bytes" {
				submitFrame(t, s, second)
			}
			submitFrame(t, s, creationInit)
			sessionNative(t, s)
			snap := s.Unattributed()
			require.Equal(t, limit, snap.MaxBytes)
			require.Equal(t, time.Minute, snap.Retention)
			require.EqualValues(t, 1, snap.Dropped)
			require.True(t, snap.CoverageGap)
			if kind == "single-frame" {
				require.Empty(t, snap.Records)
				require.Zero(t, snap.Bytes)
			} else {
				require.Len(t, snap.Records, 1)
				require.Equal(t, second, string(snap.Records[0].Event.Raw))
				snap.Records[0].Event.Raw[0] = '!'
				snap.Records[0].Event.Type = "mutated"
				again := s.Unattributed()
				require.Equal(t, second, string(again.Records[0].Event.Raw))
				require.Equal(t, "future_event", again.Records[0].Event.Type)
			}
			require.NoError(t, s.Err())
		})
	}
	// Exact Raw survives active expiry evidence without being inserted into the live FIFO.
	s := openSessionFixture(t, func(o *SessionOptions) { o.Subagents.MaxUnresolvedWait = 30 * time.Millisecond })
	submitFrame(t, s, nativeChildSnapshot)
	submitFrame(t, s, creationInit)
	sessionNative(t, s)
	snap := s.Unattributed()
	require.Len(t, snap.Records, 1)
	require.Equal(t, nativeChildSnapshot, string(snap.Records[0].Event.Raw))
	require.Eventually(t, func() bool { return s.Unattributed().Expired == 1 }, time.Second, time.Millisecond)
	require.Empty(t, s.Unattributed().Records)
}

func TestSessionActiveObservationBoundsRejectBeforeSpawn(t *testing.T) {
	for _, bounds := range []SubagentOptions{
		{MaxNodes: -1}, {MaxNodes: 65537}, {MaxDepth: -1}, {MaxDepth: 257},
		{MaxUnresolvedEvents: -1}, {MaxUnresolvedEvents: 65537},
		{MaxUnresolvedBytes: -1}, {MaxUnresolvedBytes: 64<<20 + 1},
		{MaxUnresolvedWait: -1}, {MaxUnresolvedWait: time.Minute + 1},
		{MaxNodes: 1, History: []SubagentHistory{{SessionID: "s", TaskID: "a"}, {SessionID: "s", TaskID: "b"}}},
		{History: []SubagentHistory{{SessionID: "", TaskID: "a"}}},
		{History: []SubagentHistory{{SessionID: "s", TaskID: ""}}},
	} {
		o := SessionOptions(fixtureOptions(t, "tree"))
		capture := filepath.Join(o.WorkDir, "must-not-start")
		o.Env = append(o.Env, "CLAUDE_GO_CAPTURE="+capture)
		o.Subagents = bounds
		s, err := Open(context.Background(), o)
		require.Nil(t, s)
		require.Error(t, err)
		_, err = os.Stat(capture)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestSessionMemberOverflowAndToolKnowledgeLimitRetainRaw(t *testing.T) {
	for _, kind := range []string{"member", "tool"} {
		t.Run(kind, func(t *testing.T) {
			s := openSessionFixture(t, func(o *SessionOptions) { o.Subagents.MaxNodes = 1 })
			submitFrame(t, s, creationInit)
			sessionNative(t, s)
			first, second := creationTask("a", "tool-a", ""), creationTask("b", "tool-b", "")
			if kind == "tool" {
				first = creationRequest("tool-a", "Agent", creationInput, "")
				second = creationRequest("tool-b", "Agent", creationInput, "")
			}
			submitFrame(t, s, first)
			require.Equal(t, first, string(sessionNative(t, s).Raw))
			submitFrame(t, s, second)
			require.Equal(t, second, string(sessionNative(t, s).Raw))
			if kind == "member" {
				select {
				case <-s.Done():
				case <-time.After(3 * time.Second):
					t.Fatal("member bound did not terminate")
				}
				require.ErrorIs(t, s.Err(), ErrResourceLimit)
				for {
					_, err := s.Receive(context.Background())
					if err != nil {
						require.ErrorIs(t, err, ErrResourceLimit)
						break
					}
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				found := false
				for !found {
					r, err := s.Receive(ctx)
					require.NoError(t, err)
					found = r.Observation != nil && r.Observation.Reason == "tool-knowledge-limit"
				}
				require.NoError(t, s.Err())
				require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
			}
		})
	}
}

func TestSessionNonAgentFactsAndAliasConflictsNeverFabricateIdentity(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, creationInit)
	sessionNative(t, s)
	for _, frame := range []string{
		`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"bash-tool","name":"Bash","input":{"command":"not executed"}}]}}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"bash-task","task_type":"local_bash","tool_use_id":"bash-tool"}`,
		`{"type":"system","subtype":"hook_started","session_id":"s","task_id":"hook","future":null}`,
		creationResult("bash-tool", "not-a-proof", "failed"),
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"bash-tool","message":{"content":"native output"}}`,
	} {
		submitFrame(t, s, frame)
		r := sessionNative(t, s)
		require.Equal(t, frame, string(r.Raw))
		require.Empty(t, r.Source.AssociationID)
		require.Nil(t, r.Parent)
	}
	for _, frame := range []string{creationTask("a", "tool-a", ""), creationTask("a", "tool-b", ""), creationTask("b", "tool-b", "")} {
		submitFrame(t, s, frame)
		require.Equal(t, frame, string(sessionNative(t, s).Raw))
	}
	for _, tc := range []struct{ tool, task string }{{"tool-a", "a"}, {"tool-b", "b"}} {
		frame := fmt.Sprintf(`{"type":"assistant","session_id":"s","parent_tool_use_id":%q,"message":{"content":"unpoisoned mapping"}}`, tc.tool)
		submitFrame(t, s, frame)
		r := sessionNative(t, s)
		require.Equal(t, tc.task, r.Source.TaskID)
		require.Equal(t, frame, string(r.Raw))
	}
}

func TestSessionDuplicateInitPreservesNativeFactsAndVersionRestraint(t *testing.T) {
	s := openSessionFixture(t)
	for _, frame := range []string{creationInit, creationInit, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"future"}`, creationTask("unsupported", "new-tool", "")} {
		submitFrame(t, s, frame)
		r := sessionNative(t, s)
		require.Equal(t, frame, string(r.Raw))
		require.Empty(t, r.Source.AssociationID)
	}
	frame := `{"type":"assistant","session_id":"s","parent_tool_use_id":"new-tool","message":{"content":"no inferred alias"}}`
	submitFrame(t, s, frame)
	r := sessionNative(t, s)
	require.Empty(t, r.Source.TaskID)
	require.Nil(t, r.Parent)
}

func TestSessionConcurrentCloseWithUnreadFactsAndLongQuarantine(t *testing.T) {
	s := openSessionFixture(t, func(o *SessionOptions) { o.Subagents.MaxUnresolvedWait = time.Minute })
	submitFrame(t, s, nativeChildSnapshot)
	submitFrame(t, s, creationInit)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		wg.Go(func() { results <- s.Close(); _ = s.Err() })
	}
	complete := make(chan struct{})
	go func() { wg.Wait(); close(complete) }()
	select {
	case <-complete:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited for quarantine deadline or consumer")
	}
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	natives := 0
	for {
		r, err := s.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if r.Event != nil {
			natives++
			require.Equal(t, creationInit, string(r.Raw))
		}
		require.Nil(t, r.Request)
	}
	require.Equal(t, 1, natives)
	snap := s.Unattributed()
	require.Len(t, snap.Records, 1)
	require.Equal(t, nativeChildSnapshot, string(snap.Records[0].Event.Raw))
}

func TestSessionForwardingBeforeInitializeAckAndFreshEpoch(t *testing.T) {
	for range 2 {
		o := SessionOptions(fixtureOptions(t, "tree_early"))
		s, err := Open(context.Background(), o)
		require.NoError(t, err)
		r, err := s.Receive(context.Background())
		require.NoError(t, err)
		require.Equal(t, ReceivedObservation, r.Kind)
		require.Eventually(t, func() bool { return len(s.Unattributed().Records) == 1 }, 3*time.Second, time.Millisecond)
		require.Equal(t, earlyTreeFrame, string(s.Unattributed().Records[0].Event.Raw))
		id, observed := s.SessionID()
		require.Empty(t, id)
		require.False(t, observed)
		require.NoError(t, s.Close())
	}
}

// The retired creation matcher must not remove any of its native input cases.
// These permutations test lossless delivery, not producer ordering or creation.
func TestSessionFormerCreationInputsRemainNativeFacts(t *testing.T) {
	req := creationRequest("call", "Agent", creationInput, "")
	task := creationTask("child", "call", "true")
	result := creationResult("call", "child", "async_launched")
	tests := map[string][]string{
		"foreground":                   {creationInit, req, creationTask("child", "call", "false")},
		"background alone":             {creationInit, req, task},
		"missing background":           {creationInit, req, creationTask("child", "call", "")},
		"null background":              {creationInit, req, creationTask("child", "call", "null")},
		"old":                          {strings.Replace(creationInit, "2.1.258", "2.1.77", 1), req, task, result},
		"future":                       {strings.Replace(creationInit, "2.1.258", "2.1.259", 1), req, task, result},
		"no version":                   {strings.Replace(creationInit, `"claude_code_version":"2.1.258",`, "", 1), req, task, result},
		"no init":                      {req, task, result},
		"resume key":                   {creationInit, creationRequest("call", "Agent", `{"description":"d","prompt":"p","resume":null}`, ""), task, result},
		"SendMessage":                  {creationInit, creationRequest("call", "SendMessage", creationInput, ""), task, result},
		"mcp tool":                     {creationInit, creationRequest("call", "mcp__x__Agent", creationInput, ""), task, result},
		"remote":                       {creationInit, req, task, creationResult("call", "child", "remote_launched")},
		"request remote":               {creationInit, creationRequest("call", "Agent", `{"description":"d","prompt":"p","isolation":"remote"}`, ""), task, result},
		"wrong background input":       {creationInit, creationRequest("call", "Agent", `{"description":"d","prompt":"p","run_in_background":null}`, ""), task, result},
		"unknown isolation":            {creationInit, creationRequest("call", "Agent", `{"description":"d","prompt":"p","isolation":"future"}`, ""), task, result},
		"source target is not current": {strings.Replace(creationInit, "2.1.258", "2.1.302", 1), req, task, result},
		"missing input":                {creationInit, creationRequest("call", "Agent", `{}`, ""), task, result},
		"wrong type":                   {creationInit, req, strings.Replace(task, "local_agent", "local_bash", 1), result},
		"result identity":              {creationInit, req, task, creationResult("call", "different", "completed")},
		"result error":                 {creationInit, req, task, strings.Replace(result, `"content":"done"`, `"content":"done","is_error":true`, 1)},
		"missing status":               {creationInit, req, task, strings.Replace(result, `"status":"async_launched",`, "", 1)},
		"missing agent":                {creationInit, req, task, strings.Replace(result, `"agentId":"child"`, `"agentId":null`, 1)},
		"missing result":               {creationInit, req, task, strings.Replace(result, `"tool_use_result":`, `"unrelated_result":`, 1)},
		"multiple results":             {creationInit, req, task, strings.Replace(result, `"content":"done"}`, `"content":"done"},{"type":"tool_result","tool_use_id":"other","content":"done"}`, 1)},
		"replay":                       {creationInit, req, strings.Replace(task, `"type":"system"`, `"isReplay":true,"type":"system"`, 1), result},
		"restart":                      {creationInit, req, task, creationInit, result},
		"session mismatch":             {creationInit, req, task, strings.Replace(result, `"session_id":"s"`, `"session_id":"other"`, 1)},
		"task conflict":                {creationInit, req, task, creationTask("other", "call", "true"), result},
		"tool owner conflict":          {creationInit, req, creationRequest("call", "Agent", creationInput, "other"), task, result},
	}
	for name, frames := range tests {
		t.Run(name, func(t *testing.T) {
			s := openSessionFixture(t)
			var owned, foreign []string
			for _, frame := range frames {
				submitFrame(t, s, frame)
				if strings.Contains(frame, `"session_id":"other"`) {
					foreign = append(foreign, frame)
				} else {
					owned = append(owned, frame)
				}
			}
			sentinel := `{"type":"system","subtype":"status","session_id":"s","status":null}`
			submitFrame(t, s, sentinel)
			owned = append(owned, sentinel)
			for _, frame := range owned {
				r := sessionNative(t, s)
				require.Equal(t, frame, string(r.Raw))
				require.Equal(t, frame, string(r.Event.Raw))
				require.Nil(t, r.Request)
			}
			snap := s.Unattributed()
			require.Len(t, snap.Records, len(foreign))
			for i, frame := range foreign {
				require.Equal(t, frame, string(snap.Records[i].Event.Raw))
			}
		})
	}
}
