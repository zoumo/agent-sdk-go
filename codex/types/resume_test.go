package types_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestResumeOptionalNativeSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tier    types.Optional[string]
		exclude types.Optional[bool]
		want    string
	}{
		{"omitted", types.Optional[string]{}, types.Optional[bool]{}, `{"threadId":"native"}`},
		{"clear", types.Null[string](), types.Some(false), `{"excludeTurns":false,"serviceTier":null,"threadId":"native"}`},
		{"override", types.Some("fast"), types.Some(true), `{"excludeTurns":true,"serviceTier":"fast","threadId":"native"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := types.ThreadResumeParams{ThreadID: "native", ServiceTier: tc.tier, ExcludeTurns: tc.exclude}
			raw, err := json.Marshal(p)
			if err != nil || string(raw) != tc.want {
				t.Fatalf("wire %s: %v", raw, err)
			}
			var decoded types.ThreadResumeParams
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p, decoded) {
				t.Fatalf("optional state changed: %+v", decoded)
			}
		})
	}
	if _, err := json.Marshal(types.ThreadResumeParams{ThreadID: "native", ExcludeTurns: types.Null[bool]()}); err == nil {
		t.Fatal("non-null bool encoded as null")
	}
}

func TestResumeFullExperimentalFields(t *testing.T) {
	raw := `{"threadId":"native","approvalPolicy":"never","approvalsReviewer":"user","baseInstructions":"","config":{},"cwd":"/synthetic","developerInstructions":null,"excludeTurns":false,"history":[{"type":"futureResponseItem","value":9007199254740993}],"initialTurnsPage":{"limit":0,"sortDirection":"asc","itemsView":"full"},"model":"mock","modelProvider":null,"path":"","permissions":null,"personality":null,"runtimeWorkspaceRoots":[],"sandbox":"read-only","serviceTier":null,"future":{"raw":9007199254740993}}`
	var p types.ThreadResumeParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	history, ok := p.History.Get()
	if !ok || len(history) != 1 || string(history[0].Unknown) != `{"type":"futureResponseItem","value":9007199254740993}` {
		t.Fatal("history union lost")
	}
	if !p.Permissions.IsNull() || p.Path.ValueOr("unset") != "" || !p.RuntimeWorkspaceRoots.IsSet() {
		t.Fatal("experimental params lost")
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode([]byte(raw)), decode(encoded)) {
		t.Fatalf("experimental fields changed: %s", encoded)
	}
}

func TestResumeAndUnsubscribeRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		target    any
	}{
		{"resume missing ID", `{}`, new(types.ThreadResumeParams)},
		{"resume null ID", `{"threadId":null}`, new(types.ThreadResumeParams)},
		{"exclude null", `{"threadId":"native","excludeTurns":null}`, new(types.ThreadResumeParams)},
		{"negative page limit", `{"threadId":"native","initialTurnsPage":{"limit":-1}}`, new(types.ThreadResumeParams)},
		{"unsubscribe missing ID", `{}`, new(types.ThreadUnsubscribeParams)},
		{"unsubscribe null ID", `{"threadId":null}`, new(types.ThreadUnsubscribeParams)},
		{"unsubscribe missing status", `{}`, new(types.ThreadUnsubscribeResponse)},
		{"unsubscribe null status", `{"status":null}`, new(types.ThreadUnsubscribeResponse)},
		{"resume incomplete result", `{"thread":{"id":"native"}}`, new(types.ThreadResumeResponse)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.raw), tc.target); err == nil {
				t.Fatal("invalid native model accepted")
			}
		})
	}
}
