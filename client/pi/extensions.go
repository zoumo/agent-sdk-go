package pi

import (
	"context"
	"encoding/json"
	"errors"
)

// Command identifies a slash command visible to the current Pi session.
type Command struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Source      string          `json:"source"`
	SourceInfo  json.RawMessage `json:"sourceInfo"`
}

// Commands reads the extension commands, prompt templates, and skills Pi exposes.
func (s *Session) Commands(ctx context.Context) ([]Command, error) {
	raw, err := s.request(ctx, "get_commands", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Commands []Command `json:"commands"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &DispatchError{Sent: true, Err: err}
	}
	if result.Commands == nil {
		return nil, &DispatchError{Sent: true, Err: errors.New("pi: get_commands response missing commands")}
	}
	return result.Commands, nil
}
