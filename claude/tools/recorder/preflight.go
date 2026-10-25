// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"errors"
	"fmt"
)

var (
	ErrPolicyIsolation = errors.New("recorder: independent managed-policy isolation is unproven; no CLI launch permitted")
	ErrFinancialCap    = errors.New("recorder: independent monetary/token/generation/retry enforcement is unproven")
)

// FinancialPolicy selects the approved accounting requirement, not launch authority.
// A root/input/time ceiling cannot establish a monetary or token hard stop.
type FinancialPolicy string

const (
	RequireFinancialCap        FinancialPolicy = "require-hard-cap"
	AcceptFinancialUncertainty FinancialPolicy = "accept-uncertainty"
)

// Preflight retains the independent policy-isolation gate. Explicit acceptance
// of uncertain provider accounting removes only the financial hard-cap condition;
// it never certifies credentials, versions, hooks, tool restrictions or permission.
// Omission keeps the strict historical policy. No executable or auth is inspected.
func Preflight(policy ...FinancialPolicy) error {
	selected := RequireFinancialCap
	if len(policy) > 1 {
		return errors.Join(ErrPolicyIsolation, fmt.Errorf("recorder: exactly one financial policy permitted"))
	}
	if len(policy) == 1 {
		selected = policy[0]
	}
	switch selected {
	case RequireFinancialCap:
		return errors.Join(ErrPolicyIsolation, ErrFinancialCap)
	case AcceptFinancialUncertainty:
		return ErrPolicyIsolation
	default:
		return errors.Join(ErrPolicyIsolation, fmt.Errorf("recorder: unknown financial policy"))
	}
}
