package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestSessionDiscoveryExistingNestedRecords(t *testing.T) {
	f := startFake(t, "session-config-discovery", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	page, err := s.ListSubagents(context.Background(), codex.SubagentQuery{})
	require.NoError(t, err)
	require.Len(t, page.Pages, 2)
	require.Len(t, page.Pages[0].Data, 1)
	require.Len(t, page.Pages[1].Data, 1)
	assert.Equal(t, "old-child", page.Pages[0].Data[0].ID)
	assert.Equal(t, "old-grandchild", page.Pages[1].Data[0].ID)
	assert.Equal(t, "old-child", page.Pages[1].Data[0].ParentThreadID.ValueOr(""))
	assert.False(t, page.Pages[0].Data[0].ParentThreadID.IsSet())
	assert.NotNil(t, page.Pages[1].Data[0].Status.NotLoaded)
	assert.True(t, page.Coverage.EnumerationComplete)
	assert.Equal(t, 2, page.Coverage.PagesCompleted)
	assert.True(t, page.Coverage.EndReached)
	assert.False(t, page.Coverage.Archived)
	assert.Equal(t, "main-1", page.Coverage.Requested.AncestorThreadID.ValueOr(""))
	raw, err := page.Pages[0].MarshalJSON()
	require.NoError(t, err)
	require.Contains(t, string(raw), `"pageFuture":`)
	require.Contains(t, string(raw), `"recordFuture":`)
	var calls []fakeEnvelope
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/list" {
			calls = append(calls, frame)
		}
	}
	require.Len(t, calls, 2)
	require.JSONEq(t, `{"ancestorThreadId":"main-1","sourceKinds":["subAgent","subAgentReview","subAgentCompact","subAgentThreadSpawn","subAgentOther"]}`, string(calls[0].Params))
}

func TestSessionDiscoveryCoverageAndNativeFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		query     codex.SubagentQuery
		pages     int
		completed int
		complete  bool
		cause     error
	}{
		{"empty-page", codex.SubagentQuery{Cursor: types.Some("empty")}, 2, 2, false, nil},
		{"empty-end", codex.SubagentQuery{Cursor: types.Some("empty-end")}, 1, 1, false, nil},
		{"stuck", codex.SubagentQuery{Cursor: types.Some("stuck")}, 1, 1, false, codex.ErrDiscoveryCursor},
		{"missing-end-evidence", codex.SubagentQuery{Cursor: types.Some("missing-cursor")}, 1, 1, false, codex.ErrProtocol},
		{"cycle", codex.SubagentQuery{Cursor: types.Some("cycle-a")}, 2, 2, false, codex.ErrDiscoveryCursor},
		{"partial-native-rejection", codex.SubagentQuery{Cursor: types.Some("before-reject")}, 1, 1, false, nil},
		{"page-limit", codex.SubagentQuery{MaxPages: 1}, 1, 1, false, codex.ErrLimit},
		{"record-limit", codex.SubagentQuery{MaxRecords: 1}, 1, 2, false, codex.ErrLimit},
		{"byte-limit", codex.SubagentQuery{MaxBytes: 1}, 0, 1, false, codex.ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFake(t, "session-config-discovery", codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			page, err := s.ListSubagents(context.Background(), tc.query)
			if tc.name == "partial-native-rejection" {
				var rpc *codex.RPCError
				require.ErrorAs(t, err, &rpc)
				require.Equal(t, "storage access denied", rpc.Message)
				require.JSONEq(t, `{"native":"permission"}`, string(rpc.Data))
			} else if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, page.Pages, tc.pages)
			require.Equal(t, tc.completed, page.Coverage.PagesCompleted)
			require.Equal(t, tc.complete, page.Coverage.EnumerationComplete)
			if err != nil {
				require.NotEmpty(t, page.Coverage.IncompleteReason)
			}
			switch tc.name {
			case "page-limit", "record-limit":
				require.Equal(t, "second", page.NextCursor.ValueOr(""))
			case "byte-limit":
				require.False(t, page.NextCursor.IsSet())
			case "partial-native-rejection":
				require.Equal(t, "reject", page.NextCursor.ValueOr(""))
			}
			require.NoError(t, s.Err())
		})
	}
}

