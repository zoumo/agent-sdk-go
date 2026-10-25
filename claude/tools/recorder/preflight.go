// SPDX-License-Identifier: Apache-2.0
package recorder

import "errors"

var (
	ErrPolicyIsolation = errors.New("recorder: independent managed-policy isolation is unproven; no CLI launch permitted")
	ErrFinancialCap    = errors.New("recorder: independent monetary/token/generation/retry enforcement is unproven")
)

// Preflight is deliberately closed until independently reviewed enforcement is
// implemented. Environment assertions, safe-mode, and observing no hook event
// cannot prove that mandatory managed startup hooks have not executed. This
// function does not inspect authentication or run even --version/--help.
func Preflight() error { return errors.Join(ErrPolicyIsolation, ErrFinancialCap) }
