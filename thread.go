package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/codex-sdk-go/protocol"
)

// ResumeThread sends the caller's native thread ID unchanged and accepts only a
// response with that same ID. It never creates a replacement thread, reconnects,
// or replays a request. Optional parameters preserve omitted/null/value states.
// Native history/path selectors can select another identity; such a response is
// an ErrProtocol failure under this method's strict identity contract.
func (c *Client) ResumeThread(ctx context.Context, p protocol.ThreadResumeParams) (protocol.ThreadResumeResponse, error) {
	r := resumeThreadResult{expectedID: p.ThreadID}
	err := c.Call(ctx, "thread/resume", p, &r)
	return r.response, err
}

// Validate during result decoding so identity failures retain the matching RPC
// ID and Responded dispatch evidence, just like other malformed native results.
type resumeThreadResult struct {
	expectedID string
	response   protocol.ThreadResumeResponse
}

func (r *resumeThreadResult) UnmarshalJSON(data []byte) error {
	var response protocol.ThreadResumeResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.Thread.ID != r.expectedID {
		return errors.New("resumed thread ID does not match the requested thread ID")
	}
	r.response = response
	return nil
}

// UnsubscribeThread requests server-side detachment for the specified native
// thread once and returns the native status. It does not close local Subscription
// values or imply interruption, deletion, or completion of a thread's work.
func (c *Client) UnsubscribeThread(ctx context.Context, p protocol.ThreadUnsubscribeParams) (protocol.ThreadUnsubscribeResponse, error) {
	var r protocol.ThreadUnsubscribeResponse
	err := c.Call(ctx, "thread/unsubscribe", p, &r)
	return r, err
}
