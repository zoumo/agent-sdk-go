package codex

import (
	"bytes"
	"encoding/json"
)

// ChildDetailPolicy selects SDK delivery of confirmed-child detail. It does not
// control native output, internal results, or model usage.
type ChildDetailPolicy string

const (
	// ChildDetailPreserve delivers all admitted native facts, as does the zero value.
	ChildDetailPreserve ChildDetailPolicy = "preserve"
	// ChildDetailSuppress omits only reviewed pure child progress. Full items,
	// approvals, lifecycle facts and mixed/unknown frames remain visible; this is
	// not a content redaction guarantee.
	ChildDetailSuppress ChildDetailPolicy = "suppress"
)

// suppressChildDetailLocked is a closed shape check against protocol 0.162.1
// (092d3acd), not a method-prefix filter. None of these shapes carries membership,
// control or lifecycle facts. Extensions at either level make the whole frame
// mixed, and are delivered unchanged. Untyped notifications use the same check.
func (s *Session) suppressChildDetailLocked(value Received) bool {
	if s.childDetailPolicy != ChildDetailSuppress || value.Kind != ReceivedEvent || value.Event == nil || value.Event.DecodeError != nil || value.Source.ThreadID == s.root || !s.member(value.Source.ThreadID) {
		return false
	}
	textField, indexField := "delta", ""
	switch value.Event.Method {
	case "item/agentMessage/delta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
	case "item/reasoning/textDelta":
		indexField = "contentIndex"
	case "item/reasoning/summaryTextDelta":
		indexField = "summaryIndex"
	case "item/reasoning/summaryPartAdded":
		textField, indexField = "", "summaryIndex"
	case "item/mcpToolCall/progress":
		textField = "message"
	default:
		return false
	}
	envelope, err := objectFields(value.Raw)
	if err != nil || len(envelope) != 2 || envelope["method"] == nil || envelope["params"] == nil {
		return false
	}
	fields, err := objectFields(value.Params)
	if err != nil {
		return false
	}
	expected := 3
	for _, key := range []string{"threadId", "turnId", "itemId"} {
		raw, present := fields[key]
		if _, ok := nativeIdentity(raw); !present || !ok {
			return false
		}
	}
	if textField != "" {
		expected++
		raw := bytes.TrimSpace(fields[textField])
		var text string
		if len(raw) == 0 || raw[0] != '"' || !validStringEscapes(raw) || json.Unmarshal(raw, &text) != nil {
			return false
		}
	}
	if indexField != "" {
		expected++
		raw := bytes.TrimSpace(fields[indexField])
		var index int64
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &index) != nil {
			return false
		}
	}
	return len(fields) == expected
}