func TestSessionDiscoveryEmptyEnumerationAndLocalRefusals(t *testing.T) {
	f := startFake(t, "session-config-discovery-empty", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	page, err := s.ListSubagents(context.Background(), codex.SubagentQuery{SourceKinds: types.Some([]types.ThreadSourceKind{types.ThreadSourceKindSubAgentThreadSpawn}), Archived: types.Some(false)})
	require.NoError(t, err)
	require.Len(t, page.Pages, 1)
	require.Empty(t, page.Pages[0].Data)
	require.True(t, page.Coverage.EndReached)
	require.True(t, page.Coverage.EnumerationComplete)
	before := len(trace(t, f.trace))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		query codex.SubagentQuery
		cause error
	}{
		{canceled, codex.SubagentQuery{}, context.Canceled},
		{context.Background(), codex.SubagentQuery{MaxPages: -1}, codex.ErrInvalidInput},
		{context.Background(), codex.SubagentQuery{MaxRecords: -1}, codex.ErrInvalidInput},
		{context.Background(), codex.SubagentQuery{MaxBytes: -1}, codex.ErrInvalidInput},
	} {
		page, err = s.ListSubagents(tc.ctx, tc.query)
		require.ErrorIs(t, err, tc.cause)
		var call *codex.CallError
		require.ErrorAs(t, err, &call)
		require.Equal(t, codex.NotSent, call.Dispatch)
		require.Empty(t, page.Pages)
		require.False(t, page.Coverage.EnumerationComplete)
	}
	require.Len(t, trace(t, f.trace), before)
	require.NoError(t, s.Close())
	_, err = s.ListSubagents(context.Background(), codex.SubagentQuery{})
	require.ErrorIs(t, err, codex.ErrClosed)
}

func TestSessionDiscoveryDispatchedCancellationHasNoRetry(t *testing.T) {
	f := startFake(t, "session-config-discovery", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.ListSubagents(ctx, codex.SubagentQuery{Cursor: types.Some("withhold")})
		result <- err
	}()
	waitTraceWithin(t, f.trace, "thread/list", time.Second)
	cancel()
	var call *codex.CallError
	require.ErrorAs(t, <-result, &call)
	require.ErrorIs(t, call, context.Canceled)
	require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
	_, err = s.SetModel(context.Background(), "still-usable")
	require.NoError(t, err)
	queries := 0
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/list" {
			queries++
		}
	}
	require.Equal(t, 1, queries)
}

