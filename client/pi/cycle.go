package pi

import (
	"context"
	"encoding/json"
	"errors"
)

// ModelSelection is Pi's result after cycling the active model. Model retains
// the full native object, including provider-specific fields.
type ModelSelection struct {
	Model         json.RawMessage
	ThinkingLevel string
	IsScoped      bool
}

// CycleModel advances to the next model in Pi's rotation. A nil selection
// with a nil error means Pi returned no alternative.
func (s *Session) CycleModel(ctx context.Context) (*ModelSelection, error) {
	var data *struct {
		Model         json.RawMessage `json:"model"`
		ThinkingLevel string          `json:"thinkingLevel"`
		IsScoped      *bool           `json:"isScoped"`
	}
	if _, err := s.readData(ctx, "cycle_model", nil, &data); err != nil || data == nil {
		return nil, err
	}
	if len(data.Model) == 0 || data.Model[0] != '{' || data.ThinkingLevel == "" || data.IsScoped == nil {
		return nil, malformedData("cycle_model", errors.New("expected model object, thinking level and scope flag"))
	}
	return &ModelSelection{
		Model: data.Model, ThinkingLevel: data.ThinkingLevel, IsScoped: *data.IsScoped,
	}, nil
}

// CycleThinkingLevel advances the current model's thinking level. A nil level
// with a nil error means Pi returned no alternative.
func (s *Session) CycleThinkingLevel(ctx context.Context) (*string, error) {
	var data *struct {
		Level string `json:"level"`
	}
	if _, err := s.readData(ctx, "cycle_thinking_level", nil, &data); err != nil || data == nil {
		return nil, err
	}
	if data.Level == "" {
		return nil, malformedData("cycle_thinking_level", errors.New("missing thinking level"))
	}
	return &data.Level, nil
}
