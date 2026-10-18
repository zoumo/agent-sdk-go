package types_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
			require.False(t, err != nil || string(raw) != tc.want,
				"wire %s: %v", raw, err)

			var decoded types.ThreadResumeParams
			{
				err := json.Unmarshal(raw, &decoded)
				require.NoError(t, err)
			}
			require.Equal(t, p, decoded,
				"optional state changed: %+v", decoded)

		})
	}
	{
		_, err := json.Marshal(types.ThreadResumeParams{ThreadID: "native", ExcludeTurns: types.Null[bool]()})
		require.Error(t, err,
			"non-null bool encoded as null")
	}

}

func TestResumeFullExperimentalFields(t *testing.T) {
	raw := `{"threadId":"native","approvalPolicy":"never","approvalsReviewer":"user","baseInstructions":"","config":{},"cwd":"/synthetic","developerInstructions":null,"excludeTurns":false,"history":[{"type":"futureResponseItem","value":9007199254740993}],"initialTurnsPage":{"limit":0,"sortDirection":"asc","itemsView":"full"},"model":"mock","modelProvider":null,"path":"","permissions":null,"personality":null,"runtimeWorkspaceRoots":[],"sandbox":"read-only","serviceTier":null,"future":{"raw":9007199254740993}}`
	var p types.ThreadResumeParams
	{
		err := json.Unmarshal([]byte(raw), &p)
		require.NoError(t, err)
	}

	history, ok := p.History.Get()
	require.False(t, !ok || len(history) != 1 || string(history[0].Unknown) != `{"type":"futureResponseItem","value":9007199254740993}`,
		"history union lost")
	require.False(t, !p.Permissions.IsNull() || p.Path.ValueOr("unset") != "" || !p.RuntimeWorkspaceRoots.IsSet(),
		"experimental params lost")

	encoded, err := json.Marshal(p)
	require.NoError(t, err)

	decode := func(raw []byte) any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		{
			err := decoder.Decode(&value)
			require.NoError(t, err)
		}

		return value
	}
	require.Equal(t, decode([]byte(raw)), decode(encoded),
		"experimental fields changed: %s", encoded)

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
			{
				err := json.Unmarshal([]byte(tc.raw), tc.target)
				require.Error(t, err,
					"invalid native model accepted")
			}

		})
	}
}
