// SPDX-License-Identifier: Apache-2.0

package types

import "encoding/json"

// PermissionBehavior selects the only two supported permission decisions.
type PermissionBehavior string

const (
	PermissionAllow PermissionBehavior = "allow"
	PermissionDeny  PermissionBehavior = "deny"
)

// PermissionUpdate is native rule/mode/directory update data from SDK 0.3.258.
// Type selects the applicable fields; the runtime validates before sending.
type PermissionUpdate struct {
	Type        string                `json:"type"`
	Rules       []PermissionRuleValue `json:"rules,omitempty"`
	Behavior    string                `json:"behavior,omitempty"`
	Mode        string                `json:"mode,omitempty"`
	Directories []string              `json:"directories,omitempty"`
	Destination string                `json:"destination"`
}
type PermissionRuleValue struct {
	ToolName    string  `json:"toolName"`
	RuleContent *string `json:"ruleContent,omitempty"`
}

// MarshalJSON preserves required empty arrays for the selected native variant.
func (u PermissionUpdate) MarshalJSON() ([]byte, error) {
	data := map[string]any{"type": u.Type, "destination": u.Destination}
	switch u.Type {
	case "addRules", "replaceRules", "removeRules":
		data["rules"] = u.Rules
		data["behavior"] = u.Behavior
	case "setMode":
		data["mode"] = u.Mode
	case "addDirectories", "removeDirectories":
		data["directories"] = u.Directories
	default:
		type wire PermissionUpdate
		return json.Marshal(wire(u))
	}
	return json.Marshal(data)
}
