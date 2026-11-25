package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Inject appends raw Responses API items to the owned main thread without
// starting a user turn. The server validates native item semantics. A successful
// response proves RPC acceptance, not model consumption or durable recording.
// Servers without thread/inject_items return a native method-not-found error.
func (s *Session) Inject(ctx context.Context, items []json.RawMessage) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: inject requires at least one item", ErrInvalidInput)
	}
	for _, item := range items {
		if !json.Valid(item) {
			return fmt.Errorf("%w: invalid inject item JSON", ErrInvalidInput)
		}
	}
	var response types.ThreadInjectItemsResponse
	return s.client.callOwned(ctx, "thread/inject_items", types.ThreadInjectItemsParams{
		ThreadID: s.root,
		Items:    items,
	}, &response, false, nil, s)
}
