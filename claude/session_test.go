package claude

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openSessionFixture(t *testing.T, configure ...func(*SessionOptions)) *Session {
	t.Helper()
	o := SessionOptions(fixtureOptions(t, "tree"))
	for _, f := range configure {
		f(&o)
	}
	s, err := Open(context.Background(), o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Abort() })
	return s
}

func sessionNative(t *testing.T, s *Session) Received {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		r, err := s.Receive(ctx)
		require.NoError(t, err)
		if r.Event != nil {
			return r
		}
	}
}
func submitFrame(t *testing.T, s *Session, frame string) {
	t.Helper()
	receipt, err := s.Submit(context.Background(), Input{Text: frame})
	require.NoError(t, err)
	require.True(t, receipt.Written)
}

func TestSessionPreservesArrivalAndDuplicatesWithoutParent(t *testing.T) {
	s := openSessionFixture(t)
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"missing","uuid":"same","message":{"content":"first"}}`,
		`{"type":"result","session_id":"s","uuid":"result","result":"done"}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"missing","uuid":"same","message":{"content":"second"}}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"missing","uuid":"same","message":{"content":"second"}}`,
	}
	for _, f := range frames {
		submitFrame(t, s, f)
	}
	for _, f := range frames {
		require.JSONEq(t, f, string(sessionNative(t, s).Raw))
	}
	id, observed := s.SessionID()
	require.True(t, observed)
	require.Equal(t, "s", id)
	require.NoError(t, s.Err())
}

func TestSessionQuarantineIsBoundedAndNeverBackfills(t *testing.T) {
	s := openSessionFixture(t, func(o *SessionOptions) {
		o.Subagents.MaxUnresolvedEvents = 2
		o.Subagents.MaxUnresolvedBytes = 1024
		o.Subagents.MaxUnresolvedWait = time.Minute
	})
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	for _, f := range []string{
		`{"type":"future","session_id":"s","agent_id":"not-proof","n":1}`,
		`{"type":"future","session_id":"s","agent_id":"a","n":2}`,
		`{"type":"assistant","session_id":"other","agent_id":"a","message":{"content":"foreign"}}`,
		`{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":"member"}}`,
	} {
		submitFrame(t, s, f)
	}
	r := sessionNative(t, s)
	require.Equal(t, "a", r.Source.AgentID)
	require.Empty(t, r.Source.TaskID)
	snap := s.Unattributed()
	require.Len(t, snap.Records, 2)
	require.EqualValues(t, 1, snap.Dropped)
	require.True(t, snap.CoverageGap)
	require.Equal(t, time.Minute, snap.Retention)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	for {
		_, err := s.Receive(ctx)
		if err != nil {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			break
		}
	}
	require.NoError(t, s.Err())
}

func TestSessionCloseDrainsAndAbortDiscards(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "abort"}[abort], func(t *testing.T) {
			s := openSessionFixture(t)
			submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
			sessionNative(t, s)
			for i := 0; i < 20; i++ {
				submitFrame(t, s, `{"type":"result","session_id":"s","uuid":"repeat","result":"done"}`)
			}
			// A native control acknowledgement is a reader barrier, independent of consumption.
			require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
			if abort {
				require.NoError(t, s.Abort())
			} else {
				require.NoError(t, s.Close())
			}
			select {
			case <-s.Done():
			default:
				t.Fatal("cleanup waits for consumer")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			count := 0
			for {
				r, err := s.Receive(ctx)
				if err != nil {
					require.ErrorIs(t, err, io.EOF)
					break
				}
				if r.Event != nil {
					count++
				}
			}
			if abort {
				require.Zero(t, count)
			} else {
				require.Equal(t, 20, count)
			}
		})
	}
}

