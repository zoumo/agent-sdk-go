package types_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/zoumo/codex-sdk-go/types"
)

func decodedJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func schemaRoundTrip(t *testing.T, target any, raw string) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		t.Fatalf("decode %T: %v", target, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("encode %T: %v", target, err)
	}
	if !reflect.DeepEqual(decodedJSON(t, []byte(raw)), decodedJSON(t, encoded)) {
		t.Fatalf("%T round trip changed data:\nin: %s\nout: %s", target, raw, encoded)
	}
}

func TestOptionalOmittedNullAndZero(t *testing.T) {
	var omitted types.ThreadStartParams
	for _, tt := range []struct {
		name  string
		value types.ThreadStartParams
		want  string
	}{
		{"omitted", omitted, `{}`},
		{"null", types.ThreadStartParams{ServiceTier: types.Null[string]()}, `{"serviceTier":null}`},
		{"empty", types.ThreadStartParams{ServiceTier: types.Some("")}, `{"serviceTier":""}`},
		{"false", types.ThreadStartParams{Ephemeral: types.Some(false)}, `{"ephemeral":false}`},
		{"empty array", types.ThreadStartParams{Environments: types.Some([]types.TurnEnvironmentParams{})}, `{"environments":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
			var round types.ThreadStartParams
			if err := json.Unmarshal(got, &round); err != nil {
				t.Fatal(err)
			}
			if round.ServiceTier.IsSet() != tt.value.ServiceTier.IsSet() || round.ServiceTier.IsNull() != tt.value.ServiceTier.IsNull() {
				t.Fatal("optional state changed")
			}
		})
	}
	if _, ok := omitted.ServiceTier.Get(); ok {
		t.Fatal("omitted value reported present")
	}
	if got := omitted.ServiceTier.ValueOr("fallback"); got != "fallback" {
		t.Fatal(got)
	}
	if got, ok := types.Some(false).Get(); !ok || got {
		t.Fatal("present false lost")
	}
}

func TestCompleteExperimentalStartFields(t *testing.T) {
	params := types.ThreadStartParams{
		AllowProviderModelFallback: types.Some(false),
		RuntimeWorkspaceRoots:      types.Some([]types.AbsolutePathBuf{"/synthetic"}),
		Permissions:                types.Some("workspace"),
		HistoryMode:                types.Some(types.ThreadHistoryModePaginated),
		ProjectID:                  types.Null[string](),
		DaybreakEnabled:            types.Some(false),
		Environments:               types.Some([]types.TurnEnvironmentParams{{Cwd: "/synthetic", EnvironmentID: "environment-1"}}),
		DynamicTools:               types.Some([]types.DynamicToolSpec{{Function: &types.FunctionDynamicToolSpec{Name: "test", Description: "synthetic tool", InputSchema: json.RawMessage(`{"type":"object"}`)}}}),
		SelectedCapabilityRoots:    types.Some([]types.SelectedCapabilityRoot{}),
		MockExperimentalField:      types.Some("synthetic"),
		ExperimentalRawEvents:      types.Some(false),
		MultiAgentMode:             types.Some(types.MultiAgentMode{Value: types.Ptr(types.MultiAgentModeValueExplicitRequestOnly)}),
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"allowProviderModelFallback", "runtimeWorkspaceRoots", "permissions", "historyMode", "projectId", "daybreakEnabled", "environments", "dynamicTools", "selectedCapabilityRoots", "mockExperimentalField", "experimentalRawEvents", "multiAgentMode"} {
		if _, found := fields[key]; !found {
			t.Errorf("missing native field %s", key)
		}
	}
	schemaRoundTrip(t, new(types.ThreadStartParams), string(encoded))
}

func TestKnownAndFutureUnionVariants(t *testing.T) {
	raw := `{"type":"agentMessage","id":"native-item","text":"answer","phase":"final_answer","future":{"large":9007199254740993},"memoryCitation":null}`
	var item types.ThreadItem
	schemaRoundTrip(t, &item, raw)
	if item.AgentMessage == nil || item.AgentMessage.ID != "native-item" || item.AgentMessage.Text != "answer" {
		t.Fatal("missing typed agent message")
	}
	if !item.AgentMessage.MemoryCitation.IsNull() {
		t.Fatal("null citation lost")
	}
	if string(item.AgentMessage.UnknownFields["future"]) != `{"large":9007199254740993}` {
		t.Fatal("unknown field lost")
	}
	future := `{"type":"futureItem","id":"future-id","counter":9007199254740993}`
	schemaRoundTrip(t, &item, future)
	if item.AgentMessage != nil || string(item.Unknown) != future {
		t.Fatal("union replacement or fallback failed")
	}
	created := types.ThreadItem{AgentMessage: &types.AgentMessageThreadItem{ID: "native", Text: "test"}}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"agentMessage"`) {
		t.Fatal("constant discriminator not emitted")
	}
}

func TestMalformedKnownDataRejected(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		target    func() any
	}{
		{"missing required", `{}`, func() any { return new(types.InitializeParams) }},
		{"null scalar", `{"threadId":null}`, func() any { return new(types.ThreadReadParams) }},
		{"null optional nonnullable", `{"threadId":"id","includeTurns":null}`, func() any { return new(types.ThreadReadParams) }},
		{"wrong scalar", `{"threadId":23}`, func() any { return new(types.ThreadReadParams) }},
		{"known variant missing text", `{"type":"agentMessage","id":"item"}`, func() any { return new(types.ThreadItem) }},
		{"known variant wrong text", `{"type":"agentMessage","id":"item","text":2}`, func() any { return new(types.ThreadItem) }},
		{"missing discriminator", `{"id":"item"}`, func() any { return new(types.ThreadItem) }},
		{"null discriminator", `{"type":null}`, func() any { return new(types.ThreadItem) }},
		{"null scalar array element", `{"runtimeWorkspaceRoots":[null]}`, func() any { return new(types.ThreadStartParams) }},
		{"image missing source", `{"type":"image"}`, func() any { return new(types.UserInput) }},
		{"image null source", `{"type":"image","url":null}`, func() any { return new(types.UserInput) }},
		{"null object", `null`, func() any { return new(types.ThreadStartParams) }},
		{"null union", `null`, func() any { return new(types.ThreadItem) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.raw), tt.target()); err == nil {
				t.Fatal("malformed known data accepted")
			}
		})
	}
}

