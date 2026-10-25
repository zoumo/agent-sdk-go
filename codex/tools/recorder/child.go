package recorder

import "errors"

const ChildRuntimeSource = "092d3acd6bec3e3a14bdc7e7a2810ab628ab759d"

// ErrChildRuntimeUnsupported is a technical refusal, independent of financial
// risk acceptance. This source has no verified cumulative pre-spawn interceptor
// connected to our ledger, recursive tool policy, or background cleanup proof.
var ErrChildRuntimeUnsupported = errors.New("Codex child recording blocked: cumulative pre-creation interception, recursive tool/workdir/network isolation and bounded background cleanup unproven")

// ChildPolicy records a proposal, not approval. Zero is never an implicit child
// allowance. The limits have no fixed topology or financial defaults.
type ChildPolicy struct {
	Source            string `json:"source"`
	MaxAttempts       int    `json:"maxAttempts"`
	MaxDepth          int    `json:"maxDepth"`
	BackgroundSeconds int    `json:"backgroundSeconds"`
}

func childPreflight(p LaunchProfile) error {
	if p.Child == nil {
		switch p.Scene {
		case "child-direct", "child-nested", "child-parallel", "child-background":
			return ErrChildRuntimeUnsupported
		}
		return nil
	}
	if p.Child.MaxAttempts <= 0 || p.Child.MaxDepth <= 0 || p.Child.BackgroundSeconds <= 0 {
		return errors.New("explicit positive child attempt/depth/background proposal required")
	}
	// Version/source assertions cannot supply missing native enforcement. Neither
	// an operator approval string nor a synthetic peer can unlock this gate.
	if p.CLIVersion != "0.162.1" || p.Child.Source != ChildRuntimeSource {
		return errors.Join(ErrChildRuntimeUnsupported, errors.New("unsupported child runtime version/source"))
	}
	return ErrChildRuntimeUnsupported
}
