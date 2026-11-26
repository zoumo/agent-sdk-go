package pi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

func TestClearQueueAndCompact(t *testing.T) {
	const usage = `{"input":8,"totalTokens":12,"cost":{"total":0.01},"future":true}`
	const details = `{"extension":{"readFiles":["a.go"]}}`
	full := `{"summary":"keep this","firstKeptEntryId":"abc","tokensBefore":42,"estimatedTokensAfter":0,"usage":` + usage + `,"details":` + details + `}`
	responses := []string{
		`{"steering":[],"followUp":[]}`,
		`{"steering":["two","","one"],"followUp":["next","last"]}`,
		`{"summary":"","firstKeptEntryId":"first","tokensBefore":0}`,
		full,
	}
	requests := make(chan map[string]any, len(responses))
	s, ctx := runPeer(t, func(p peer) error {
		for _, data := range responses {
			var frame map[string]any
			if err := p.input.Decode(&frame); err != nil {
				return err
			}
			id, _ := frame["id"].(string)
			typ, _ := frame["type"].(string)
			delete(frame, "id")
			requests <- frame
			if err := p.reply(command{ID: id, Type: typ}, json.RawMessage(data)); err != nil {
				return err
			}
		}
		return nil
	})

	q, err := s.ClearQueue(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "clear_queue"}, <-requests)
	assert.Equal(t, pi.ClearedQueue{Steering: []string{}, FollowUp: []string{}}, q)
	q, err = s.ClearQueue(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "clear_queue"}, <-requests)
	assert.Equal(t, []string{"two", "", "one"}, q.Steering)
	assert.Equal(t, []string{"next", "last"}, q.FollowUp)

	result, err := s.Compact(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "compact"}, <-requests)
	assert.Equal(t, pi.CompactionResult{FirstKeptEntryID: "first"}, result)
	result, err = s.Compact(ctx, "summarize changes")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "compact", "customInstructions": "summarize changes"}, <-requests)
	assert.Equal(t, "keep this", result.Summary)
	assert.Equal(t, "abc", result.FirstKeptEntryID)
	assert.Equal(t, int64(42), result.TokensBefore)
	assert.Equal(t, new(int64(0)), result.EstimatedTokensAfter)
	assert.JSONEq(t, usage, string(result.Usage))
	assert.JSONEq(t, details, string(result.Details))
}

func TestMaintenanceResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, command, data string
	}{
		{"missing steering", "clear_queue", `{"followUp":[]}`},
		{"null follow-up", "clear_queue", `{"steering":[],"followUp":null}`},
		{"invalid steering text", "clear_queue", `{"steering":[1],"followUp":[]}`},
		{"null follow-up text", "clear_queue", `{"steering":[],"followUp":[null]}`},
		{"missing summary", "compact", `{"firstKeptEntryId":"a","tokensBefore":4}`},
		{"null first kept entry", "compact", `{"summary":"s","firstKeptEntryId":null,"tokensBefore":4}`},
		{"missing token count", "compact", `{"summary":"s","firstKeptEntryId":"a"}`},
		{"invalid token count", "compact", `{"summary":"s","firstKeptEntryId":"a","tokensBefore":"4"}`},
		{"invalid usage", "compact", `{"summary":"s","firstKeptEntryId":"a","tokensBefore":4,"usage":[]}`},
		{"rejected compaction", "compact", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := runPeer(t, func(p peer) error {
				c, err := p.receive()
				if err != nil {
					return err
				}
				if c.Type != tc.command {
					return fmt.Errorf("got %s, want %s", c.Type, tc.command)
				}
				if tc.data != "" {
					return p.reply(c, json.RawMessage(tc.data))
				}
				return json.NewEncoder(p.output).Encode(map[string]any{
					"id": c.ID, "type": "response", "command": c.Type, "success": false, "error": "nothing to compact",
				})
			})
			var err error
			if tc.command == "clear_queue" {
				_, err = s.ClearQueue(ctx)
			} else {
				_, err = s.Compact(ctx, "")
			}
			if tc.data == "" {
				var rejected *pi.CommandError
				require.ErrorAs(t, err, &rejected)
				assert.Equal(t, tc.command, rejected.Command)
				assert.Equal(t, "nothing to compact", rejected.Message)
				return
			}
			var malformed *pi.DispatchError
			require.ErrorAs(t, err, &malformed)
			assert.True(t, malformed.Sent)
			assert.ErrorContains(t, err, tc.command+" response data")
		})
	}
}

func TestAutoCompactionAndRetrySettings(t *testing.T) {
	type toggle struct {
		command
		Enabled *bool `json:"enabled"`
	}
	requests := make(chan toggle, 4)
	s, ctx := runPeer(t, func(p peer) error {
		for range cap(requests) {
			var c toggle
			if err := p.input.Decode(&c); err != nil {
				return err
			}
			requests <- c
			if err := p.reply(c.command, nil); err != nil {
				return err
			}
		}
		c, err := p.receive()
		if err != nil {
			return err
		}
		if c.Type != "abort_retry" {
			return fmt.Errorf("unexpected command %q", c.Type)
		}
		return p.reply(c, nil)
	})
	for _, tc := range []struct {
		name string
		set  func(context.Context, bool) error
	}{
		{"set_auto_compaction", s.SetAutoCompaction},
		{"set_auto_retry", s.SetAutoRetry},
	} {
		for _, enabled := range []bool{true, false} {
			require.NoError(t, tc.set(ctx, enabled))
			req := <-requests
			assert.Equal(t, tc.name, req.Type)
			assert.Equal(t, &enabled, req.Enabled)
		}
	}
	require.NoError(t, s.AbortRetry(ctx))
}

func TestAbortRetryCancelAfterDispatch(t *testing.T) {
	read := make(chan struct{})
	s, ctx := runPeer(t, func(p peer) error {
		retry, err := p.receive()
		close(read)
		if err != nil {
			return err
		}
		if retry.Type != "abort_retry" {
			return fmt.Errorf("unexpected command %q", retry.Type)
		}
		// Accepting the next command proves the abort_retry write completed.
		state, err := p.receive()
		if err != nil {
			return err
		}
		if state.Type != "get_state" {
			return fmt.Errorf("expected state while retry response pending, got %q", state.Type)
		}
		if err := p.reply(state, map[string]string{"sessionId": "native-1"}); err != nil {
			return err
		}
		abort, err := p.receive()
		if err != nil {
			return err
		}
		if abort.Type != "abort" {
			return fmt.Errorf("expected explicit abort after cancellation, got %q", abort.Type)
		}
		if err := p.reply(retry, nil); err != nil {
			return err
		}
		if err := p.reply(abort, nil); err != nil {
			return err
		}
		return p.expectEOF()
	})
	wait, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.AbortRetry(wait) }()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err := s.State(ctx)
	require.NoError(t, err)
	cancel()
	var unknown *pi.DispatchError
	require.ErrorAs(t, <-result, &unknown)
	assert.True(t, unknown.Sent)
	assert.ErrorIs(t, unknown, context.Canceled)
	require.NoError(t, s.Abort(ctx))
	require.NoError(t, s.Close())
}
