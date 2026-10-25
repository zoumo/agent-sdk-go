package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// These tests retain the independent reader's assertions after removal of the
// Session Load lifecycle. The peer handles query frames only; transport framing
// and actual subprocesses remain covered by the public contract tests.
func TestHistoryReaderIntegrity(t *testing.T) {
	for _, mode := range []string{"complete", "late", "cursor", "cursor absent", "wrong cursor key", "wrong data key", "wrong items key", "wrong id key", "duplicate turn", "duplicate item", "summary", "missing", "active", "identity", "cwd", "mutated", "limit", "index limit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := newClient(DefaultLimits())
			c.ready = true
			stop := make(chan struct{})
			peerDone := make(chan error, 1)
			methods := make([]string, 0)
			go func() {
				reads := 0
				for {
					select {
					case <-stop:
						peerDone <- nil
						return
					case out := <-c.queue:
						c.mu.Lock()
						out.call.started = true
						c.mu.Unlock()
						var q struct {
							ID     json.RawMessage
							Method string
							Params types.ThreadTurnsListParams
						}
						if err := json.Unmarshal(out.frame, &q); err != nil {
							peerDone <- err
							return
						}
						methods = append(methods, q.Method)
						result := ""
						switch q.Method {
						case "thread/read":
							reads++
							result = `{"thread":{"id":"thread","cwd":"/synthetic","sessionId":"native-session","cliVersion":"0.162.1","modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","turns":[],"createdAt":1,"updatedAt":2,"ephemeral":false,"status":{"type":"idle"}}}`
							switch mode {
							case "missing":
								if err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"error":{"code":-32602,"message":"missing"}}`, q.ID))); err != nil {
									peerDone <- err
									return
								}
								continue
							case "active":
								result = strings.ReplaceAll(result, `"type":"idle"`, `"type":"active","activeFlags":[]`)
							case "identity":
								result = strings.ReplaceAll(result, `"id":"thread"`, `"id":"foreign"`)
							case "cwd":
								result = strings.ReplaceAll(result, `/synthetic`, `/foreign`)
							case "mutated":
								if reads > 1 {
									result = strings.ReplaceAll(result, `"updatedAt":2`, `"updatedAt":3`)
								}
							}
						case "thread/turns/list":
							if q.Params.SortDirection.ValueOr("") != types.SortDirectionAsc || q.Params.ItemsView.ValueOr("") != types.TurnItemsViewFull {
								peerDone <- errors.New("history request must select asc/full")
								return
							}
							if !q.Params.Cursor.IsSet() {
								result = `{"data":[{"id":"t1","status":"completed","items":[{"id":"i1","type":"userMessage","content":[{"type":"text","text":"hello"}]}],"error":null}],"nextCursor":"p2"}`
								switch mode {
								case "cursor absent":
									result = strings.ReplaceAll(result, `,"nextCursor":"p2"`, ``)
								case "wrong cursor key":
									result = strings.ReplaceAll(result, `"nextCursor"`, `"NEXTCURSOR"`)
								case "wrong data key":
									result = strings.ReplaceAll(result, `"data"`, `"DATA"`)
								case "wrong items key":
									result = strings.ReplaceAll(result, `"items"`, `"ITEMS"`)
								case "wrong id key":
									result = strings.ReplaceAll(result, `"id"`, `"ID"`)
								}
							} else {
								result = `{"data":[{"id":"t2","status":"completed","items":[{"id":"i2","type":"agentMessage","text":"done"}],"error":null}],"nextCursor":null}`
								switch mode {
								case "late":
									if err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"error":{"code":-32601,"message":"unsupported"}}`, q.ID))); err != nil {
										peerDone <- err
										return
									}
									continue
								case "cursor":
									result = `{"data":[],"nextCursor":"p2"}`
								case "duplicate turn":
									result = strings.ReplaceAll(result, `"t2"`, `"t1"`)
								case "duplicate item":
									result = strings.ReplaceAll(result, `"i2"`, `"i1"`)
								case "summary":
									result = strings.ReplaceAll(result, `"status":"completed"`, `"status":"completed","itemsView":"summary"`)
								}
							}
						default:
							peerDone <- fmt.Errorf("unexpected side-effect method %s", q.Method)
							return
						}
						if err := c.routeFrame([]byte(fmt.Sprintf(`{"id":%s,"result":%s}`, q.ID, result))); err != nil {
							peerDone <- err
							return
						}
					}
				}
			}()
			var join sync.Once
			finish := func() { join.Do(func() { close(stop); require.NoError(t, <-peerDone); c.fail(ErrClosed) }) }
			t.Cleanup(finish)
			o := HistoryOptions{ThreadID: "thread", CWD: "/synthetic", CodexHome: "/synthetic-home", PageSize: 1}
			if mode == "limit" {
				o.MaxRecords = 3
			}
			if mode == "index limit" {
				o.MaxIndexBytes = 1
			}
			require.NoError(t, normalizeHistoryOptions(&o))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var events []types.Event
			err := c.replayHistoryOwned(ctx, o, func(e types.Event) error {
				events = append(events, e)
				if mode == "cancel" {
					cancel()
				}
				return nil
			}, nil, nil)
			// Join the worker before inspecting its query trace.
			finish()
			if mode == "complete" {
				require.NoError(t, err)
				require.Len(t, events, 4)
				assert.Equal(t, []string{"thread/read", "thread/turns/list", "thread/turns/list", "thread/read"}, methods)
				assert.Equal(t, "done", events[2].Data.(*types.ItemCompletedNotification).Item.AgentMessage.Text)
			} else {
				require.Error(t, err)
				var h *HistoryError
				require.ErrorAs(t, err, &h)
				assert.Equal(t, "thread", h.ThreadID())
				assert.Equal(t, len(events), h.Accepted())
				if mode == "cancel" {
					assert.ErrorIs(t, err, context.Canceled)
				}
				if mode == "late" {
					var native *RPCError
					assert.ErrorAs(t, err, &native)
					assert.Equal(t, "p2", h.Cursor().ValueOr(""))
					assert.Empty(t, h.Raw())
				}
			}
			assert.Empty(t, c.sessions, "queries must not create Session owners")
			for _, e := range events {
				require.NotNil(t, e.History)
				assert.Empty(t, e.Raw)
				assert.True(t, json.Valid(e.History.Raw))
			}
		})
	}
}

