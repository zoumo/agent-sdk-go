package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Reserve the explicitly selected identity before dispatch. Notifications may
// precede the resume response; no Session or answer handle escapes until that
// response confirms the identity. Saved turns are never projected into Receive.
func (s *Session) resume(ctx context.Context, params types.ThreadResumeParams) error {
	c := s.client
	c.mu.Lock()
	err := s.bindLocked(params.ThreadID)
	c.mu.Unlock()
	if err != nil {
		return &CallError{Method: "thread/resume", Dispatch: NotSent, Cause: err}
	}
	err = c.callOwned(ctx, "thread/resume", params, nil, false, func(raw json.RawMessage) error {
		result := resumeThreadResult{expectedID: params.ThreadID}
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("%w: decode thread/resume result: %w", ErrProtocol, err)
		}
		c.attachments[s.root].managed = true
		s.resumeRaw = append(json.RawMessage(nil), raw...)
		return nil
	}, s)
	if err != nil {
		// Even an unknown dispatch outcome may have attached the native thread.
		// Detach once; a failed detach retains the existing uncertainty fence.
		var call *CallError
		c.mu.Lock()
		if errors.As(err, &call) && call.Dispatch != NotSent {
			c.attachments[s.root].managed = true
		}
		c.mu.Unlock()
		return errors.Join(err, s.Close())
	}
	return nil
}

// ResumeResponse returns an independent copy of the native thread/resume
// response, including saved turns and unknown fields. It is absent for starts.
func (s *Session) ResumeResponse() (types.ThreadResumeResponse, bool) {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	if len(s.resumeRaw) == 0 {
		return types.ThreadResumeResponse{}, false
	}
	var response types.ThreadResumeResponse
	_ = json.Unmarshal(s.resumeRaw, &response)
	return response, true
}
