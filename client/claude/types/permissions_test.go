// SPDX-License-Identifier: Apache-2.0

package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestPermissionModeValues(t *testing.T) {
	modes := []types.PermissionMode{
		types.PermissionModeDefault,
		types.PermissionModeAcceptEdits,
		types.PermissionModeBypassPermissions,
		types.PermissionModePlan,
		types.PermissionModeDontAsk,
		types.PermissionModeAuto,
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			require.True(t, mode.Valid())
			raw, err := json.Marshal(mode)
			require.NoError(t, err)
			require.JSONEq(t, `"`+string(mode)+`"`, string(raw))
			var decoded types.PermissionMode
			require.NoError(t, json.Unmarshal(raw, &decoded))
			require.Equal(t, mode, decoded)
		})
	}
	for _, mode := range []types.PermissionMode{"", "manual", "future"} {
		require.Falsef(t, mode.Valid(), "%q must not be accepted by the pinned SDK", mode)
	}
}

func TestPermissionUpdateSetModeWireValue(t *testing.T) {
	raw, err := json.Marshal(types.PermissionUpdate{
		Type:        "setMode",
		Mode:        types.PermissionModeAuto,
		Destination: "session",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"setMode","mode":"auto","destination":"session"}`, string(raw))
}
