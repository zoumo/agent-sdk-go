package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
)

func deliveries(path string) ([]map[string]any, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4<<20)
	var out []map[string]any
	for s.Scan() {
		var v map[string]any
		if e = json.Unmarshal(s.Bytes(), &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, s.Err()
}
func mapping(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func text(v any) string { s, _ := v.(string); return s }

// Compare actual Go Receive streams, including untouched mixed/unknown facts.
// This assertion belongs to the Go SDK driver, never the generic runner.
func compareDelivery(provider, preserved, suppressed string) error {
	p, e := deliveries(preserved)
	if e != nil {
		return e
	}
	s, e := deliveries(suppressed)
	if e != nil {
		return e
	}
	confirmed := map[string]bool{}
	for _, v := range p {
		o := mapping(v["observation"])
		if o["Reason"] == "member-confirmed" {
			id := text(o["ThreadID"])
			if id == "" {
				id = text(mapping(o["Source"])["association_id"])
			}
			confirmed[id] = true
		}
	}
	pos, removed := 0, 0
	for _, v := range p {
		if pos < len(s) && reflect.DeepEqual(v, s[pos]) {
			pos++
			continue
		}
		removed++
		if v["kind"] != "event" || v["origin"] != "live" {
			return errors.New("suppression removed a protected SDK fact")
		}
		var wire map[string]any
		if e = json.Unmarshal([]byte(text(v["raw"])), &wire); e != nil {
			return e
		}
		source := mapping(v["source"])
		if provider == "codex" {
			params := mapping(wire["params"])
			child := text(source["ThreadID"])
			if child == "" || !confirmed[child] || wire["method"] != "item/agentMessage/delta" || params["threadId"] != child || len(params) != 4 {
				return errors.New("suppression removed unconfirmed or mixed Codex detail")
			}
			if !only(wire, "method", "params", "emittedAtMs") {
				return errors.New("suppression removed unknown Codex fields")
			}
		} else {
			id := text(source["association_id"])
			if id == "" || !confirmed[id] || wire["type"] != "assistant" || text(wire["parent_tool_use_id"]) == "" || source["tool_use_id"] != wire["parent_tool_use_id"] {
				return errors.New("suppression removed unconfirmed Claude detail")
			}
			if !only(wire, "type", "session_id", "uuid", "parent_tool_use_id", "message", "timestamp", "subagent_type", "task_description") {
				return errors.New("suppression removed mixed Claude metadata")
			}
			message := mapping(wire["message"])
			if !only(message, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "usage", "context_management") || message["context_management"] != nil {
				return errors.New("suppression removed protected Claude message")
			}
			blocks, _ := message["content"].([]any)
			if len(blocks) == 0 {
				return errors.New("suppression removed unknown Claude content")
			}
			for _, b := range blocks {
				block := mapping(b)
				switch block["type"] {
				case "text":
					if len(block) != 2 || !only(block, "type", "text") {
						return errors.New("mixed text block")
					}
				case "thinking":
					if len(block) != 3 || !only(block, "type", "thinking", "signature") {
						return errors.New("mixed thinking block")
					}
				case "redacted_thinking":
					if len(block) != 2 || !only(block, "type", "data") {
						return errors.New("mixed redacted block")
					}
				default:
					return errors.New("suppression removed a tool or unknown content")
				}
			}
		}
	}
	if pos != len(s) || removed == 0 {
		return fmt.Errorf("invalid child detail comparison: retained=%d/%d removed=%d", pos, len(s), removed)
	}
	return nil
}
func only(v map[string]any, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k := range v {
		if !allowed[k] {
			return false
		}
	}
	return true
}
