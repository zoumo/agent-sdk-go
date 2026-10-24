package types

// EventIdentity contains only native thread, turn, and item identities.
type EventIdentity struct{ ThreadID, TurnID, ItemID string }
