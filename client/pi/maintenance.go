package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ClearedQueue contains the messages Pi removed, in their original order within each queue.
type ClearedQueue struct {
	Steering []string
	FollowUp []string
}

// ClearQueue removes queued steering and follow-up messages in the attached Pi process.
// The active turn continues. Canceling the wait after dispatch leaves the outcome unknown.
func (s *Session) ClearQueue(ctx context.Context) (ClearedQueue, error) {
	var data struct {
		Steering []*string `json:"steering"`
		FollowUp []*string `json:"followUp"`
	}
	if _, err := s.readData(ctx, "clear_queue", nil, &data); err != nil {
		return ClearedQueue{}, err
	}
	steering, err := queueText("steering", data.Steering)
	if err != nil {
		return ClearedQueue{}, err
	}
	followUp, err := queueText("followUp", data.FollowUp)
	if err != nil {
		return ClearedQueue{}, err
	}
	return ClearedQueue{Steering: steering, FollowUp: followUp}, nil
}

func queueText(field string, messages []*string) ([]string, error) {
	if _, err := requiredList("clear_queue", field, messages, nil); err != nil {
		return nil, err
	}
	text := make([]string, len(messages))
	for i, msg := range messages {
		if msg == nil {
			return nil, malformedData("clear_queue", fmt.Errorf("null message in %s at index %d", field, i))
		}
		text[i] = *msg
	}
	return text, nil
}

// CompactionResult describes the summary produced by Pi and the first entry it kept.
// Pi supplies EstimatedTokensAfter, Usage and extension-specific Details when available.
type CompactionResult struct {
	Summary              string
	FirstKeptEntryID     string
	TokensBefore         int64
	EstimatedTokensAfter *int64
	Usage                json.RawMessage
	Details              json.RawMessage
}

// Compact asks the attached Pi process to abort any active turn and compact
// its conversation. Pi may call the model to generate a summary. Pass an empty
// string to use Pi's default instructions. Canceling the wait after dispatch
// lets Pi continue compaction.
func (s *Session) Compact(ctx context.Context, customInstructions string) (CompactionResult, error) {
	var fields map[string]any
	if customInstructions != "" {
		fields = map[string]any{"customInstructions": customInstructions}
	}
	var data struct {
		Summary              *string         `json:"summary"`
		FirstKeptEntryID     *string         `json:"firstKeptEntryId"`
		TokensBefore         *int64          `json:"tokensBefore"`
		EstimatedTokensAfter *int64          `json:"estimatedTokensAfter"`
		Usage                json.RawMessage `json:"usage"`
		Details              json.RawMessage `json:"details"`
	}
	if _, err := s.readData(ctx, "compact", fields, &data); err != nil {
		return CompactionResult{}, err
	}
	if data.Summary == nil || data.FirstKeptEntryID == nil || data.TokensBefore == nil {
		return CompactionResult{}, malformedData("compact", errors.New("required summary, firstKeptEntryId or tokensBefore missing or null"))
	}
	if len(data.Usage) > 0 && data.Usage[0] != '{' {
		return CompactionResult{}, malformedData("compact", errors.New("usage must be an object"))
	}
	return CompactionResult{
		Summary:              *data.Summary,
		FirstKeptEntryID:     *data.FirstKeptEntryID,
		TokensBefore:         *data.TokensBefore,
		EstimatedTokensAfter: data.EstimatedTokensAfter,
		Usage:                data.Usage,
		Details:              data.Details,
	}, nil
}

// SetAutoCompaction asks Pi to update its global setting for automatic context
// compaction. Canceling the wait after dispatch leaves the outcome unknown.
func (s *Session) SetAutoCompaction(ctx context.Context, enabled bool) error {
	_, err := s.request(ctx, "set_auto_compaction", map[string]any{"enabled": enabled})
	return err
}

// SetAutoRetry asks Pi to update its global setting for automatic retry of
// transient errors. Canceling the wait after dispatch leaves the outcome unknown.
func (s *Session) SetAutoRetry(ctx context.Context, enabled bool) error {
	_, err := s.request(ctx, "set_auto_retry", map[string]any{"enabled": enabled})
	return err
}

// AbortRetry asks Pi to stop its current retry. Canceling the wait after dispatch
// leaves the outcome unknown.
func (s *Session) AbortRetry(ctx context.Context) error {
	_, err := s.request(ctx, "abort_retry", nil)
	return err
}