func TestSessionParentKnowledgeCanArriveAfterChildWithoutReordering(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn","message":{"content":"early"}}`)
	early := sessionNative(t, s)
	require.Nil(t, early.Parent)
	submitFrame(t, s, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	sessionNative(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		r, err := s.Receive(ctx)
		require.NoError(t, err)
		if r.Observation != nil && r.Observation.Reason == "parent-confirmed" {
			require.Equal(t, "a", r.Observation.Source.TaskID)
			require.NotNil(t, r.Observation.Parent)
			break
		}
	}
}

func TestSessionLongMainConversationHasNoUUIDLedgerLimit(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	for i := 0; i < 8300; i++ {
		frame := fmt.Sprintf(`{"type":"assistant","session_id":"s","uuid":"u%d","message":{"content":"text"}}`, i)
		submitFrame(t, s, frame)
	}
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	for i := 0; i < 8300; i++ {
		require.Equal(t, fmt.Sprintf("u%d", i), sessionNative(t, s).Event.UUID)
	}
	require.NoError(t, s.Err())
}

func TestSessionDepthRejectionDoesNotRejectMemberOrSortRuns(t *testing.T) {
	s := openSessionFixture(t, func(o *SessionOptions) { o.Subagents.MaxDepth = 1 })
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`,
		`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn-a","name":"Agent","input":{}}]}}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn-a","run_id":"z"}`,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn-a","message":{"content":[{"type":"tool_use","id":"spawn-b","name":"Agent","input":{}}]}}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"spawn-b","run_id":"a"}`,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn-b","message":{"content":"still observed"}}`,
		`{"type":"system","subtype":"task_progress","session_id":"s","task_id":"a","run_id":"a"}`,
	}
	for _, f := range frames {
		submitFrame(t, s, f)
	}
	for _, f := range frames {
		require.JSONEq(t, f, string(sessionNative(t, s).Raw))
	}
	require.NoError(t, s.Err())
}

func TestSessionWaitCancellationAndQuarantineExpiry(t *testing.T) {
	for _, policy := range []ChildDetailPolicy{ChildDetailPreserve, ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) {
			s := openSessionFixture(t, func(o *SessionOptions) {
				o.Subagents.MaxUnresolvedWait = time.Millisecond
				o.ChildDetailPolicy = policy
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := s.Receive(ctx)
			require.ErrorIs(t, err, context.Canceled)
			submitFrame(t, s, `{"type":"future","session_id":"foreign"}`)
			submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
			sessionNative(t, s)
			// Snapshot reads do not expire material; only the active retention timer can
			// advance Expired while the native peer is idle.
			require.Eventually(t, func() bool { return s.Unattributed().Expired == 1 }, time.Second, time.Millisecond)
			require.Empty(t, s.Unattributed().Records)
			require.True(t, s.Unattributed().CoverageGap)

		})
	}
}

func TestSessionForeignHistoryCannotSupplyLocalParentAlias(t *testing.T) {
	s := openSessionFixture(t, func(o *SessionOptions) { o.Subagents.History = []SubagentHistory{{SessionID: "foreign", TaskID: "a"}} })
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn"}`)
	r := sessionNative(t, s)
	require.Equal(t, "s", r.Source.SessionID)
	require.Equal(t, "spawn", r.Source.ToolUseID)
}

func TestSessionFaultDrainsAcceptedFactsAndKeepsFirstCause(t *testing.T) {
	for _, policy := range []ChildDetailPolicy{ChildDetailPreserve, ChildDetailSuppress} {
		t.Run(string(policy), func(t *testing.T) {
			s := openSessionFixture(t, func(o *SessionOptions) { o.ChildDetailPolicy = policy })
			submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
			sessionNative(t, s)
			submitFrame(t, s, `{"type":"result","session_id":"s","result":"accepted"}`)
			// Valid JSON with an invalid native message shape faults the SDK decoder.
			submitFrame(t, s, `{"type":"assistant","session_id":"s"}`)
			select {
			case <-s.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("fault waited for Receive")
			}
			cause := s.Err()
			require.ErrorIs(t, cause, ErrProtocol)
			require.Equal(t, "accepted", sessionNative(t, s).Event.Result.Result)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := s.Receive(ctx)
			require.Equal(t, cause, err)
			_ = s.Close()
			require.Equal(t, cause, s.Err())

		})
	}
}

