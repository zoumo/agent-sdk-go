package claude

import (
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// GetSettings returns the native get_settings response body without interpreting
// or caching it. It is a read-only control query, not an execution-idle barrier.
func (s *Session) GetSettings(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, dispatchFailure("get_settings", queue.ErrNilContext)
	}
	response, err := s.client.controlRequest(
		ctx, "get_settings", map[string]any{"subtype": "get_settings"}, false,
	)
	return response, dispatchFailure("get_settings", err)
}
