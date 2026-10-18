package codex_test

import (
	"encoding/json"

	"github.com/zoumo/codex-sdk-go/types"
)

// This test-only view lets the existing transport fixtures share assertions.
// Native identity is read from the actual Params; no routing metadata is supplied
// by the SDK. Convenience fields come exclusively from their public Data types.
const (
	nativeRecord     = "native"
	createdRecord    = "created"
	associatedRecord = "associated"
	stateRecord      = "state"
)

type subscriptionRecord struct {
	Kind                                      string
	Native                                    *types.Event
	Raw                                       json.RawMessage
	Source                                    types.EventIdentity
	ParentThreadID, Provenance, Reason, State string
	CreationConfirmed                         bool
	Evidence, PreviousEvidence                *types.SubagentEvidence
}

func recordView(event types.Event) subscriptionRecord {
	e := subscriptionRecord{Raw: event.Raw}
	switch d := event.Data.(type) {
	case *types.SubagentCreated:
		e.Kind = createdRecord
		e.Source.ThreadID = d.ThreadID
		e.ParentThreadID = d.ParentThreadID
		e.Evidence = d.Evidence
		e.CreationConfirmed = true
	case *types.SubagentAssociated:
		e.Kind = associatedRecord
		e.Source.ThreadID = d.ThreadID
		e.ParentThreadID = d.ParentThreadID
		e.Evidence = d.Evidence
	case *types.ObservationState:
		e.Kind = stateRecord
		e.State = d.State
		e.Reason = d.Reason
		e.Source.ThreadID = d.UnresolvedThreadID
		e.ParentThreadID = d.ParentThreadID
		e.Evidence = d.Evidence
		e.PreviousEvidence = d.PreviousEvidence
	default:
		e.Kind = nativeRecord
		e.Native = &event
		var f map[string]json.RawMessage
		_ = json.Unmarshal(event.Params, &f)
		for key, target := range map[string]*string{"threadId": &e.Source.ThreadID, "turnId": &e.Source.TurnID, "itemId": &e.Source.ItemID} {
			_ = json.Unmarshal(f[key], target)
		}
		for key, target := range map[string]*string{"thread": &e.Source.ThreadID, "turn": &e.Source.TurnID, "item": &e.Source.ItemID} {
			var nested struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(f[key], &nested)
			if *target == "" {
				*target = nested.ID
			}
		}
	}
	if e.Evidence != nil {
		e.Provenance = e.Evidence.Provenance
	}
	return e
}
