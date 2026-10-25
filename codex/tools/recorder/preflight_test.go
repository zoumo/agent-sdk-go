package recorder_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
)

// Preflight is an offline public tooling seam. Accepting financial uncertainty
// must never silently satisfy the independent authentication/isolation gate.
func TestAcceptedCostUncertaintyStillRequiresTechnicalIsolation(t *testing.T) {
	p := recorder.LaunchProfile{Scene: "r1", Model: "gpt-6.1-sol", Effort: "high"}
	require.ErrorIs(t, recorder.Preflight(p), recorder.ErrFinancialGate)
	p.CostPolicy = recorder.AcceptUnmeteredCost
	p.CostReview = "synthetic operator risk review"
	require.ErrorIs(t, recorder.Preflight(p), recorder.ErrIsolationGate)
	require.NotErrorIs(t, recorder.Preflight(p), recorder.ErrFinancialGate)
	p.Scene = "r4"
	p.SavedRootID = "fresh-synthetic-root"
	require.ErrorIs(t, recorder.Preflight(p), recorder.ErrIsolationGate)
}

func TestRiskAcceptanceDoesNotAuthorizeOtherWorkloads(t *testing.T) {
	for _, scene := range []string{"s1", "s2", "r2", "r3", "s3", ""} {
		t.Run(scene, func(t *testing.T) {
			p := recorder.LaunchProfile{Scene: scene, Model: "gpt-6.1-sol", Effort: "high", CostPolicy: recorder.AcceptUnmeteredCost, CostReview: "synthetic review"}
			err := recorder.Preflight(p)
			require.ErrorIs(t, err, recorder.ErrFinancialGate)
			require.NotErrorIs(t, err, recorder.ErrIsolationGate)
		})
	}
	for _, name := range []string{"missing review", "unknown policy", "other model", "other effort"} {
		t.Run(name, func(t *testing.T) {
			p := recorder.LaunchProfile{Scene: "r1", Model: "gpt-6.1-sol", Effort: "high", CostPolicy: recorder.AcceptUnmeteredCost, CostReview: "synthetic review"}
			switch name {
			case "missing review":
				p.CostReview = ""
			case "unknown policy":
				p.CostPolicy = "hard-dollar-cap"
			case "other model":
				p.Model = "other-model"
			case "other effort":
				p.Effort = "low"
			}
			require.ErrorIs(t, recorder.Preflight(p), recorder.ErrFinancialGate)
		})
	}
}
