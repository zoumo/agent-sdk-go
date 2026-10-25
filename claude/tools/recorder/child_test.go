// SPDX-License-Identifier: Apache-2.0
package recorder_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/tools/mock"
	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func TestChildGateIsIndependentAndCannotBeAssertedAway(t *testing.T) {
	t.Setenv("CLAUDE_CHILD_POLICY_VERIFIED", "1")
	require.ErrorIs(t, recorder.Preflight(), recorder.ErrChildEnforcement)
	require.ErrorIs(t, recorder.Preflight(), recorder.ErrPolicyIsolation)
	require.ErrorIs(t, recorder.Preflight(), recorder.ErrFinancialCap)
}

func TestChildPermissionCannotUseRootReadAllowance(t *testing.T) {
	for _, scene := range []string{"s1", "r2"} {
		for name, raw := range map[string]string{
			"nested":   `{"type":"control_request","request_id":"child-read","request":{"subtype":"can_use_tool","tool_name":"Read","tool_use_id":"read","agent_id":"unverified","input":{"file_path":"numbers.txt"}}}`,
			"envelope": `{"type":"control_request","request_id":"child-read","agent_id":"unverified","parent_tool_use_id":"spawn","request":{"subtype":"can_use_tool","tool_name":"Read","tool_use_id":"read","input":{"file_path":"numbers.txt"}}}`,
		} {
			t.Run(scene+"/"+name, func(t *testing.T) {

				base, err := filepath.EvalSymlinks(t.TempDir())
				require.NoError(t, err)
				w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), scene)
				require.NoError(t, err)
				l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
				require.NoError(t, err)
				require.NoError(t, l.Reserve(scene))
				plan, err := recorder.Plan(scene)
				require.NoError(t, err)
				frames := append(initialize(), user(plan.Prompts[0]))
				frames = append(frames,
					native("server", "control", raw),
					native("client", "control", `{"type":"control_response","response":{"subtype":"success","request_id":"child-read","response":{"behavior":"deny","message":"outside exact synthetic story policy"}}}`),
					mock.Frame{Direction: "disconnect", Channel: "protocol"})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				s := openSynthetic(t, ctx, w, frames)
				var observations bytes.Buffer
				require.ErrorContains(t, recorder.Drive(ctx, s, scene, w, l, &observations), "out-of-policy permission")
			})
		}
	}
}

func TestRootDriverRejectsChildFactsBeforeClaimingSuccess(t *testing.T) {
	for _, raw := range []string{
		`{"type":"system","subtype":"task_notification","session_id":"root","task_id":"late","status":"completed"}`,
		`{"type":"system","subtype":"background_tasks_changed","session_id":"root","tasks":[]}`,
		`{"type":"system","subtype":"task_updated","session_id":"root","task_id":"nested"}`,
		`{"type":"assistant","session_id":"root","agent_id":"unknown","message":{"content":[{"type":"text","text":"ALPHA"}]}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			w, err := recorder.PrepareWorkspace(filepath.Join(base, "capture"), "r1")
			require.NoError(t, err)
			l, err := recorder.OpenLedger(filepath.Join(base, "ledger"))
			require.NoError(t, err)
			require.NoError(t, l.Reserve("r1"))
			frames := append(initialize(), user("Reply with the word Ready."))
			frames = append(frames, terminal(false)[0], native("server", "protocol", raw))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := openSynthetic(t, ctx, w, frames)
			var observations bytes.Buffer
			require.ErrorIs(t, recorder.Drive(ctx, s, "r1", w, l, &observations), recorder.ErrChildEnforcement)
			require.Error(t, l.Reserve("r2"))
		})
	}
}
