// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestApprovalPolicyValuesRoundTripThroughAskForApproval(t *testing.T) {
	for _, policy := range []types.ApprovalPolicy{
		types.ApprovalPolicyUntrusted,
		types.ApprovalPolicyOnRequest,
		types.ApprovalPolicyNever,
	} {
		t.Run(string(policy), func(t *testing.T) {
			value := types.AskForApproval{Value: types.Ptr(policy)}
			raw, err := json.Marshal(value)
			require.NoError(t, err)
			require.JSONEq(t, `"`+string(policy)+`"`, string(raw))

			var decoded types.AskForApproval
			require.NoError(t, json.Unmarshal(raw, &decoded))
			require.NotNil(t, decoded.Value)
			require.Equal(t, policy, *decoded.Value)
			require.Nil(t, decoded.Granular)
			require.Nil(t, decoded.Unknown)
		})
	}
}

func TestAskForApprovalPreservesGranularAndUnknownStrings(t *testing.T) {
	granular := `{"granular":{"mcp_elicitations":true,"request_permissions":false,"rules":true,"sandbox_approval":false,"future":7},"future":null}`
	var decoded types.AskForApproval
	require.NoError(t, json.Unmarshal([]byte(granular), &decoded))
	require.NotNil(t, decoded.Granular)
	require.Nil(t, decoded.Value)
	raw, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.JSONEq(t, granular, string(raw))

	require.NoError(t, json.Unmarshal([]byte(`"future-policy"`), &decoded))
	require.NotNil(t, decoded.Value)
	require.Equal(t, types.ApprovalPolicy("future-policy"), *decoded.Value)
	raw, err = json.Marshal(decoded)
	require.NoError(t, err)
	require.JSONEq(t, `"future-policy"`, string(raw))
}

func TestDeprecatedAskForApprovalValueAliases(t *testing.T) {
	require.Equal(t, types.ApprovalPolicyNever, types.AskForApprovalValueNever)
	require.Equal(t, types.ApprovalPolicyOnRequest, types.AskForApprovalValueOnRequest)

	value := types.AskForApproval{Value: types.Ptr(types.AskForApprovalValueUntrusted)}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, `"untrusted"`, string(raw))
}
