package types_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func decodedJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	return value
}

func schemaRoundTrip(t *testing.T, target any, raw string) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(raw), target), "decode %T", target)
	encoded, err := json.Marshal(target)
	require.NoError(t, err, "encode %T", target)
	require.Equal(t, decodedJSON(t, []byte(raw)), decodedJSON(t, encoded),
		"%T round trip changed data:\nin: %s\nout: %s", target, raw, encoded)
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
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got),
				"got %s, want %s", got, tt.want)

			var round types.ThreadStartParams
			{
				err := json.Unmarshal(got, &round)
				require.NoError(t, err)
			}
			require.False(t, round.ServiceTier.IsSet() != tt.value.ServiceTier.IsSet() || round.ServiceTier.IsNull() != tt.value.ServiceTier.IsNull(),
				"optional state changed")

		})
	}
	{
		_, ok := omitted.ServiceTier.Get()
		require.False(t, ok,
			"omitted value reported present")
	}
	{

		got := omitted.ServiceTier.ValueOr("fallback")
		require.Equal(t, "fallback", got,
			got)
	}
	{

		got, ok := types.Some(false).Get()
		require.False(t, !ok || got,
			"present false lost")
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
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	{
		err := json.Unmarshal(encoded, &fields)
		require.NoError(t, err)
	}

	for _, key := range []string{"allowProviderModelFallback", "runtimeWorkspaceRoots", "permissions", "historyMode", "projectId", "daybreakEnabled", "environments", "dynamicTools", "selectedCapabilityRoots", "mockExperimentalField", "experimentalRawEvents", "multiAgentMode"} {
		{
			_, found := fields[key]
			assert.True(t, found,
				"missing native field %s", key)
		}

	}
	schemaRoundTrip(t, new(types.ThreadStartParams), string(encoded))
}

func TestKnownAndFutureUnionVariants(t *testing.T) {
	raw := `{"type":"agentMessage","id":"native-item","text":"answer","phase":"final_answer","future":{"large":9007199254740993},"memoryCitation":null}`
	var item types.ThreadItem
	schemaRoundTrip(t, &item, raw)
	require.False(t, item.AgentMessage == nil || item.AgentMessage.ID != "native-item" || item.AgentMessage.Text != "answer",
		"missing typed agent message")
	require.True(t, item.AgentMessage.MemoryCitation.IsNull(),
		"null citation lost")
	require.Equal(t, `{"large":9007199254740993}`, string(item.AgentMessage.UnknownFields["future"]),
		"unknown field lost")

	future := `{"type":"futureItem","id":"future-id","counter":9007199254740993}`
	schemaRoundTrip(t, &item, future)
	require.False(t, item.AgentMessage != nil || string(item.Unknown) != future,
		"union replacement or fallback failed")

	created := types.ThreadItem{AgentMessage: &types.AgentMessageThreadItem{ID: "native", Text: "test"}}
	encoded, err := json.Marshal(created)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"type":"agentMessage"`,
		"constant discriminator not emitted")

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
			{
				err := json.Unmarshal([]byte(tt.raw), tt.target())
				require.Error(t, err,
					"malformed known data accepted")
			}

		})
	}
}

func TestAtomicDecodeAndEncodingValidation(t *testing.T) {
	t.Run("atomic decode", func(t *testing.T) {
		original := types.ThreadReadParams{ThreadID: "original", IncludeTurns: types.Some(true)}
		decoded := original
		require.Error(t, json.Unmarshal([]byte(`{"threadId":"replacement","includeTurns":"wrong"}`), &decoded))
		assert.Equal(t, original, decoded, "failed decode modified receiver")
	})
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"null nonnullable", types.ThreadReadParams{ThreadID: "id", IncludeTurns: types.Null[bool]()}},
		{"empty union", types.ThreadItem{}},
		{"ambiguous union", types.ThreadItem{Unknown: json.RawMessage(`{}`), AgentMessage: &types.AgentMessageThreadItem{}}},
		{"unknown field overwrites known member", types.ThreadReadParams{ThreadID: "id", UnknownFields: map[string]json.RawMessage{"threadId": json.RawMessage(`"override"`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := json.Marshal(tc.value)
			assert.Error(t, err, "invalid encoding accepted")
		})
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
		{
			err := json.Unmarshal([]byte(raw), &params)
			assert.Error(t, err,
				"lossy decoding accepted for %q", raw)
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
		{
			_, err := json.Marshal(value)
			assert.Error(t, err,
				"lossy JSON encoding accepted for %T", value)
		}

	}
}