func TestAtomicDecodeAndEncodingValidation(t *testing.T) {
	p := types.ThreadReadParams{ThreadID: "original", IncludeTurns: types.Some(true)}
	if err := json.Unmarshal([]byte(`{"threadId":"replacement","includeTurns":"wrong"}`), &p); err == nil {
		t.Fatal("expected error")
	}
	if p.ThreadID != "original" || !p.IncludeTurns.ValueOr(false) {
		t.Fatal("failed decode modified receiver")
	}
	if _, err := json.Marshal(types.ThreadReadParams{ThreadID: "id", IncludeTurns: types.Null[bool]()}); err == nil {
		t.Fatal("null nonnullable value encoded")
	}
	if _, err := json.Marshal(types.ThreadItem{}); err == nil {
		t.Fatal("empty union encoded")
	}
	if _, err := json.Marshal(types.ThreadItem{Unknown: json.RawMessage(`{}`), AgentMessage: &types.AgentMessageThreadItem{}}); err == nil {
		t.Fatal("ambiguous union encoded")
	}
	if _, err := json.Marshal(types.ThreadReadParams{ThreadID: "id", UnknownFields: map[string]json.RawMessage{"threadId": json.RawMessage(`"override"`)}}); err == nil {
		t.Fatal("unknown field overwrote known member")
	}
}

func ExampleOptional() {
	params := types.ThreadStartParams{
		Model:       types.Some("configured-model"),
		ServiceTier: types.Null[string](),
		Ephemeral:   types.Some(false),
	}
	_, _ = json.Marshal(params)
	// The model and false value are present, serviceTier is null, and every
	// other optional field is omitted.
}

func TestRejectLossyJSONDecoding(t *testing.T) {
	for _, raw := range []string{
		`{"threadId":"one","threadId":"two"}`,
		`{"threadId":"one","thread\u0049d":"two"}`,
		`{"threadId":"one","unknown":{"key":1,"key":2}}`,
		`{"threadId":"\ud800"}`,
		`{"threadId":"\udc00"}`,
		`{"threadId":"\ud800\u0041"}`,
		"{\"threadId\":\"" + string([]byte{0xff}) + "\"}",
	} {
		var params types.ThreadReadParams
		if err := json.Unmarshal([]byte(raw), &params); err == nil {
			t.Errorf("lossy decoding accepted for %q", raw)
		}
	}
	for _, raw := range []string{
		`{"threadId":"\ud83d\ude00"}`,
		`{"threadId":"literal \\ud800 text"}`,
		`{"threadId":"unicode-😀"}`,
	} {
		schemaRoundTrip(t, new(types.ThreadReadParams), raw)
	}
}

func TestRejectLossyJSONEncoding(t *testing.T) {
	invalid := string([]byte{0xff})
	for _, value := range []any{
		types.ThreadReadParams{ThreadID: invalid},
		types.ThreadStartParams{Model: types.Some(invalid)},
		types.ThreadStartParams{Config: types.Some(map[string]json.RawMessage{invalid: json.RawMessage(`null`)})},
		types.ThreadStartParams{UnknownFields: map[string]json.RawMessage{"future": json.RawMessage(`{"a":1,"a":2}`)}},
		types.ThreadItem{Unknown: json.RawMessage(`{"type":"future","id":"\ud800"}`)},
		types.SessionSource{Value: types.Ptr(types.SessionSourceValue(invalid))},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("lossy JSON encoding accepted for %T", value)
		}
	}
}
