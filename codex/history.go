package codex

import (
	"context"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// ListThreadTurns returns one native page without resuming or projecting events.
// Direction, item detail, limits and opaque cursors are interpreted by the server.
func (c *Client) ListThreadTurns(ctx context.Context, p types.ThreadTurnsListParams) (types.ThreadTurnsListResponse, error) {
	var r types.ThreadTurnsListResponse
	err := c.Call(ctx, "thread/turns/list", p, &r)
	return r, err
}

// ListThreadItems returns one native saved-item page, not an event log.
// An item anchor requires a non-empty TurnID under the native server contract.
func (c *Client) ListThreadItems(ctx context.Context, p types.ThreadItemsListParams) (types.ThreadItemsListResponse, error) {
	var r types.ThreadItemsListResponse
	err := c.Call(ctx, "thread/items/list", p, &r)
	return r, err
}
