// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const creationInit = `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258","tools":["Agent","Task","SendMessage"]}`

func creationRequest(tool, name, input, owner string) string {
	source := ""
	if owner != "" {
		source = `,"parent_tool_use_id":"outer"`
		if owner == "other" {
			source = `,"parent_tool_use_id":"unknown"`
		}
	}
	return fmt.Sprintf(`{"type":"assistant","session_id":"s"%s,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, source, tool, name, input)
}

const creationInput = `{"description":"inspect local code","prompt":"Inspect the local source"}`

func creationTask(task, tool, background string) string {
	extra := ""
	if background != "" {
		extra += `,"is_backgrounded":` + background
	}

	return fmt.Sprintf(`{"type":"system","subtype":"task_started","session_id":"s","task_id":%q,"task_type":"local_agent","tool_use_id":%q%s}`, task, tool, extra)
}
func creationResult(tool, agent, status string) string {
	output := fmt.Sprintf(`{"status":%q,"agentId":%q,"description":"inspect local source","prompt":"Inspect source","outputFile":"/synthetic/child.output"}`, status, agent)
	if status == "completed" {
		output = fmt.Sprintf(`{"status":%q,"agentId":%q,"content":[{"type":"text","text":"inspection complete"}],"prompt":"Inspect source","totalToolUseCount":0,"totalDurationMs":1,"totalTokens":1,"usage":{"input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"server_tool_use":null,"service_tier":null,"cache_creation":null}}`, status, agent)
	}
	return fmt.Sprintf(`{"type":"user","session_id":"s","tool_use_result":%s,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"done"}]}}`, output, tool)
}

// A sentinel lets each test assert the absence of delayed Created without
// timing guesses, while checking every original native byte in arrival order.
func creationFrames(t *testing.T, c *Client, frames ...string) []relationshipRecord {
	t.Helper()
	var got []relationshipRecord
	for _, frame := range frames {
		treeEmit(t, c, frame)
		for {
			r := treeReceive(t, c)
			got = append(got, r)
			if r.Kind == "Native" {
				require.EqualValuesf(t, frame, string(r.Native.Raw), "Raw changed: %s != %s", r.Native.Raw, frame)
				break
			}
		}
	}
	treeEmit(t, c, `{"type":"creation_sentinel","session_id":"s"}`)
	for {
		r := treeReceive(t, c)
		if r.Kind == "Native" && r.Native.Type == "creation_sentinel" {
			break
		}
		got = append(got, r)
	}
	return got
}
func creations(records []relationshipRecord) []relationshipRecord {
	var result []relationshipRecord
	for _, r := range records {
		if r.Kind == "SubagentCreated" {
			result = append(result, r)
		}
	}
	return result
}
func (s *treeObservationSuite) TestTreeCreatedForegroundAndLateBackground() {
	t := s.T()
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			c := openTreeFixture(t)
			initialFrames := []string{creationInit, creationRequest("call", "Agent", creationInput, "")}
			status := "async_launched"
			if !background {
				initialFrames = append(initialFrames, creationTask("child", "call", "false"))
				status = "completed"
			}
			// A root tool receipt belongs before that root turn's result.
			initialFrames = append(initialFrames, creationResult("call", "child", status))
			initial := creationFrames(t, c, initialFrames...)
			want := 1
			if background {
				want = 0
			}
			require.EqualValues(t, want, len(creations(initial)), initial)
			// Native child output continues after parent result, before background proof.
			middle := creationFrames(t, c, `{"type":"result","subtype":"success","session_id":"s","result":"parent done"}`, `{"type":"assistant","session_id":"s","parent_tool_use_id":"call","message":{"role":"assistant","content":"child continues"}}`)
			require.EqualValues(t, 0, len(creations(middle)), middle)
			lateFrame := `{"type":"system","subtype":"task_updated","session_id":"s","task_id":"child","patch":{"status":"running"}}`
			if background {
				lateFrame = creationTask("child", "call", "true")
			}
			late := creationFrames(t, c, lateFrame)
			if background {
				require.EqualValues(t, 1, len(creations(late)), late)
			} else {
				require.EqualValues(t, 0, len(creations(late)), late)
			}
			all := append(append(initial, middle...), late...)
			created := creations(all)[0]
			require.False(t, created.Provenance != "live" || created.Source.TaskID != "child" || created.Source.RunID != "" || created.RunScope != "legacy-unknown" || created.Parent == nil || created.Parent.SessionID != "s" || created.Scope != "child" || created.Native != nil || created.Evidence == "", created)
			associated := false
			for _, r := range all {
				if r.Kind == "SubagentAssociated" && r.Source.TaskID == "child" {
					associated = true
				}
				require.False(t, r.Kind == "SubagentCreated" && !associated, "Created before association")
			}
		})
	}
}

