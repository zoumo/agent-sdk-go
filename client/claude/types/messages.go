// SPDX-License-Identifier: Apache-2.0

package types

import "encoding/json"

// MessageOrigin is an open provenance record. Unrecognized kinds and keys survive.
type MessageOrigin struct {
	Kind   string                `json:"kind"`
	Raw    json.RawMessage       `json:"-"`
	Fields map[string]FieldState `json:"-"`
}

// ServerToolUseBlock is performed by the API; it does not require a client answer.
type ServerToolUseBlock struct {
	ID     string                `json:"id"`
	Name   string                `json:"name"`
	Input  json.RawMessage       `json:"input,omitempty"`
	Raw    json.RawMessage       `json:"-"`
	Fields map[string]FieldState `json:"-"`
}

// ServerToolResultBlock keeps server-specific content opaque.
type ServerToolResultBlock struct {
	ToolUseID string                `json:"tool_use_id"`
	Content   json.RawMessage       `json:"content"`
	Raw       json.RawMessage       `json:"-"`
	Fields    map[string]FieldState `json:"-"`
}
type RedactedThinkingBlock struct {
	Data   string                `json:"data"`
	Raw    json.RawMessage       `json:"-"`
	Fields map[string]FieldState `json:"-"`
}

// ModelUsage uses the CLI's camelCase keys. Totals may be cumulative: do not
// assume that summing successive result frames gives per-turn accounting.
type ModelUsage struct {
	InputTokens              int64                 `json:"inputTokens"`
	OutputTokens             int64                 `json:"outputTokens"`
	CacheReadInputTokens     int64                 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64                 `json:"cacheCreationInputTokens"`
	WebSearchRequests        int64                 `json:"webSearchRequests"`
	CostUSD                  float64               `json:"costUSD"`
	ContextWindow            int64                 `json:"contextWindow"`
	MaxOutputTokens          int64                 `json:"maxOutputTokens"`
	CanonicalModel           *string               `json:"canonicalModel,omitempty"`
	Provider                 *string               `json:"provider,omitempty"`
	Raw                      json.RawMessage       `json:"-"`
	Fields                   map[string]FieldState `json:"-"`
}
type RateLimitEvent struct {
	RateLimitInfo *RateLimitInfo        `json:"rate_limit_info"`
	UUID          string                `json:"uuid,omitempty"`
	SessionID     string                `json:"session_id,omitempty"`
	Raw           json.RawMessage       `json:"-"`
	Fields        map[string]FieldState `json:"-"`
}
type RateLimitInfo struct {
	Status                string                `json:"status"`
	ResetsAt              *int64                `json:"resetsAt,omitempty"`
	RateLimitType         *string               `json:"rateLimitType,omitempty"`
	Utilization           *float64              `json:"utilization,omitempty"`
	OverageStatus         *string               `json:"overageStatus,omitempty"`
	OverageResetsAt       *int64                `json:"overageResetsAt,omitempty"`
	OverageDisabledReason *string               `json:"overageDisabledReason,omitempty"`
	Raw                   json.RawMessage       `json:"-"`
	Fields                map[string]FieldState `json:"-"`
}
type ConversationResetEvent struct {
	NewConversationID string                `json:"new_conversation_id"`
	UUID              string                `json:"uuid,omitempty"`
	SessionID         string                `json:"session_id,omitempty"`
	Raw               json.RawMessage       `json:"-"`
	Fields            map[string]FieldState `json:"-"`
}