func TestSessionRejectsCyclesConflictsAndToolAliasesWithoutLosingFacts(t *testing.T) {
	s := openSessionFixture(t)
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"tool-a"}`,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"tool-a","message":{"content":[{"type":"tool_use","id":"tool-b","name":"Agent","input":{}}]}}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"tool-b"}`,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"tool-b","message":{"content":[{"type":"tool_use","id":"tool-a","name":"Agent","input":{}}]}}`,
		`{"type":"system","subtype":"task_started","session_id":"s","task_id":"c","task_type":"local_agent","tool_use_id":"tool-b"}`,
		`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"tool-b","name":"Agent","input":{}}]}}`,
	}
	for _, f := range frames {
		submitFrame(t, s, f)
	}
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	var reasons []string
	var raw []string
	for {
		r, err := s.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if r.Event != nil {
			raw = append(raw, string(r.Raw))
		}
		if r.Observation != nil {
			reasons = append(reasons, r.Observation.Reason)
		}
	}
	require.Len(t, raw, len(frames))
	for i, f := range frames {
		require.JSONEq(t, f, raw[i])
	}
	require.Contains(t, reasons, "tool-alias-conflict")
	require.Contains(t, reasons, "tool-owner-conflict")
	require.Contains(t, reasons, "parent-knowledge-rejected: lineage: cycle")
}

func TestSessionUnknownEnvelopeCannotBorrowMemberIDAndStreamUsesVerifiedAlias(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"system","subtype":"task_started","session_id":"s","task_id":"native-task","task_type":"local_agent","tool_use_id":"spawn"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"assistant","session_id":"s","agent_id":"a","parent_tool_use_id":"spawn","message":{"content":"member"}}`)
	member := sessionNative(t, s)
	require.Equal(t, "native-task", member.Source.TaskID)
	require.Equal(t, "a", member.Source.AgentID)
	submitFrame(t, s, `{"type":"future","session_id":"s","agent_id":"a","target":"other"}`)
	submitFrame(t, s, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"next"}}}`)
	r := sessionNative(t, s)
	require.Equal(t, "stream_event", r.Event.Type)
	require.Empty(t, r.Source.AgentID)
	require.Equal(t, "native-task", r.Source.TaskID)
	snap := s.Unattributed()
	require.Len(t, snap.Records, 1)
	require.Equal(t, "future", snap.Records[0].Event.Type)
}

func TestSessionUnknownIdentityExtensionsDoNotMergeTaskOrParent(t *testing.T) {
	s := openSessionFixture(t)
	id, observed := s.SessionID()
	require.False(t, observed)
	require.Empty(t, id)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	message := `{"type":"assistant","session_id":"s","agent_id":"same","message":{"content":"unknown identity"},"extra":null}`
	submitFrame(t, s, message)
	m := sessionNative(t, s)
	require.JSONEq(t, message, string(m.Raw))
	require.Equal(t, "same", m.Source.AgentID)
	require.Empty(t, m.Source.TaskID)
	require.Nil(t, m.Parent)
	task := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"same","task_type":"local_agent","parent_task_id":"invented","run_id":"future"}`
	submitFrame(t, s, task)
	r := sessionNative(t, s)
	require.JSONEq(t, task, string(r.Raw))
	require.Equal(t, "same", r.Source.TaskID)
	require.Empty(t, r.Source.AgentID)
	require.Nil(t, r.Parent)
}

func TestSessionUnsupportedVersionPreservesFactsWithoutRelationshipClaims(t *testing.T) {
	for _, version := range []string{"", "future"} {
		t.Run(version, func(t *testing.T) {
			s := openSessionFixture(t)
			submitFrame(t, s, fmt.Sprintf(`{"type":"system","subtype":"init","session_id":"s","claude_code_version":%q}`, version))
			sessionNative(t, s)
			submitFrame(t, s, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
			sessionNative(t, s)
			f := `{"type":"system","subtype":"task_started","session_id":"s","task_id":"task","task_type":"local_agent","tool_use_id":"spawn"}`
			submitFrame(t, s, f)
			r := sessionNative(t, s)
			require.JSONEq(t, f, string(r.Raw))
			require.Nil(t, r.Parent)
			require.Empty(t, r.Source.AssociationID)
			require.NoError(t, s.Err())
		})
	}
}