// Synthetic permutations test matcher robustness, not legal native producer order.
func (s *treeObservationSuite) TestTreeCreatedCorroborationOrders() {
	t := s.T()
	req := creationRequest("call", "Agent", creationInput, "")
	task := creationTask("child", "call", "true")
	result := creationResult("call", "child", "completed")
	for i, frames := range [][]string{{req, task, result}, {req, result, task}, {task, req, result}, {task, result, req}, {result, req, task}, {result, task, req}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := openTreeFixture(t)
			got := creationFrames(t, c, append([]string{creationInit}, frames...)...)
			require.EqualValues(t, 1, len(creations(got)), got)
		})
	}
}
func (s *treeObservationSuite) TestTreeCreatedConservativeGates() {
	t := s.T()
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
			c := openTreeFixture(t)
			got := creationFrames(t, c, frames...)
			want := 0
			if name == "foreground" {
				want = 1
			}
			require.EqualValuesf(t, want, len(creations(got)), "Created=%d want %d: %+v", len(creations(got)), want, got)
		})
	}
}
func (s *treeObservationSuite) TestTreeCreatedHistoryAndRootResume() {
	t := s.T()
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			opts := fixtureOptions(t, "tree")
			bounds := SubagentOptions{MaxUnresolvedWait: time.Nanosecond}
			if history {
				bounds.History = []SubagentHistory{{SessionID: "s", TaskID: "old"}}
			} else {
				opts.ResumeID = "s"
			}
			c, err := openWithTree(context.Background(), opts, bounds)
			require.NoError(t, err)
			defer c.Abort()
			// Drain startup and seed/gap records through the init frame.
			treeReceive(t, c)
			got := creationFrames(t, c, creationInit, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false"), creationResult("call", "child", "completed"))
			require.EqualValues(t, 0, len(creations(got)), got)
		})
	}
}
func (s *treeObservationSuite) TestTreeCreatedNestedParallelAndEntityDedup() {
	t := s.T()
	c := s.client
	got := creationFrames(t, c, creationInit,
		creationRequest("outer", "Agent", creationInput, ""), creationTask("parent", "outer", "false"),
		creationRequest("nested", "Task", creationInput, "parent"), creationTask("grandchild", "nested", "false"),
		creationRequest("parallel", "Agent", creationInput, ""), creationTask("sibling", "parallel", "false"))
	require.EqualValues(t, 3, len(creations(got)), got)
	for _, r := range creations(got) {
		require.False(t, r.Source.TaskID == "grandchild" && (r.Parent == nil || r.Parent.TaskID != "parent"), r)
	}
	// Same names have no role in identity. Later work and tool references cannot
	// create the same entity twice; no UUID is needed.
	later := creationFrames(t, c, `{"type":"assistant","session_id":"s","parent_tool_use_id":"nested","message":{"role":"assistant","content":"enrich alias"}}`, creationRequest("again", "Agent", creationInput, "parent"), creationTask("grandchild", "again", "false"), creationResult("again", "grandchild", "completed"))
	require.EqualValues(t, 0, len(creations(later)), later)
}
func (s *treeObservationSuite) TestTreeCreationEvidenceBound() {
	t := s.T()
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxCreationEntries: 1})
	require.NoError(t, err)
	defer c.Abort()
	treeReceive(t, c)
	creationFrames(t, c, creationInit, creationRequest("one", "Agent", creationInput, ""))
	treeEmit(t, c, creationRequest("two", "Agent", creationInput, ""))
	var limit bool
	for i := 0; i < 4; i++ {
		r := treeReceive(t, c)
		if r.Reason == "subagent-resource-limit" {
			limit = true
		}
		if r.Kind == "Native" {
			require.True(t, limit, r)
			return
		}
	}
	require.FailNow(t, "verification failed", "missing bounded failure")
}

