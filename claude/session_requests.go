// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

// IncomingRequest retains the native envelope and original permission handle.
// Parsing native data cannot construct this authority. Copies share one attempt.
type IncomingRequest struct {
	permission *PermissionRequest
	method     string
	raw        json.RawMessage
}

func (r IncomingRequest) ID() string                 { return r.permission.RequestID() }
func (r IncomingRequest) Method() string             { return r.method }
func (r IncomingRequest) Raw() json.RawMessage       { return bytes.Clone(r.raw) }
func (r IncomingRequest) Params() json.RawMessage    { return r.permission.Raw() }
func (r IncomingRequest) Handle() *PermissionRequest { return r.permission }
func (r IncomingRequest) Context() context.Context   { return r.permission.Context() }

type ApprovalOptions struct {
	UpdatedInput           json.RawMessage
	UpdatedPermissions     []types.PermissionUpdate
	ToolUseID              string
	DecisionClassification string
}
type DenialOptions struct {
	Message                string
	Interrupt              *bool
	ToolUseID              string
	DecisionClassification string
}

// QuestionAnswers uses the fixed native string-valued full-question-text keys.
// The original input (questions, options and unknown fields) is preserved.
type QuestionAnswers struct{ Answers map[string]string }

func (r IncomingRequest) Answer(ctx context.Context, answer QuestionAnswers) error {
	if r.method != "can_use_tool" || r.permission.ToolName() != "AskUserQuestion" {
		return &DispatchError{Op: "answer question", State: NotSent, Err: ErrUnsupported}
	}
	var input types.AskUserQuestionInput
	if err := json.Unmarshal(r.permission.Input(), &input); err != nil {
		return &DispatchError{Op: "answer question", State: NotSent, Err: err}
	}
	if answer.Answers == nil {
		return &DispatchError{Op: "answer question", State: NotSent, Err: ErrInvalidPermissionAnswer}
	}
	answers := make(map[string]types.QuestionAnswer, len(answer.Answers))
	for k, v := range answer.Answers {
		answers[k] = types.QuestionAnswer{Text: &v}
	}
	updated, err := input.WithAnswers(answers)
	if err != nil {
		return &DispatchError{Op: "answer question", State: NotSent, Err: err}
	}
	return r.permission.Answer(ctx, PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: updated})
}

func (r IncomingRequest) Approve(ctx context.Context, options ApprovalOptions) error {
	if r.method != "can_use_tool" {
		return &DispatchError{Op: "approve", State: NotSent, Err: ErrUnsupported}
	}
	return r.permission.Answer(ctx, PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: options.UpdatedInput, UpdatedPermissions: options.UpdatedPermissions, ToolUseID: options.ToolUseID, DecisionClassification: options.DecisionClassification})
}
func (r IncomingRequest) Deny(ctx context.Context, options DenialOptions) error {
	if r.method != "can_use_tool" {
		return &DispatchError{Op: "deny", State: NotSent, Err: ErrUnsupported}
	}
	return r.permission.Answer(ctx, PermissionAnswer{Behavior: PermissionDeny, Message: options.Message, Interrupt: options.Interrupt, ToolUseID: options.ToolUseID, DecisionClassification: options.DecisionClassification})
}

func (s *Session) acceptRequest(e Event, p *PermissionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	r := &IncomingRequest{permission: p, method: e.Control.Subtype, raw: bytes.Clone(e.Raw)}
	// Authority comes from the sole owned interaction, never relationship aliases
	// or unknown agent/session extensions in the request.
	return s.fifo.Push(Received{Kind: ReceivedRequest, Request: r, Raw: r.Raw(), Params: r.Params(), Source: SubagentIdentity{SessionID: s.root}, Origin: "live"}, int64(len(e.Raw)))
}
