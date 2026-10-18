package types

// These SDK convenience data types are not app-server notifications and never
// alter native protocol structs or JSON. They appear only in Event.Data.

// EventIdentity contains only native thread, turn, and item identities.
type EventIdentity struct{ ThreadID, TurnID, ItemID string }

// SubagentEvidence preserves the native proof of a relationship. History seeds
// establish association only; Method is empty for a snapshot. Activity is the
// actual V2 creation item, not a synthesized collab item.
type SubagentEvidence struct {
	Method            string
	Provenance        string
	ParentThreadID    string
	Source            EventIdentity
	Tool              string
	ReceiverThreadIDs []string
	Activity          *SubAgentActivityThreadItem
}

// SubagentCreated confirms creation from a live successful completed spawn item
// or a live completed V2 activity of kind started. It grants no answer authority.
type SubagentCreated struct {
	ThreadID       string
	ParentThreadID string
	Evidence       *SubagentEvidence
}

// SubagentAssociated establishes a relationship without asserting creation.
type SubagentAssociated struct {
	ThreadID       string
	ParentThreadID string
	Evidence       *SubagentEvidence
}

// ObservationState describes local observation gaps and bounds, never the
// success or completion of provider work. UnresolvedThreadID is not membership.
type ObservationState struct {
	State              string
	Reason             string
	UnresolvedThreadID string
	ParentThreadID     string
	Evidence           *SubagentEvidence
	PreviousEvidence   *SubagentEvidence
}
