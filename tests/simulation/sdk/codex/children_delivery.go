package codexsim

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

func readObject(path string) (map[string]any, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var v map[string]any
	e = json.Unmarshal(b, &v)
	return v, e
}
func list(v any) []any { a, _ := v.([]any); return a }

// Verify actual public Go Receive delivery against native child facts. Source
// reports supply expectations, not a replacement event stream.
func ValidateChildDelivery(root string, background bool) error {
	received, e := deliveries(filepath.Join(root, "deliveries.jsonl"))
	if e != nil {
		return e
	}
	frames, e := deliveries(filepath.Join(root, "raw/raw.jsonl"))
	if e != nil {
		return e
	}
	original, e := readObject(filepath.Join(root, "report.json"))
	if e != nil {
		return e
	}
	parents := map[string]string{}
	rootID := ""

	for _, v := range list(original["childEvidence"]) {
		item := mapping(v)
		parents[text(item["childThreadId"])] = text(item["parentThreadId"])
	}
	for _, v := range list(original["receipts"]) {
		item := mapping(v)
		for _, name := range []string{"native-child/read", "topology/thread-read"} {
			thread := mapping(mapping(item[name])["thread"])
			if text(thread["parentThreadId"]) != "" {
				parents[text(thread["id"])] = text(thread["parentThreadId"])
			}
		}
	}
	for _, parent := range parents {
		if _, child := parents[parent]; !child {
			if rootID != "" && rootID != parent {
				return errors.New("child topology has multiple roots")
			}
			rootID = parent
		}
	}

	if rootID == "" || len(parents) == 0 {
		return errors.New("recording lacks confirmed native child identities")
	}
	members := map[string]bool{}
	relations := map[string]string{}
	for _, v := range received {
		o := mapping(v["observation"])
		id, parent := text(o["ThreadID"]), text(o["ParentThreadID"])

		if o["Reason"] == "member-confirmed" {
			members[id] = true
		}
		if o["Reason"] == "parent-confirmed" {
			relations[id] = parent
		}
	}
	for child, parent := range parents {
		if child == "" || !members[child] || relations[child] != parent {
			return errors.New("Go child membership or parent relationship differs from native evidence")
		}
	}
	type event struct {
		position int
		wire     map[string]any
	}
	var events []event
	for i, v := range received {
		if v["kind"] != "event" || v["origin"] != "live" {
			continue
		}
		var w map[string]any
		if e = json.Unmarshal([]byte(text(v["raw"])), &w); e != nil {
			return e
		}
		events = append(events, event{i, w})
	}
	rootPosition := -1
	for _, v := range events {
		w := v.wire

		if w["method"] == "turn/completed" && mapping(w["params"])["threadId"] == rootID {
			rootPosition = v.position
			break
		}

	}
	expected, afterRoot := 0, false
	for _, f := range frames {
		if f["direction"] != "s2c" {
			continue
		}
		w := mapping(f["wire"])
		id := text(mapping(w["params"])["threadId"])
		terminal := w["method"] == "turn/completed"

		if _, ok := parents[id]; !ok || !terminal {
			continue
		}
		expected++
		found := false
		for _, v := range events {
			if reflect.DeepEqual(w, v.wire) {
				found = true
				afterRoot = afterRoot || (rootPosition >= 0 && v.position > rootPosition)
				break
			}
		}
		if !found {
			return errors.New("Go stream omitted or changed native child completion")
		}
	}
	if expected == 0 {
		return errors.New("recording lacks native child completion")
	}
	if background && !afterRoot {
		return errors.New("Go stream lacks native child completion after root completion")
	}

	return nil
}