// Existing codecs preserve additional raw identity fields even though 2.1.258
// does not promise to emit them. Merge must retain the entity's creation bit.
func (s *treeObservationSuite) TestTreeCreatedSurvivesAliasMerge() {
	t := s.T()
	c := s.client
	got := creationFrames(t, c, creationInit,
		`{"type":"future_source","session_id":"s","agent_id":"child"}`,
		creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false"))
	require.EqualValues(t, 1, len(creations(got)), got)
	later := creationFrames(t, c,
		`{"type":"assistant","session_id":"s","agent_id":"child","parent_tool_use_id":"call","message":{"content":"bridge"}}`,
		creationTask("child", "call", "false"), creationResult("call", "child", "completed"))
	merged := false
	for _, r := range later {
		if r.Reason == "identity-merged" {
			merged = true
		}
	}
	require.False(t, !merged || len(creations(later)) != 0, later)
}
func (s *treeObservationSuite) TestTreeCreatedUUIDCapacityDoesNotEvictEntity() {
	t := s.T()
	opts := fixtureOptions(t, "tree")
	c, err := openWithTree(context.Background(), opts, SubagentOptions{MaxDedupEntries: 1})
	require.NoError(t, err)
	defer c.Abort()
	treeReceive(t, c)
	got := creationFrames(t, c, creationInit, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false"),
		`{"type":"assistant","session_id":"s","uuid":"one","message":{"content":"first"}}`)
	require.EqualValues(t, 1, len(creations(got)), got)
	treeEmit(t, c, strings.Replace(creationTask("child", "call", "false"), `"type":"system"`, `"uuid":"two","type":"system"`, 1))
	limited := false
	for i := 0; i < 4; i++ {
		r := treeReceive(t, c)
		require.NotEqualValues(t, "SubagentCreated", r.Kind, "cache exhaustion recreated entity")
		if r.Reason == "subagent-resource-limit" {
			limited = true
		}
		if r.Kind == "Native" {
			require.True(t, limited, r)
			return
		}
	}
	require.FailNow(t, "verification failed", "no bounded failure")
}
func (s *treeObservationSuite) TestTreeCreatedDuplicateInitBoundary() {
	t := s.T()
	c := s.client
	init := strings.Replace(creationInit, `"type":"system"`, `"uuid":"init","type":"system"`, 1)
	creationFrames(t, c, init, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "true"))
	// The duplicate init has no Native delivery, but must invalidate trust.
	treeEmit(t, c, init)
	got := creationFrames(t, c, creationResult("call", "child", "completed"))
	require.EqualValues(t, 0, len(creations(got)), got)
}

func (s *treeObservationSuite) TestTreeCreatedNestedBackgroundCaller() {
	t := s.T()
	for _, matching := range []bool{true, false} {
		t.Run(fmt.Sprint(matching), func(t *testing.T) {
			c := openTreeFixture(t)
			creationFrames(t, c, creationInit, creationRequest("outer", "Agent", creationInput, ""), creationTask("parent", "outer", "false"), creationRequest("nested", "Agent", creationInput, "parent"), creationTask("grandchild", "nested", "true"))
			result := creationResult("nested", "grandchild", "async_launched")
			if matching {
				result = strings.Replace(result, `"session_id":"s"`, `"session_id":"s","parent_tool_use_id":"outer"`, 1)
			}
			got := creationFrames(t, c, result)
			want := 0
			if matching {
				want = 1
			}
			require.EqualValues(t, want, len(creations(got)), got)
		})
	}
}
