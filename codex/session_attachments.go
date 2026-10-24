package codex

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Native subscriptions are sets of connection IDs, not one attachment per
// resume. Keep references for all confirmed observers separately from main
// ownership and request authority. All fields are protected by Client.mu.
type sessionAttachment struct {
	observers map[*Session]struct{}
	managed   bool
	releasing bool
	uncertain error
}

func (s *Session) retainAttachmentLocked(id string) bool {
	c := s.client
	if c.threadOperations[id] > 0 {
		return false
	}
	if c.attachments == nil {
		c.attachments = make(map[string]*sessionAttachment)
	}
	a := c.attachments[id]
	if a == nil {
		a = &sessionAttachment{observers: make(map[*Session]struct{})}
		c.attachments[id] = a
	}
	if a.releasing || a.uncertain != nil {
		return false
	}
	a.observers[s] = struct{}{}
	return true
}

// releaseAttachments removes this observer and releases only physical
// attachments no remaining Session needs. An uncertain release keeps a fence;
// neither a new owner nor a new observer can silently reuse that attachment.
func (s *Session) releaseAttachments() error {
	c := s.client
	c.mu.Lock()
	var release []string
	var result error
	for id, a := range c.attachments {
		if _, ok := a.observers[s]; !ok {
			continue
		}
		if c.cause != nil {
			delete(a.observers, s)
			if len(a.observers) == 0 {
				delete(c.attachments, id)
			}
			continue
		}
		if id == s.root && s.resumeUnknown != nil {
			a.uncertain = s.releaseLoadError()
			result = errors.Join(result, a.uncertain)
			continue
		}
		delete(a.observers, s)
		if len(a.observers) != 0 {
			continue
		}
		if a.uncertain != nil {
			result = errors.Join(result, a.uncertain)
			continue
		}
		if !a.managed || c.cause != nil {
			delete(c.attachments, id)
			continue
		}
		a.releasing = true
		release = append(release, id)
	}
	c.mu.Unlock()
	for _, id := range release {
		ctx, cancel := context.WithTimeout(context.Background(), c.limits.ShutdownTimeout)
		r, err := c.unsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: id})
		cancel()
		if err == nil && r.Status != types.ThreadUnsubscribeStatusUnsubscribed && r.Status != types.ThreadUnsubscribeStatusNotLoaded && r.Status != types.ThreadUnsubscribeStatusNotSubscribed {
			err = errors.New("codex: unknown unsubscribe result")
		}
		c.mu.Lock()
		if err == nil || c.cause != nil {
			delete(c.attachments, id)
		} else {
			a := c.attachments[id]
			a.releasing = false
			a.uncertain = err
		}
		c.mu.Unlock()
		result = errors.Join(result, err)
	}
	return result
}

// attachmentBusyLocked covers both active observers and release tombstones.
func (c *Client) attachmentBusyLocked(id string) bool {
	a := c.attachments[id]
	return a != nil && (len(a.observers) != 0 || a.releasing || a.uncertain != nil)
}