func TestSessionDiscoveryCoverageFreezesExactWireSourceSelection(t *testing.T) {
	f := startFake(t, "session-config-discovery-empty", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for _, tc := range []struct {
		name      string
		sources   []types.ThreadSourceKind
		selection string
		wire      string
	}{
		{"nil-value", nil, "native-unrestricted", `null`},
		{"empty-array", []types.ThreadSourceKind{}, "native-interactive", `[]`},
		{"explicit", []types.ThreadSourceKind{types.ThreadSourceKindSubAgentReview}, "explicit", `["subAgentReview"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers := []string{"provider-original"}
			page, err := s.ListSubagents(context.Background(), codex.SubagentQuery{SourceKinds: types.Some(tc.sources), ModelProviders: types.Some(providers)})
			require.NoError(t, err)
			require.Equal(t, tc.selection, page.Coverage.SourceSelection)
			providers[0] = "mutated-provider"
			if len(tc.sources) > 0 {
				tc.sources[0] = types.ThreadSourceKindCLI
			}
			raw, err := page.Coverage.Requested.MarshalJSON()
			require.NoError(t, err)
			require.JSONEq(t, `{"ancestorThreadId":"main-1","sourceKinds":`+tc.wire+`,"modelProviders":["provider-original"]}`, string(raw))
		})
	}
}

func TestSessionDiscoveryScopeAndNoObservationSideEffects(t *testing.T) {
	f := startFake(t, "session-config-discovery", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Receive(context.Background()) // opened main only
	require.NoError(t, err)
	page, err := s.ListSubagents(context.Background(), codex.SubagentQuery{Archived: types.Some(true), Limit: types.Some(uint32(2)), SourceKinds: types.Some([]types.ThreadSourceKind{})})
	require.NoError(t, err)
	require.True(t, page.Coverage.Archived)
	require.Equal(t, "native-interactive", page.Coverage.SourceSelection)
	emitRequests(t, f.c, `{"method":"future/unobserved","params":{"threadId":"old-grandchild"}}`, `{"method":"future/unrelated","params":{"threadId":"unrelated"}}`, `{"method":"future/main","params":{"threadId":"main-1"}}`)
	got, err := s.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "future/main", got.Event.Method)
	for _, frame := range trace(t, f.trace) {
		switch frame.Method {
		case "initialize", "initialized", "thread/start", "thread/list", "request-frames":
		default:
			assert.Failf(t, "unexpected query side effect", "%s", frame.Method)
		}
	}
}

func TestSessionDiscoveryKnownGateAndOwnerCancellation(t *testing.T) {
	for _, mode := range []string{"session-discovery-unsupported", "session-config-discovery"} {
		t.Run(mode, func(t *testing.T) {
			f := startFake(t, mode, codex.Limits{})
			s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			if strings.Contains(mode, "unsupported") {
				_, err = s.ListSubagents(context.Background(), codex.SubagentQuery{})
				require.ErrorIs(t, err, codex.ErrUnsupportedDiscovery)
				var call *codex.CallError
				require.ErrorAs(t, err, &call)
				require.Equal(t, codex.NotSent, call.Dispatch)
				for _, frame := range trace(t, f.trace) {
					require.NotEqual(t, "thread/list", frame.Method)
				}
			} else {
				result := make(chan error, 1)
				go func() {
					_, err := s.ListSubagents(context.Background(), codex.SubagentQuery{Cursor: types.Some("withhold")})
					result <- err
				}()
				waitTraceWithin(t, f.trace, "thread/list", time.Second)
				require.NoError(t, s.Close())
				var call *codex.CallError
				require.ErrorAs(t, <-result, &call)
				require.Equal(t, codex.OutcomeUnknown, call.Dispatch)
				require.NoError(t, f.c.Call(context.Background(), "echo", json.RawMessage(`{}`), nil))
			}
		})
	}
}

func TestSessionDiscoveryExplicitNativeFiltersAndUnknownMetadata(t *testing.T) {
	f := startFake(t, "session-config-discovery", codex.Limits{})
	s, err := codex.Open(context.Background(), codex.SessionOptions{Client: f.c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	page, err := s.ListSubagents(context.Background(), codex.SubagentQuery{
		Cursor: types.Some("future"), Limit: types.Some(uint32(7)), Archived: types.Null[bool](), SourceKinds: types.Null[[]types.ThreadSourceKind](),
		ModelProviders: types.Some([]string{"native-provider"}), SortKey: types.Some(types.ThreadSortKeyUpdatedAt), SortDirection: types.Some(types.SortDirectionAsc),
		Cwd: types.Some(types.ThreadListCwdFilter{Value: types.Ptr("/chosen")}), UseStateDbOnly: types.Some(true),
	})
	require.NoError(t, err)
	require.True(t, page.Coverage.EndReached)
	require.False(t, page.Coverage.EnumerationComplete)
	require.False(t, page.Coverage.Archived)
	require.Equal(t, "native-unrestricted", page.Coverage.SourceSelection)
	require.JSONEq(t, `{"futureSource":true}`, string(page.Pages[0].Data[0].Source.Unknown))
	require.JSONEq(t, `{"type":"future-status","active":null}`, string(page.Pages[0].Data[0].Status.Unknown))
	for _, frame := range trace(t, f.trace) {
		if frame.Method == "thread/list" {
			require.JSONEq(t, `{"ancestorThreadId":"main-1","cursor":"future","limit":7,"archived":null,"sourceKinds":null,"modelProviders":["native-provider"],"sortKey":"updated_at","sortDirection":"asc","cwd":"/chosen","useStateDbOnly":true}`, string(frame.Params))
		}
	}
}

// Stored records exist before Open; they are not seeded by live notifications.
func handleDiscoveryPeer(mode string, m fakeEnvelope, send func(json.RawMessage, string)) bool {
	if !strings.Contains(mode, "discovery") || m.Method != "thread/list" {
		return false
	}
	var p struct {
		Cursor *string `json:"cursor"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		os.Exit(97)
	}
	if strings.HasSuffix(mode, "discovery-empty") {
		send(m.ID, `{"data":[],"nextCursor":null}`)
		return true
	}
	if p.Cursor != nil {
		switch *p.Cursor {
		case "empty":
			send(m.ID, `{"data":[],"nextCursor":"second"}`)
			return true
		case "empty-end":
			send(m.ID, `{"data":[],"nextCursor":null}`)
			return true
		case "stuck":
			send(m.ID, `{"data":[],"nextCursor":"stuck"}`)
			return true
		case "missing-cursor":
			send(m.ID, `{"data":[]}`)
			return true
		case "cycle-a":
			send(m.ID, `{"data":[],"nextCursor":"cycle-b"}`)
			return true
		case "cycle-b":
			send(m.ID, `{"data":[],"nextCursor":"cycle-a"}`)
			return true
		case "before-reject":
			send(m.ID, `{"data":[],"nextCursor":"reject"}`)
			return true
		case "reject":
			fmt.Fprintf(os.Stdout, `{"id":%s,"error":{"code":-32001,"message":"storage access denied","data":{"native":"permission"}}}`+"\n", m.ID)
			return true
		case "withhold":
			return true
		case "future":
			send(m.ID, `{"data":[{"id":"future-child","sessionId":"future-session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":false,"modelProvider":"mock","preview":"unknown","projectId":null,"source":{"futureSource":true},"status":{"type":"future-status","active":null},"turns":[]}],"nextCursor":null}`)
			return true
		}
	}
	if p.Cursor == nil {
		send(m.ID, `{"data":[{"id":"old-child","sessionId":"old-session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":false,"modelProvider":"mock","preview":"stored","projectId":null,"source":{"subAgent":"review"},"status":{"type":"notLoaded"},"turns":[],"recordFuture":{"n":9007199254740993}}],"nextCursor":"second","backwardsCursor":"back","pageFuture":true}`)
	} else {
		send(m.ID, `{"data":[{"id":"old-grandchild","parentThreadId":"old-child","sessionId":"grand-session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":false,"modelProvider":"mock","preview":"nested","projectId":null,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":"old-child","depth":2}}},"status":{"type":"notLoaded"},"turns":[]}],"nextCursor":null,"backwardsCursor":null}`)
	}
	return true
}
