package agentcodex_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zoumo/agent-sdk-go/agent/codex"
)

func TestStableErrorReasons(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"event decode failed", agentcodex.ErrorReasonEventDecodeFailed, "event_decode_failed"},
		{"execution options not encodable", agentcodex.ErrorReasonExecutionOptionsNotEncodable, "execution_options_not_encodable"},
		{"form outcome unsupported", agentcodex.ErrorReasonFormOutcomeUnsupported, "form_outcome_unsupported"},
		{"invalid native event", agentcodex.ErrorReasonInvalidNativeEvent, "invalid_native_event"},
		{"permission cancel unsupported", agentcodex.ErrorReasonPermissionCancelUnsupported, "permission_cancel_unsupported"},
		{"core start failed", agentcodex.ErrorReasonCoreStartFailed, "core_start_failed"},
		{"start or steer identity missing", agentcodex.ErrorReasonStartOrSteerIdentityMissing, "start_or_steer_identity_missing"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.got)
		})
	}
}
