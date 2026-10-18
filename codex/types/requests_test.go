package types_test

import (
	"encoding/json"
	"math"

	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestServerRequestResolvedPreservesNativeIDKinds(t *testing.T) {
	for _, raw := range []string{`"42"`, `42`, `""`, `0`, `-42`, `9007199254740993`, `9223372036854775807`, `-9223372036854775808`} {
		t.Run(raw, func(t *testing.T) {
			var resolved types.ServerRequestResolvedNotification
			schemaRoundTrip(t, &resolved, `{"threadId":"thread-original","requestId":`+raw+`,"futureMember":{"exact":9007199254740993}}`)
			require.False(t, (resolved.RequestID.Value != nil) != (raw[0] == '"') ||
				(resolved.RequestID.Integer != nil) != (raw[0] != '"') || len(resolved.RequestID.Unknown) != 0,
				"wrong native ID variant: %#v", resolved.RequestID)

		})
	}
	stringID := types.RequestID{Value: types.Ptr("42")}
	integerID := types.RequestID{Integer: types.Ptr(int64(42))}
	stringWire, err := json.Marshal(stringID)
	require.NoError(t, err)

	integerWire, err := json.Marshal(integerID)
	require.NoError(t, err)
	require.False(t, string(stringWire) != `"42"` || string(integerWire) != `42`,
		"equal-text string and integer IDs collapsed: %s / %s", stringWire, integerWire)

	for _, n := range []int64{math.MinInt64, math.MaxInt64, 9007199254740993} {
		wire, err := json.Marshal(types.RequestID{Integer: types.Ptr(n)})
		require.NoError(t, err)

		var decoded types.RequestID
		{
			err := json.Unmarshal(wire, &decoded)
			require.False(t, err != nil || decoded.Integer == nil || *decoded.Integer != n,
				"integer precision changed for %d: %#v, %v", n, decoded, err)
		}

	}
}

func TestRequestIDRejectsInvalidNativeValues(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `false`, `[]`, `{}`, `1.0`, `1e0`, `1.5`, `9223372036854775808`, `-9223372036854775809`, `"\ud800"`, `"\udc00"`} {
		t.Run(raw, func(t *testing.T) {
			original := types.RequestID{Value: types.Ptr("unchanged")}
			decoded := original
			{
				err := json.Unmarshal([]byte(raw), &decoded)
				require.Error(t, err,
					"accepted invalid native request ID %s", raw)
			}
			require.Equal(t, decoded, original,
				"failed decode modified target")
			{

				_, err := json.Marshal(types.RequestID{Unknown: json.RawMessage(raw)})
				require.Error(t, err,
					"raw escape hatch accepted invalid native request ID %s", raw)
			}

		})
	}
	for _, id := range []types.RequestID{
		{},
		{Value: types.Ptr("42"), Integer: types.Ptr(int64(42))},
		{Integer: types.Ptr(int64(42)), Unknown: json.RawMessage(`42`)},
	} {
		{
			_, err := json.Marshal(id)
			require.Error(t, err,
				"accepted empty or ambiguous request ID: %#v", id)
		}

	}
	for _, raw := range []string{`{"threadId":"t"}`, `{"requestId":42}`, `{"threadId":"t","requestId":null}`} {
		var resolved types.ServerRequestResolvedNotification
		{
			err := json.Unmarshal([]byte(raw), &resolved)
			require.Error(t, err,
				"accepted incomplete resolved notification %s", raw)
		}

	}
}

