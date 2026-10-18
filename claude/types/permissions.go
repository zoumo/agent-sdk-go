// SPDX-License-Identifier: Apache-2.0

package types

import "encoding/json"

// PermissionBehavior selects the only two supported permission decisions.
type PermissionBehavior string

const (
	PermissionAllow PermissionBehavior = "allow"
	PermissionDeny  PermissionBehavior = "deny"
)

// PermissionAnswer is a single permission decision. Allow uses the original
// input when UpdatedInput is nil; a non-nil value must be a JSON object ({} is
// an explicit replacement). Deny sends Message verbatim and rejects UpdatedInput.
// Message is only valid for deny. Permission persistence is not supported.
type PermissionAnswer struct {
	Behavior     PermissionBehavior
	UpdatedInput json.RawMessage
	Message      string
}
