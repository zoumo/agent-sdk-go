// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

// PermissionBehavior is native decision data in types.
type PermissionBehavior = types.PermissionBehavior

// PermissionAnswer is a runtime command. A nil UpdatedInput uses the original
// input; it is distinct from the detached native PermissionResult value.
type PermissionAnswer struct {
	Behavior           PermissionBehavior
	UpdatedInput       json.RawMessage
	UpdatedPermissions []types.PermissionUpdate
	Message            string
	Interrupt          *bool
	// These native extensions are not connected to the runtime answer contract.
	ToolUseID              string
	DecisionClassification string
}

const (
	PermissionAllow = types.PermissionAllow
	PermissionDeny  = types.PermissionDeny
)

// PermissionRequest is a connection-local, single-use answer authority. Its
// metadata is immutable; byte accessors return copies. Copying the handle does
// not create another response authority. The zero value cannot be answered.
type PermissionRequest struct{ state *permissionState }

// RequestID is the exact incoming control request identifier.
func (p *PermissionRequest) RequestID() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.id
}

// ToolUseID is the supplied tool-use identifier, or empty if absent.
func (p *PermissionRequest) ToolUseID() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.toolUseID
}

func (p *PermissionRequest) ToolName() string {
	if p == nil || p.state == nil {
		return ""
	}
	return p.state.toolName
}

// Input returns the original input JSON, including unknown fields.
func (p *PermissionRequest) Input() json.RawMessage {
	if p == nil || p.state == nil {
		return nil
	}
	return bytes.Clone(p.state.input)
}

// Raw returns the entire nested request object. It preserves context such as
// agent_id, permission_suggestions, blocked_path, decision_reason, title,
// display_name, description, and unknown fields without interpreting policy.
func (p *PermissionRequest) Raw() json.RawMessage {
	if p == nil || p.state == nil {
		return nil
	}
	return bytes.Clone(p.state.raw)
}

// Metadata returns an owned, detached data snapshot. It carries no answer
// authority; edits cannot change this handle's IDs, original input or state.
// A nil/zero handle returns ErrInvalidPermissionRequest.
func (p *PermissionRequest) Metadata() (types.PermissionMetadata, error) {
	if p == nil || p.state == nil {
		return types.PermissionMetadata{}, ErrInvalidPermissionRequest
	}
	var metadata types.PermissionMetadata
	err := json.Unmarshal(p.state.raw, &metadata)
	return metadata, err
}

// Epoch identifies this client instance within this Go process. It is not a
// native session ID and is never sent to the CLI or reused for resume.
func (p *PermissionRequest) Epoch() uint64 {
	if p == nil || p.state == nil {
		return 0
	}
	return p.state.epoch
}

// Context is canceled when the request is answered, canceled by the CLI, or
// invalidated by shutdown/failure. context.Cause identifies the reason. A nil
// or zero handle returns an already canceled context.
func (p *PermissionRequest) Context() context.Context {
	if p != nil && p.state != nil {
		return p.state.ctx
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrInvalidPermissionRequest)
	return ctx
}

// Answer reserves the sole response attempt. Invalid/unsupported input and
// cancellation before write authorization return NotSent and retain live native
// authority. An authorized write with an unknown outcome is never retried.
// Success establishes only a complete local write, not CLI acceptance.
func (p *PermissionRequest) Answer(ctx context.Context, answer PermissionAnswer) (result error) {
	defer func() {
		if result != nil {
			state := NotSent
			if errors.Is(result, ErrUnknownOutcome) {
				state = OutcomeUnknown
			}
			result = &DispatchError{Op: "permission answer", State: state, Err: result}
		}
	}()
	if err := p.reserveAnswer(ctx); err != nil {
		return err
	}
	s := p.state
	data, err := p.answerData(answer)
	if err != nil {
		c := s.owner
		c.mu.Lock()
		defer c.mu.Unlock()
		if stateErr := c.permissionErrorLocked(s, permissionAnswering); stateErr != nil {
			return stateErr
		}
		s.status = permissionPending
		return err
	}
	return p.writeResponse(ctx, map[string]any{"subtype": "success", "request_id": s.id, "response": data})
}

