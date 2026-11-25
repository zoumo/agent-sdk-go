package pi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/pi"
)

func TestRPCInspection(t *testing.T) {
	type step struct {
		want string
		data string
	}
	steps := []step{
		{`{"type":"get_messages"}`, `{"messages":[{"role":"custom","native":true}]}`},
		{`{"type":"get_entries"}`, `{"entries":[{"id":"a","type":"message","parentId":null,"timestamp":"2026-11-25T00:00:00Z","message":{"role":"user"}},{"id":"b","type":"future","parentId":"a","extra":42}],"leafId":"a"}`},
		{`{"type":"get_entries","since":"b"}`, `{"entries":[{"id":"c","type":"message","parentId":"a","message":{"role":"assistant"}}],"leafId":"c"}`},
		{`{"type":"get_entries","since":"c"}`, `{"entries":[],"leafId":"c"}`},
		{`{"type":"get_tree"}`, `{"tree":[{"entry":{"id":"a","type":"message","parentId":null},"children":[{"entry":{"id":"b","type":"future","parentId":"a"},"children":[],"label":"old","labelTimestamp":"2026-11-25T00:00:00Z"},{"entry":{"id":"c","type":"message","parentId":"a"},"children":[]}]}],"leafId":"c"}`},
		{`{"type":"get_session_stats"}`, `{"sessionId":"native-1","sessionFile":"/session.jsonl","userMessages":1,"assistantMessages":1,"toolCalls":3,"toolResults":3,"totalMessages":5,"tokens":{"input":30,"output":4,"cacheRead":6,"cacheWrite":7,"total":47},"cost":0.45,"contextUsage":{"tokens":40,"contextWindow":200,"percent":20},"future":1}`},
		{`{"type":"get_available_models"}`, `{"models":[{"id":"m","provider":"custom","compat":{"future":true}}]}`},
		{`{"type":"get_available_thinking_levels"}`, `{"levels":["off","xhigh","max"]}`},
		{`{"type":"get_state"}`, `{"sessionId":"native-1","steeringMode":"all","followUpMode":"one-at-a-time","pendingMessageCount":2}`},
	}
	s, ctx := runPeer(t, func(p peer) error {
		for _, step := range steps {
			var got map[string]json.RawMessage
			if err := p.input.Decode(&got); err != nil {
				return err
			}
			var id, typ string
			if err := json.Unmarshal(got["id"], &id); err != nil {
				return err
			}
			if err := json.Unmarshal(got["type"], &typ); err != nil {
				return err
			}
			delete(got, "id")
			var want map[string]json.RawMessage
			if err := json.Unmarshal([]byte(step.want), &want); err != nil {
				return err
			}
			if !maps.EqualFunc(got, want, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
				return fmt.Errorf("received %v, expected %s", got, step.want)
			}
			if err := p.reply(command{ID: id, Type: typ}, json.RawMessage(step.data)); err != nil {
				return err
			}
		}
		if _, err := p.receive(); !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected stdin EOF, got %w", err)
		}
		return nil
	})

	messages, err := s.Messages(ctx)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.JSONEq(t, `{"role":"custom","native":true}`, string(messages[0]))
	first, err := s.Entries(ctx, "")
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	assert.Equal(t, "2026-11-25T00:00:00Z", first.Entries[0].Timestamp)
	assert.Nil(t, first.Entries[0].ParentID)
	cursor := first.Entries[1]
	assert.Equal(t, "b", cursor.ID)
	assert.Equal(t, "future", cursor.Type)
	assert.Equal(t, new("a"), cursor.ParentID)
	assert.Equal(t, new("a"), first.LeafID)
	assert.Contains(t, string(cursor.Raw), `"extra":42`)
	second, err := s.Entries(ctx, cursor.ID)
	require.NoError(t, err)
	require.Len(t, second.Entries, 1)
	assert.Equal(t, "c", second.Entries[0].ID)
	last, err := s.Entries(ctx, second.Entries[0].ID)
	require.NoError(t, err)
	assert.Equal(t, []pi.Entry{}, last.Entries)
	assert.Equal(t, new("c"), last.LeafID)

	tree, err := s.Tree(ctx)
	require.NoError(t, err)
	require.Len(t, tree.Roots, 1)
	require.Len(t, tree.Roots[0].Children, 2)
	assert.Equal(t, new("c"), tree.LeafID)
	assert.Equal(t, new("old"), tree.Roots[0].Children[0].Label)
	assert.Equal(t, "c", tree.Roots[0].Children[1].Entry.ID)
	stats, err := s.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, s.ID(), stats.SessionID)
	assert.Equal(t, int64(47), stats.Tokens.Total)
	assert.InDelta(t, 0.45, stats.Cost, 1e-9)
	require.NotNil(t, stats.ContextUsage)
	assert.Equal(t, new(int64(40)), stats.ContextUsage.Tokens)
	assert.Contains(t, string(stats.Raw), `"future":1`)
	models, err := s.AvailableModels(ctx)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Contains(t, string(models[0]), `"future":true`)
	levels, err := s.AvailableThinkingLevels(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"off", "xhigh", "max"}, levels)
	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, pi.QueueAll, state.SteeringMode)
	assert.Equal(t, pi.QueueOneAtATime, state.FollowUpMode)
	assert.Equal(t, 2, state.PendingMessageCount)
	assert.Equal(t, "native-1", s.ID())
	require.NoError(t, s.Close())
}

func TestRPCInspectionResponseErrors(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		c, err := p.receive()
		if err != nil {
			return err
		}
		err = json.NewEncoder(p.output).Encode(map[string]any{
			"id": c.ID, "type": "response", "command": c.Type, "success": false, "error": "unknown cursor",
		})
		if err != nil {
			return err
		}
		malformed := []string{`{"tree":[],"leafId":5}`, `{"messages":null}`, `{"levels":["off",false]}`}
		for _, data := range malformed {
			next, err := p.receive()
			if err != nil {
				return err
			}
			if err := p.reply(next, json.RawMessage(data)); err != nil {
				return err
			}
		}
		c, err = p.receive()
		if err != nil {
			return err
		}
		return p.reply(c, json.RawMessage(`{"sessionId":"another-session"}`))
	})

	_, err := s.Entries(ctx, "unknown")
	var rejected *pi.CommandError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "get_entries", rejected.Command)
	assert.Equal(t, "unknown cursor", rejected.Message)

	_, err = s.Tree(ctx)
	var malformed *pi.DispatchError
	require.ErrorAs(t, err, &malformed)
	assert.True(t, malformed.Sent)
	assert.ErrorContains(t, err, "get_tree response data")
	_, err = s.Messages(ctx)
	require.ErrorAs(t, err, &malformed)
	assert.ErrorContains(t, err, "get_messages response data")
	_, err = s.AvailableThinkingLevels(ctx)
	require.ErrorAs(t, err, &malformed)
	assert.ErrorContains(t, err, "get_available_thinking_levels response data")
	_, err = s.Stats(ctx)
	require.ErrorAs(t, err, &malformed)
	assert.ErrorContains(t, err, "missing or changed native session ID")
	<-s.Done()
	assert.Equal(t, "native-1", s.ID())
	assert.ErrorIs(t, s.SetSteeringMode(ctx, pi.QueueAll), io.ErrClosedPipe)
}