func TestApprovalNativeDecisionVariants(t *testing.T) {
	for _, raw := range []string{
		`"accept"`, `"acceptForSession"`, `"decline"`, `"cancel"`,
		`{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}}`,
		`{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"allow","host":"example.invalid"}}}`,
	} {
		t.Run("command/"+raw, func(t *testing.T) {
			schemaRoundTrip(t, new(types.CommandExecutionRequestApprovalResponse), `{"decision":`+raw+`}`)
		})
	}
	for _, raw := range []string{`"accept"`, `"acceptForSession"`, `"decline"`, `"cancel"`} {
		t.Run("file/"+raw, func(t *testing.T) {
			schemaRoundTrip(t, new(types.FileChangeRequestApprovalResponse), `{"decision":`+raw+`}`)
		})
	}
	for _, raw := range []string{
		`"approved"`, `"approved_for_session"`, `"approved_mcp_policy_amendment"`, `"timed_out"`, `"abort"`,
		`{"approved_execpolicy_amendment":{"proposed_execpolicy_amendment":["git","status"]}}`,
		`{"network_policy_amendment":{"network_policy_amendment":{"action":"deny","host":"example.invalid"}}}`,
		`{"denied":{"rejection":"Needs a narrower command"}}`,
	} {
		t.Run("legacy/"+raw, func(t *testing.T) {
			schemaRoundTrip(t, new(types.ApplyPatchApprovalResponse), `{"decision":`+raw+`}`)
			schemaRoundTrip(t, new(types.ExecCommandApprovalResponse), `{"decision":`+raw+`}`)
		})
	}
	// Unknown decision values and fields retain the package's forward-compatible
	// model policy. Runtime handling must still be chosen explicitly by callers.
	schemaRoundTrip(t, new(types.CommandExecutionRequestApprovalResponse), `{"decision":{"futureDecision":{"exact":9007199254740993}},"futureMember":false}`)
}

func TestApprovalAndInputOptionalNativeFields(t *testing.T) {
	for _, raw := range []string{
		`{"itemId":"item","threadId":"thread","turnId":"turn","startedAtMs":0}`,
		`{"itemId":"item","threadId":"thread","turnId":"turn","startedAtMs":0,"approvalId":null,"command":null,"reason":null,"availableDecisions":[]}`,
		`{"itemId":"item","threadId":"thread","turnId":"turn","startedAtMs":9007199254740993,"approvalId":"callback","kind":"writeStdin","command":"","futureMember":false}`,
	} {
		schemaRoundTrip(t, new(types.CommandExecutionRequestApprovalParams), raw)
	}
	for _, raw := range []string{
		`{"permissions":{}}`,
		`{"permissions":{},"scope":"turn","strictAutoReview":false}`,
		`{"permissions":{"network":{"enabled":true}},"scope":"session","strictAutoReview":null}`,
	} {
		schemaRoundTrip(t, new(types.PermissionsRequestApprovalResponse), raw)
	}
	schemaRoundTrip(t, new(types.ToolRequestUserInputParams), `{"itemId":"i","threadId":"t","turnId":"u","isBlocking":false,"autoResolutionMs":null,"questions":[{"id":"question-1","header":"Choose","question":"Which?","isOther":false,"isSecret":false,"options":null}]}`)
	schemaRoundTrip(t, new(types.ToolRequestUserInputResponse), `{"answers":{"question-1":{"answers":["","arbitrary text"]},"question-2":{"answers":[]}},"futureMember":9007199254740993}`)
}

func TestPermissionSpecialPathUsesKindDiscriminator(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"root"}`, `{"kind":"minimal"}`, `{"kind":"tmpdir"}`, `{"kind":"slash_tmp"}`,
		`{"kind":"project_roots","subpath":null}`,
		`{"kind":"unknown","path":"future-special","subpath":"relative"}`,
		`{"kind":"future_kind","futureMember":9007199254740993}`,
	} {
		schemaRoundTrip(t, new(types.FileSystemSpecialPath), raw)
	}
	for _, raw := range []string{`{}`, `{"type":"root"}`, `{"kind":null}`, `{"kind":42}`, `{"kind":"unknown"}`} {
		var value types.FileSystemSpecialPath
		{
			err := json.Unmarshal([]byte(raw), &value)
			require.Error(t, err,
				"accepted invalid native kind-discriminated path %s", raw)
		}

	}
}