func (p *PermissionRequest) reserveAnswer(ctx context.Context) error {
	if p == nil || p.state == nil {
		return ErrInvalidPermissionRequest
	}
	if ctx == nil {
		return ErrInvalidPermissionAnswer
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := p.state
	c := s.owner
	c.mu.Lock()
	if err := c.permissionErrorLocked(s, permissionPending); err != nil {
		c.mu.Unlock()
		return err
	}
	s.status = permissionAnswering
	c.mu.Unlock()

	return nil
}

func (p *PermissionRequest) answerUnsupported(ctx context.Context, subtype string) error {
	if err := p.reserveAnswer(ctx); err != nil {
		return err
	}
	return p.writeResponse(ctx, map[string]any{"subtype": "error", "request_id": p.RequestID(), "error": "Unsupported control request subtype: " + subtype})
}

func (p *PermissionRequest) writeResponse(ctx context.Context, response map[string]any) error {
	s := p.state
	c := s.owner
	writeCtx, cancelWrite := context.WithCancel(ctx)
	stopRequest := context.AfterFunc(s.ctx, cancelWrite)
	defer cancelWrite()
	authorized := false
	err := c.writeJSONChecked(writeCtx, map[string]any{
		"type": "control_response", "response": response,
	}, func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.permissionErrorLocked(s, permissionAnswering); err != nil {
			return err
		}
		// This is write authorization, serialized with cancellation and Close.
		// The mutex is never held across the potentially blocking OS write.
		s.status = permissionWriting
		authorized = true
		return nil
	})
	stopRequest()

	c.mu.Lock()
	if err == nil && s.status != permissionWriting {
		err = &Error{Op: "permission answer", Err: c.permissionErrorLocked(s, permissionWriting), OutcomeUnknown: true}
	}
	if err != nil {
		switch {
		case s.status == permissionCancelled:
			if authorized {
				err = errors.Join(ErrPermissionCancelled, err)
			} else {
				// Another operation's terminal error may have an unknown outcome;
				// this answer is known not to have reached write authorization.
				err = ErrPermissionCancelled
			}
			err = &PermissionError{RequestID: s.id, Err: err}
		case !authorized && (s.status == permissionClosed || c.closing || c.stopped):
			err = &PermissionError{RequestID: s.id, Err: ErrClosed}
		case !authorized && s.status == permissionAnswering:
			// Local cancellation/encoding/size rejection before OS authorization
			// does not consume the original native answer authority.
			s.status = permissionPending
		case authorized || s.status == permissionAnswering || s.status == permissionWriting:
			// writeJSONChecked can already have closed the connection on error.
			// Preserve the consumed attempt's failure instead of hiding it behind
			// that global closed state. Nothing can reserve this handle again.
			err = p.failAnswerLocked(err)
		default:
			err = &PermissionError{RequestID: s.id, Err: errors.Join(ErrClosed, err)}
		}
	} else {
		s.status = permissionAnswered
		delete(c.permissions, s.id)
		s.cancel(ErrPermissionUsed)
	}
	c.mu.Unlock()
	return err
}

// Called under owner.mu after the sole answer attempt has been consumed.
func (p *PermissionRequest) failAnswerLocked(err error) error {
	s := p.state
	s.status = permissionWriteFailed
	s.failure = err
	delete(s.owner.permissions, s.id)
	failure := &PermissionError{RequestID: s.id, Err: errors.Join(ErrPermissionWriteFailed, err)}
	s.cancel(failure)
	// A failed local attempt must not strand the CLI waiting for a response
	// that cannot be retried. Terminal-ID history remains reserved.
	s.owner.failLocked(failure)
	return failure
}

func (p *PermissionRequest) answerData(answer PermissionAnswer) (map[string]any, error) {
	if answer.ToolUseID != "" || answer.DecisionClassification != "" {
		return nil, ErrUnsupported
	}
	switch answer.Behavior {
	case PermissionAllow:
		if answer.Message != "" || answer.Interrupt != nil {
			return nil, ErrInvalidPermissionAnswer
		}
		input := answer.UpdatedInput
		if input == nil {
			input = p.state.input
		}
		if len(input) > p.state.owner.opts.Limits.FrameBytes {
			return nil, &ResourceLimitError{Resource: "outbound frame bytes", Limit: p.state.owner.opts.Limits.FrameBytes}
		}
		input = bytes.TrimSpace(input)
		if !utf8.Valid(input) || !json.Valid(input) || len(input) == 0 || input[0] != '{' {
			return nil, ErrInvalidPermissionAnswer
		}
		if p.ToolName() == "AskUserQuestion" {
			var question types.AskUserQuestionInput
			if err := json.Unmarshal(input, &question); err != nil {
				return nil, errors.Join(ErrInvalidPermissionAnswer, err)
			}
			if question.Fields["response"] != types.Default || question.Response != nil {
				return nil, ErrUnsupported
			}
			for _, a := range question.Answers {
				if a.Labels != nil {
					return nil, ErrUnsupported
				}
			}
		}
		data := map[string]any{"behavior": "allow", "updatedInput": json.RawMessage(bytes.Clone(input))}
		if answer.UpdatedPermissions != nil {
			if err := validatePermissionUpdates(answer.UpdatedPermissions); err != nil {
				return nil, err
			}
			data["updatedPermissions"] = answer.UpdatedPermissions
		}
		return data, nil
	case PermissionDeny:
		if answer.UpdatedInput != nil || answer.UpdatedPermissions != nil || !utf8.ValidString(answer.Message) {
			return nil, ErrInvalidPermissionAnswer
		}
		if len(answer.Message) > p.state.owner.opts.Limits.FrameBytes {
			return nil, &ResourceLimitError{Resource: "outbound frame bytes", Limit: p.state.owner.opts.Limits.FrameBytes}
		}
		data := map[string]any{"behavior": "deny", "message": answer.Message}
		if answer.Interrupt != nil {
			data["interrupt"] = *answer.Interrupt
		}
		return data, nil
	default:
		return nil, ErrInvalidPermissionAnswer
	}
}

