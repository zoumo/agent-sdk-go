// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestReceiveConcreteEventsAndNativeJSON(t *testing.T) {
	c := openFixture(t, "tree")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := c.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := first.(ObservationState)
	if !ok || state.State != "unverified" {
		t.Fatalf("first=%#v", first)
	}
	associated, created := false, false
	frames := []string{creationInit, creationRequest("call", "Agent", creationInput, ""), creationTask("child", "call", "false")}
	for _, origin := range []string{"", `,"origin":null`, `,"origin":{"kind":"future","extra":7}`} {
		frames = append(frames, ` {"type":"result","subtype":"success","session_id":"s","future":true`+origin+`} `)
	}
	for _, frame := range frames {
		treeEmit(t, c, frame)
		for {
			received, err := c.Receive(ctx)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(received)
			if err != nil {
				t.Fatal(err)
			}
			switch event := received.(type) {
			case Event:
				if string(event.Raw) != frame {
					t.Fatalf("Raw changed: %q", event.Raw)
				}
				var before, after any
				if json.Unmarshal([]byte(frame), &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("native JSON changed: %s", encoded)
				}
				if event.Subtype == "task_started" && (!associated || !created) {
					t.Fatal("child preceded relationship notifications")
				}
				goto nextFrame
			case SubagentAssociated:
				associated = true
				if event.Source.TaskID != "child" {
					t.Fatal(event)
				}
			case SubagentCreated:
				if !associated {
					t.Fatal("creation preceded association")
				}
				created = true
			case ObservationState:
			default:
				t.Fatalf("unexpected public event %T", received)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(encoded, &fields) != nil || fields["origin"] != nil || fields["native"] != nil || fields["kind"] != nil {
				t.Fatalf("derived envelope classification survived: %s", encoded)
			}
		}
	nextFrame:
	}
	if !associated || !created {
		t.Fatal("missing derived convenience events")
	}
}
