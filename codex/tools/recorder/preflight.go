package recorder

import "errors"

// AcceptUnmeteredCost identifies explicit operator acceptance of unknown cost,
// token, provider-request and internal-retry exposure. It promises no hard cap.
// A profile is an audit record, never an authorization source.
const AcceptUnmeteredCost = "accept-unknown-provider-cost-v1"

var ErrIsolationGate = errors.New("Codex recording blocked: approved existing opaque authentication transport and independently established policy/config/hook isolation are unavailable")

// Preflight distinguishes financial-risk acceptance from technical readiness.
// The accepted workload is only R1 (one root, two inputs, 120 seconds) followed
// by R4 (one independent reader, 30 seconds). Children and scene retries remain
// forbidden. Before enabling an actual transport, its durable ledger must bind
// exactly this workload and preserve all historical attempts.
//
// The current implementation intentionally always returns an error: no string,
// review reference or cost acceptance can prove authentication/policy isolation
// or enable a proxy/model spawn. It performs no credential or history discovery.
func Preflight(p LaunchProfile) error {
	if p.CostPolicy != AcceptUnmeteredCost || p.CostReview == "" {
		return ErrFinancialGate
	}
	if p.Scene != "r1" && p.Scene != "r4" {
		return ErrFinancialGate
	}
	if p.Model != "gpt-6.1-sol" || (p.Scene == "r1" && p.Effort != "high") {
		return ErrFinancialGate
	}
	return ErrIsolationGate
}
