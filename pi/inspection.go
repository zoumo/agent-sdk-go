package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// QueueMode is a native Pi steering or follow-up delivery mode.
type QueueMode string

const (
	QueueAll        QueueMode = "all"
	QueueOneAtATime QueueMode = "one-at-a-time"
)

// Entry contains common native session entry fields. Raw retains the full entry,
// including type-specific content.
type Entry struct {
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Raw       json.RawMessage `json:"-"`
}

func (e *Entry) UnmarshalJSON(raw []byte) error {
	type fields Entry
	var entry fields
	if err := json.Unmarshal(raw, &entry); err != nil {
		return err
	}
	if entry.ID == "" || entry.Type == "" {
		return errors.New("missing entry ID or type")
	}
	*e = Entry(entry)
	e.Raw = append(json.RawMessage(nil), raw...)
	return nil
}

// Entries contains entries in native append order. LeafID identifies the active
// branch's leaf and is nil when no leaf is selected.
type Entries struct {
	Entries []Entry
	LeafID  *string
}

// Tree contains native root nodes, including abandoned branches, and the
// optional active leaf identity.
type Tree struct {
	Roots  []TreeNode
	LeafID *string
}

// TreeNode mirrors one native session tree node.
type TreeNode struct {
	Entry          Entry      `json:"entry"`
	Children       []TreeNode `json:"children"`
	Label          *string    `json:"label"`
	LabelTimestamp *string    `json:"labelTimestamp"`
}

// SessionStats contains Pi's session-wide totals. ContextUsage, when present,
// describes the current model context. Raw retains the full native data object.
type SessionStats struct {
	SessionID         string          `json:"sessionId"`
	SessionFile       string          `json:"sessionFile"`
	UserMessages      int64           `json:"userMessages"`
	AssistantMessages int64           `json:"assistantMessages"`
	ToolCalls         int64           `json:"toolCalls"`
	ToolResults       int64           `json:"toolResults"`
	TotalMessages     int64           `json:"totalMessages"`
	Tokens            TokenUsage      `json:"tokens"`
	Cost              float64         `json:"cost"`
	ContextUsage      *ContextUsage   `json:"contextUsage"`
	Raw               json.RawMessage `json:"-"`
}

// TokenUsage holds the native token totals.
type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

// ContextUsage describes the current model context, with nil values when Pi
// cannot estimate how much of it is occupied.
type ContextUsage struct {
	Tokens        *int64   `json:"tokens"`
	ContextWindow int64    `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// Messages reads the current model-facing conversation. Each message retains
// its complete native JSON.
func (s *Session) Messages(ctx context.Context) ([]json.RawMessage, error) {
	var data struct {
		Messages []json.RawMessage `json:"messages"`
	}
	_, err := s.readData(ctx, "get_messages", nil, &data)
	return requiredList("get_messages", "messages", data.Messages, err)
}

// Entries reads append-order records from the current session. Pass an empty
// since to start from the beginning, or the last received entry ID to read
// strictly after it. LeafID describes the active branch independently of since.
// An empty result means there were no new entries at the time of the call.
func (s *Session) Entries(ctx context.Context, since string) (Entries, error) {
	var fields map[string]any
	if since != "" {
		fields = map[string]any{"since": since}
	}
	var data struct {
		Entries []Entry         `json:"entries"`
		LeafID  json.RawMessage `json:"leafId"`
	}
	_, err := s.readData(ctx, "get_entries", fields, &data)
	entries, err := requiredList("get_entries", "entries", data.Entries, err)
	if err != nil {
		return Entries{}, err
	}
	leaf, err := decodeLeaf("get_entries", data.LeafID)
	if err != nil {
		return Entries{}, err
	}
	return Entries{Entries: entries, LeafID: leaf}, nil
}

// Tree reads the current session tree with its native active leaf.
func (s *Session) Tree(ctx context.Context) (Tree, error) {
	var data struct {
		Roots  []TreeNode      `json:"tree"`
		LeafID json.RawMessage `json:"leafId"`
	}
	_, err := s.readData(ctx, "get_tree", nil, &data)
	roots, err := requiredList("get_tree", "tree", data.Roots, err)
	if err != nil {
		return Tree{}, err
	}
	leaf, err := decodeLeaf("get_tree", data.LeafID)
	if err != nil {
		return Tree{}, err
	}
	return Tree{Roots: roots, LeafID: leaf}, nil
}

// Stats reads session statistics. A different identity invalidates the session.
func (s *Session) Stats(ctx context.Context) (SessionStats, error) {
	var stats SessionStats
	raw, err := s.readData(ctx, "get_session_stats", nil, &stats)
	if err != nil {
		return SessionStats{}, err
	}
	if err := s.checkIdentity(stats.SessionID); err != nil {
		return SessionStats{}, err
	}
	stats.Raw = raw
	return stats, nil
}

// AvailableModels reads the configured models as complete native model objects.
func (s *Session) AvailableModels(ctx context.Context) ([]json.RawMessage, error) {
	var data struct {
		Models []json.RawMessage `json:"models"`
	}
	_, err := s.readData(ctx, "get_available_models", nil, &data)
	return requiredList("get_available_models", "models", data.Models, err)
}

// AvailableThinkingLevels reads the selected model's discrete native levels.
func (s *Session) AvailableThinkingLevels(ctx context.Context) ([]string, error) {
	var data struct {
		Levels []string `json:"levels"`
	}
	_, err := s.readData(ctx, "get_available_thinking_levels", nil, &data)
	return requiredList("get_available_thinking_levels", "levels", data.Levels, err)
}

func (s *Session) readData(ctx context.Context, command string, fields map[string]any, dst any) (json.RawMessage, error) {
	raw, err := s.request(ctx, command, fields)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return nil, malformedData(command, err)
	}
	return raw, nil
}

func requiredList[T any](command, field string, list []T, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, malformedData(command, fmt.Errorf("missing %s array", field))
	}
	return list, nil
}

func decodeLeaf(command string, raw json.RawMessage) (*string, error) {
	var leaf *string
	if err := json.Unmarshal(raw, &leaf); err != nil {
		return nil, malformedData(command, fmt.Errorf("leaf ID: %w", err))
	}
	return leaf, nil
}

func malformedData(command string, err error) *DispatchError {
	return &DispatchError{Sent: true, Err: fmt.Errorf("pi %s response data: %w", command, err)}
}
