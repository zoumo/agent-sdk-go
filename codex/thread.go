package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// ResumeThread sends the caller's native thread ID unchanged and accepts only a
// response with that same ID. It never creates a replacement thread, reconnects,
// or replays a request. Optional parameters preserve omitted/null/value states.
// Native history/path selectors can select another identity; such a response is
// an ErrProtocol failure under this method's strict identity contract.
func (c *Client) ResumeThread(ctx context.Context, p types.ThreadResumeParams) (types.ThreadResumeResponse, error) {
	finish, err := c.beginThreadOperation(p.ThreadID, true)
	if err != nil {
		return types.ThreadResumeResponse{}, err
	}
	defer finish()
	return c.resumeThread(ctx, p)
}

func (c *Client) resumeThread(ctx context.Context, p types.ThreadResumeParams) (types.ThreadResumeResponse, error) {
	r := resumeThreadResult{expectedID: p.ThreadID}
	err := c.Call(ctx, "thread/resume", p, &r)
	return r.response, err
}

// Validate during result decoding so identity failures retain the matching RPC
// ID and Responded dispatch evidence, just like other malformed native results.
type resumeThreadResult struct {
	expectedID string
	response   types.ThreadResumeResponse
}

func (r *resumeThreadResult) UnmarshalJSON(data []byte) error {
	var response types.ThreadResumeResponse
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
func (c *Client) UnsubscribeThread(ctx context.Context, p types.ThreadUnsubscribeParams) (types.ThreadUnsubscribeResponse, error) {
	c.mu.Lock()
	if c.sessions[p.ThreadID] != nil || c.attachmentBusyLocked(p.ThreadID) || c.loads[p.ThreadID] != nil {
		c.mu.Unlock()
		return types.ThreadUnsubscribeResponse{}, &CallError{Method: "thread/unsubscribe", Dispatch: NotSent, Cause: ErrOwnershipConflict}
	}
	if c.threadOperations == nil {
		c.threadOperations = make(map[string]int)
	}
	c.threadOperations[p.ThreadID]++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.threadOperations[p.ThreadID]--
		if c.threadOperations[p.ThreadID] == 0 {
			delete(c.threadOperations, p.ThreadID)
		}
		c.mu.Unlock()
	}()
	return c.unsubscribeThread(ctx, p)
}

// Only resource owners use this dispatch after reserving the release fence.
func (c *Client) unsubscribeThread(ctx context.Context, p types.ThreadUnsubscribeParams) (types.ThreadUnsubscribeResponse, error) {
	var r types.ThreadUnsubscribeResponse
	err := c.Call(ctx, "thread/unsubscribe", p, &r)
	return r, err
}
