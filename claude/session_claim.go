package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// maxClaimBufferedBytes bounds startup frames that arrive before claim_session
// names the native identity. The CLI may run SessionStart hooks during the
// claim, so this allowance is larger than a single frame.
const maxClaimBufferedBytes = 8 << 20

type claimRequest struct {
	Subtype            string          `json:"subtype"`
	CWD                string          `json:"cwd"`
	AppendSystemPrompt string          `json:"append_system_prompt,omitempty"`
	Agents             json.RawMessage `json:"agents,omitempty"`
	SDKMCPServers      []string        `json:"sdk_mcp_servers,omitempty"`
}

type claimReply struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

// claim binds the parked spare to this Session's working directory and takes
// the native identity the CLI assigns to it. Frames read before the response
// wait in order, then enter admission once the root identity is fixed.
func (c *interaction) claim(ctx context.Context) error {
	claimCtx, cancel := context.WithTimeout(ctx, c.opts.InitializeTimeout)
	defer cancel()
	cwd := c.opts.WorkDir
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	request := claimRequest{
		Subtype:            "claim_session",
		CWD:                cwd,
		AppendSystemPrompt: c.opts.AppendSystemPrompt,
		SDKMCPServers:      c.opts.SDKMCPServers,
	}
	if c.opts.Initialize != nil && len(c.opts.Initialize.Agents) > 0 &&
		string(c.opts.Initialize.Agents) != "null" {
		request.Agents = c.opts.Initialize.Agents
	}
	raw, err := c.control(claimCtx, "claim_session", request)
	if err != nil {
		return err
	}
	var reply claimReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return &Error{Op: "claim_session", Err: fmt.Errorf("%w: %w", ErrProtocol, err), Raw: append(json.RawMessage(nil), raw...)}
	}
	if reply.SessionID == "" {
		return &Error{Op: "claim_session", Err: fmt.Errorf("%w: claim response has no session ID", ErrProtocol), Raw: append(json.RawMessage(nil), raw...)}
	}
	if reply.Status != "ok" {
		return &Error{Op: "claim_session", Err: fmt.Errorf("%w: claim response status is %q", ErrProtocol, reply.Status), Raw: append(json.RawMessage(nil), raw...)}
	}
	if reply.CWD == "" || !sameClaimDirectory(reply.CWD, cwd) {
		return &Error{Op: "claim_session", Err: fmt.Errorf("%w: claimed working directory differs from the requested directory", ErrProtocol), Raw: append(json.RawMessage(nil), raw...)}
	}

	c.claimMu.Lock()
	defer c.claimMu.Unlock()
	if c.session != nil {
		if err := c.session.confirmClaimedRoot(reply.SessionID); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.sessionID = reply.SessionID
	c.mu.Unlock()
	buffered := c.claimBuffered
	c.claimBuffered, c.claimBufferedBytes = nil, 0
	for _, event := range buffered {
		if err := c.routeNative(event); err != nil {
			return err
		}
	}
	c.claiming.Store(false)
	select {
	case <-c.done:
		return c.operationError()
	default:
	}
	return nil
}

func (c *interaction) bufferClaimedFrame(event Event) error {
	size := len(event.Raw)
	if size > maxClaimBufferedBytes-c.claimBufferedBytes {
		return &ResourceLimitError{Resource: "startup frames before session claim", Limit: maxClaimBufferedBytes}
	}
	c.claimBuffered = append(c.claimBuffered, event)
	c.claimBufferedBytes += size
	return nil
}

// sameClaimDirectory compares the canonical directory reported by the CLI with
// the requested one without rejecting a symlinked path to the same directory.
func sameClaimDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil &&
		leftInfo.IsDir() && rightInfo.IsDir() && os.SameFile(leftInfo, rightInfo)
}
