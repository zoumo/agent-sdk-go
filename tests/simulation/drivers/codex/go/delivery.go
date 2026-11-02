package codexsim

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
		if e := json.Unmarshal(s.Bytes(), &v); e != nil {
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
func CompareDelivery(preserved, suppressed string) error {
	p, e := deliveries(preserved)
	if e != nil {
		return e
	}
	s, e := deliveries(suppressed)
	if e != nil {
		return e
	}
	original, e := readObject(filepath.Join(filepath.Dir(preserved), "report.json"))
	if e != nil {
		return e
	}
	var selection map[string]any
	count := 0
	for _, item := range list(original["receipts"]) {
		if value, ok := mapping(item)["child-detail-selection"]; ok {
			selection = mapping(value)
			count++
		}
	}
	root := text(selection["rootId"])
	children := map[string]bool{}
	for _, child := range list(selection["childIds"]) {
		children[text(child)] = true
	}
	if count != 1 || root == "" || len(children) == 0 {
		return errors.New("recording lacks the original native child selection")
	}
	confirmed := map[string]bool{}
	for _, v := range p {
		o := mapping(v["observation"])
		if o["Reason"] == "member-confirmed" {
			id := text(o["ThreadID"])

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
		if e := json.Unmarshal([]byte(text(v["raw"])), &wire); e != nil {
			return e
		}
		source := mapping(v["source"])

		params := mapping(wire["params"])
		child := text(source["ThreadID"])
		if child == "" || child == root || !children[child] || !confirmed[child] || params["threadId"] != child {
			return errors.New("suppression removed unconfirmed or mixed Codex detail")
		}
		if !reviewedProgress(wire) {
			return errors.New("suppression removed unreviewed Codex detail")
		}
		if !only(wire, "method", "params", "emittedAtMs") {
			return errors.New("suppression removed unknown Codex fields")
		}
	}
	if pos != len(s) || removed == 0 {
		return fmt.Errorf("invalid child detail comparison: retained=%d/%d removed=%d", pos, len(s), removed)
	}
	terminals := 0
	for _, value := range s {
		if text(value["raw"]) == "" {
			continue
		}
		var wire map[string]any
		if e := json.Unmarshal([]byte(text(value["raw"])), &wire); e != nil {
			return e
		}
		if wire["method"] == "turn/completed" && mapping(wire["params"])["threadId"] == root {
			terminals++
		}
	}
	if terminals != 1 {
		return errors.New("policy comparison lacks the single original root result")
	}
	return nil
}

// Independent protocol assertions for the text and reasoning progress exercised
// by this case. Complete items, lifecycle frames and extensions are protected.
func reviewedProgress(wire map[string]any) bool {
	fields := mapping(wire["params"])
	keys := []string{"threadId", "turnId", "itemId"}
	for _, key := range keys {
		if text(fields[key]) == "" {
			return false
		}
	}
	index := ""
	hasText := true
	switch wire["method"] {
	case "item/agentMessage/delta":
	case "item/reasoning/summaryPartAdded":
		index, hasText = "summaryIndex", false
	case "item/reasoning/summaryTextDelta":
		index = "summaryIndex"
	case "item/reasoning/textDelta":
		index = "contentIndex"
	default:
		return false
	}
	if hasText {
		if _, ok := fields["delta"].(string); !ok {
			return false
		}
		keys = append(keys, "delta")
	}
	if index != "" {
		if !protocolInteger(fields[index]) {
			return false
		}
		keys = append(keys, index)
	}
	if value, exists := wire["emittedAtMs"]; exists && (!protocolInteger(value) || value.(float64) < 0) {
		return false
	}
	return len(fields) == len(keys) && only(fields, keys...)
}

func protocolInteger(value any) bool {
	n, ok := value.(float64)
	return ok && n >= -math.Exp2(63) && n < math.Exp2(63) && math.Trunc(n) == n
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
