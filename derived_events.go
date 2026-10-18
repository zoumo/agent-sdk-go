package codex

import (
	"encoding/json"

	"github.com/zoumo/codex-sdk-go/types"
)

// cloneSubagentEvidence isolates delivered evidence from retained relation state.
func cloneSubagentEvidence(e *types.SubagentEvidence) *types.SubagentEvidence {
	if e == nil {
		return nil
	}
	copy := *e
	copy.ReceiverThreadIDs = append([]string(nil), e.ReceiverThreadIDs...)
	if e.Activity != nil {
		activity := *e.Activity
		if e.Activity.UnknownFields != nil {
			activity.UnknownFields = make(map[string]json.RawMessage, len(e.Activity.UnknownFields))
			for key, raw := range e.Activity.UnknownFields {
				activity.UnknownFields[key] = append(json.RawMessage(nil), raw...)
			}
		}
		copy.Activity = &activity
	}
	return &copy
}
