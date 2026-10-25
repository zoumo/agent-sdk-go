// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"encoding/json"
	"slices"
)

// ChildDetailPolicy selects SDK delivery of confirmed-child detail. It never
// changes native forwarding, internal results-to-parent or history queries.
type ChildDetailPolicy string

const (
	// ChildDetailPreserve retains current event delivery; the empty value is equivalent.
	ChildDetailPreserve ChildDetailPolicy = "preserve"
	// ChildDetailSuppress omits only reviewed pure child detail before FIFO admission.
	// Mixed and unknown frames remain visible; this is not a privacy guarantee.
	ChildDetailSuppress ChildDetailPolicy = "suppress"
)

// pureChildDetail is a closed, stateless allowlist over already parsed frames.
// The caller must finish membership, aliases, parent facts and native control
// processing first. Even a recognized typed field outside these shapes is mixed.
func pureChildDetail(e Event) bool {
	switch e.Type {
	case "assistant":
		if e.Assistant == nil || detailObject(e.Raw, "type", "session_id", "uuid", "parent_tool_use_id", "message") == nil {
			return false
		}
		m := detailObject(e.Assistant.Raw, "type", "id", "role", "model", "content", "stop_reason", "stop_sequence", "usage")
		if m == nil {
			return false
		}
		for _, key := range []string{"type", "id", "role", "model", "stop_reason", "stop_sequence"} {
			if raw, ok := m[key]; ok && !detailString(raw, true) {
				return false
			}
		}
		if raw, ok := m["type"]; ok && string(raw) != `"message"` {
			return false
		}
		if raw, ok := m["role"]; ok && string(raw) != `"assistant"` {
			return false
		}
		if raw, ok := m["usage"]; ok && !detailUsage(raw) {
			return false
		}
		// A string content shape is supported by the decoder but is not the reviewed
		// assistant content-block schema. Keep it conservatively.
		if e.Assistant.Content.Text != nil || len(e.Assistant.Content.Blocks) == 0 {
			return false
		}
		for _, b := range e.Assistant.Content.Blocks {
			var fields map[string]json.RawMessage
			var required []string
			switch b.Type {
			case "text":
				fields = detailObject(b.Raw, "type", "text")
				required = []string{"text"}
			case "thinking":
				fields = detailObject(b.Raw, "type", "thinking", "signature")
				required = []string{"thinking", "signature"}
			case "redacted_thinking":
				fields = detailObject(b.Raw, "type", "data")
				required = []string{"data"}
			default:
				return false
			}
			if fields == nil {
				return false
			}
			for _, key := range required {
				if !detailString(fields[key], false) {
					return false
				}
			}
		}
		return true
	case "stream_event":
		if e.Stream == nil || e.Stream.Delta == nil || detailObject(e.Raw, "type", "session_id", "uuid", "parent_tool_use_id", "event") == nil {
			return false
		}
		event := detailObject(e.Stream.Event, "type", "index", "delta")
		if event == nil || e.Stream.Delta.Type != "content_block_delta" {
			return false
		}
		if !detailCount(event["index"]) {
			return false
		}
		var delta map[string]json.RawMessage
		var field string
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(event["delta"], &kind) != nil {
			return false
		}
		switch kind.Type {
		case "text_delta":
			field = "text"
		case "thinking_delta":
			field = "thinking"
		case "signature_delta":
			field = "signature"
		case "input_json_delta":
			field = "partial_json"
		default:
			return false
		}
		delta = detailObject(event["delta"], "type", field)
		return delta != nil && detailString(delta[field], false)
	}
	return false
}

// detailObject rejects every unreviewed key, including differently cased keys.
// ParseEvent already rejects duplicate keys, invalid JSON/UTF-8 and bad native
// typed values; this additional check never rewrites the original bytes.
func detailObject(raw json.RawMessage, keys ...string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil
	}
	for key := range fields {
		if !slices.Contains(keys, key) {
			return nil
		}
	}
	return fields
}
func detailString(raw json.RawMessage, nullable bool) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nullable
	}
	var value string
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil
}
func detailCount(raw json.RawMessage) bool {
	var n int64
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, &n) == nil && n >= 0
}
func detailUsage(raw json.RawMessage) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	fields := detailObject(raw, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")
	if fields == nil {
		return false
	}
	for _, count := range fields {
		if !detailCount(count) {
			return false
		}
	}
	return true
}
