// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

// Financial uncertainty acceptance never attests policy isolation or grants a launch.
func TestAcceptedFinancialUncertaintyRetainsIsolationGate(t *testing.T) {
	err := recorder.Preflight(recorder.AcceptFinancialUncertainty)
	require.ErrorIs(t, err, recorder.ErrPolicyIsolation)
	require.NotErrorIs(t, err, recorder.ErrFinancialCap)
	require.ErrorIs(t, recorder.Preflight(), recorder.ErrFinancialCap)
	require.Error(t, recorder.Preflight(recorder.FinancialPolicy("unknown")))
}
