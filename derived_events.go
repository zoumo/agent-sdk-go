package codex

import (
	"encoding/json"

	"github.com/zoumo/codex-sdk-go/types"
)

// subscriptionEventKind tags private delivery staging, never the public API,
// app-server methods or protocol union variants.
type subscriptionEventKind string

const (
	subscriptionNative     subscriptionEventKind = "Native"
	subscriptionCreated    subscriptionEventKind = "SubagentCreated"
	subscriptionAssociated subscriptionEventKind = "SubagentAssociated"
	subscriptionState      subscriptionEventKind = "ObservationState"
)

// subscriptionEvent stages bounded native delivery and separate SDK convenience data.
type subscriptionEvent struct {
	Kind           subscriptionEventKind
	Source         types.EventIdentity
	ParentThreadID string
	Evidence       *types.SubagentEvidence
	// PreviousEvidence retains the accepted edge when Evidence conflicts with it.
	PreviousEvidence *types.SubagentEvidence
	Native           *types.Event
	Raw              json.RawMessage
	// State and Reason describe observation limits, never provider work success.
	State  string
	Reason string
}

// Native notifications retain their own fields and JSON. Convenience events
// carry separate SDK data types, with no method or origin flag fabricated.
func (e subscriptionEvent) event() types.Event {
	if e.Native != nil {
		event := *e.Native
		event.Raw = e.Raw
		return event
	}
	var data any
	switch e.Kind {
	case subscriptionCreated:
		data = &types.SubagentCreated{ThreadID: e.Source.ThreadID, ParentThreadID: e.ParentThreadID, Evidence: e.Evidence}
	case subscriptionAssociated:
		data = &types.SubagentAssociated{ThreadID: e.Source.ThreadID, ParentThreadID: e.ParentThreadID, Evidence: e.Evidence}
	case subscriptionState:
		data = &types.ObservationState{State: e.State, Reason: e.Reason, UnresolvedThreadID: e.Source.ThreadID, ParentThreadID: e.ParentThreadID, Evidence: e.Evidence, PreviousEvidence: e.PreviousEvidence}
	}
	return types.Event{Data: data}
}