func validatePermissionUpdates(updates []types.PermissionUpdate) error {
	for _, u := range updates {
		switch u.Destination {
		case "userSettings", "projectSettings", "localSettings", "session", "cliArg":
		default:
			return ErrInvalidPermissionAnswer
		}
		switch u.Type {
		case "addRules", "replaceRules", "removeRules":
			if u.Rules == nil || u.Mode != "" || u.Directories != nil {
				return ErrInvalidPermissionAnswer
			}
			if u.Behavior != "allow" && u.Behavior != "deny" && u.Behavior != "ask" {
				return ErrInvalidPermissionAnswer
			}
			for _, r := range u.Rules {
				if r.ToolName == "" || !utf8.ValidString(r.ToolName) || (r.RuleContent != nil && !utf8.ValidString(*r.RuleContent)) {
					return ErrInvalidPermissionAnswer
				}
			}
		case "setMode":
			if u.Rules != nil || u.Directories != nil || u.Behavior != "" {
				return ErrInvalidPermissionAnswer
			}
			switch u.Mode {
			case "default", "acceptEdits", "bypassPermissions", "plan", "dontAsk", "auto":
			default:
				return ErrUnsupported
			}
		case "addDirectories", "removeDirectories":
			if u.Directories == nil || u.Rules != nil || u.Mode != "" || u.Behavior != "" {
				return ErrInvalidPermissionAnswer
			}
			for _, d := range u.Directories {
				if d == "" || !utf8.ValidString(d) {
					return ErrInvalidPermissionAnswer
				}
			}
		default:
			return ErrUnsupported
		}
	}
	return nil
}

type permissionStatus uint8

const (
	permissionPending permissionStatus = iota
	permissionAnswering
	permissionWriting
	permissionAnswered
	permissionCancelled
	permissionClosed
	permissionWriteFailed
)

type permissionState struct {
	owner                   *interaction
	id, toolUseID, toolName string
	input, raw              json.RawMessage
	epoch                   uint64
	ctx                     context.Context
	cancel                  context.CancelCauseFunc
	status                  permissionStatus // guarded by owner.mu
	failure                 error            // guarded by owner.mu; retained after shutdown
}

type incomingControl struct {
	subtype    string
	permission *PermissionRequest
}

var connectionEpoch atomic.Uint64

// Called only under c.mu, including the final writer authorization check.
func (c *interaction) permissionErrorLocked(s *permissionState, expected permissionStatus) error {
	var err error
	switch {
	case s.owner != c || s.epoch != c.epoch:
		err = ErrPermissionStale
	case s.status == permissionCancelled:
		err = ErrPermissionCancelled
	case s.status == permissionWriteFailed:
		err = errors.Join(ErrPermissionWriteFailed, s.failure)
	case s.status == permissionAnswered:
		err = ErrPermissionUsed
	case s.status == permissionClosed || c.closing || c.stopped:
		err = ErrClosed
	case s.status != expected:
		err = ErrPermissionUsed
	}
	if err != nil {
		return &PermissionError{RequestID: s.id, Err: err}
	}
	return nil
}

func (c *interaction) registerIncoming(request *ControlRequest) (*PermissionRequest, error) {
	var wire types.PermissionMetadata
	if request.Subtype == "can_use_tool" {
		if err := json.Unmarshal(request.Request, &wire); err != nil {
			return nil, err
		}
		if wire.ToolName == "" || len(wire.Input) == 0 || wire.Input[0] != '{' {
			return nil, fmt.Errorf("permission request requires tool_name and object input")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.stopped {
		return nil, ErrClosed
	}
	if _, exists := c.permissionIDs[request.RequestID]; exists {
		return nil, fmt.Errorf("duplicate incoming control request ID %q", request.RequestID)
	}
	if len(c.permissions) >= c.opts.Limits.PendingPermissions {
		return nil, &ResourceLimitError{Resource: "pending permissions", Limit: c.opts.Limits.PendingPermissions}
	}
	if len(c.permissionIDs) >= c.opts.Limits.PermissionHistory {
		return nil, &ResourceLimitError{Resource: "permission request history", Limit: c.opts.Limits.PermissionHistory}
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	s := &permissionState{owner: c, id: request.RequestID, toolUseID: wire.ToolUseID, toolName: wire.ToolName,
		input: bytes.Clone(wire.Input), raw: bytes.Clone(request.Request), epoch: c.epoch, ctx: ctx, cancel: cancel}
	c.permissions[s.id], c.permissionIDs[s.id] = s, struct{}{}
	return &PermissionRequest{state: s}, nil
}

func (c *interaction) cancelPermission(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.permissions[id]; s != nil {
		s.status = permissionCancelled
		delete(c.permissions, id)
		s.cancel(ErrPermissionCancelled)
	}
	if _, seen := c.permissionIDs[id]; !seen {
		if len(c.permissionIDs) >= c.opts.Limits.PermissionHistory {
			return &ResourceLimitError{Resource: "permission request history", Limit: c.opts.Limits.PermissionHistory}
		}
		c.permissionIDs[id] = struct{}{}
	}
	return nil
}