func TestHistoryProjectionRetainsNativeFacts(t *testing.T) {
	raw := json.RawMessage(`{"id":"turn","status":"interrupted","items":[{"id":"tool","type":"commandExecution","command":"never executed","commandActions":[],"cwd":"/synthetic","status":"failed","exitCode":7,"aggregatedOutput":"retained failure","future":true},{"id":"compact","type":"contextCompaction"},{"id":"future","type":"futureItem","opaque":{"retained":true}}],"error":null,"futureTurn":true}`)
	var events []types.Event
	require.NoError(t, replayTurn("thread", raw, func(e types.Event) error { events = append(events, e); return nil }))
	require.Len(t, events, 4)
	tool := events[0].Data.(*types.ItemCompletedNotification).Item.CommandExecution
	require.NotNil(t, tool)
	assert.Equal(t, "failed", string(tool.Status))
	assert.Equal(t, int32(7), tool.ExitCode.ValueOr(0))
	assert.Equal(t, "retained failure", tool.AggregatedOutput.ValueOr(""))
	assert.JSONEq(t, `true`, string(tool.UnknownFields["future"]))
	for _, e := range events[:3] {
		assert.Equal(t, []string{"completedAtMs"}, e.History.MissingFields)
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(e.Params, &params))
		assert.NotContains(t, params, "completedAtMs")
		assert.Empty(t, e.Raw)
	}
	assert.Equal(t, `{"id":"future","type":"futureItem","opaque":{"retained":true}}`, string(events[2].History.Raw))
	assert.Equal(t, types.TurnStatusInterrupted, events[3].Data.(*types.TurnCompletedNotification).Turn.Status)
	events = nil
	require.NoError(t, replayTurn("thread", json.RawMessage(`{"id":"turn","status":"futureStatus","items":[],"error":null,"opaque":true}`), func(e types.Event) error { events = append(events, e); return nil }))
	require.Len(t, events, 1)
	assert.Empty(t, events[0].Method)
	assert.Equal(t, types.TurnStatus("futureStatus"), events[0].Data.(*types.Turn).Status)
	assert.Contains(t, string(events[0].History.Raw), `"opaque":true`)
}
