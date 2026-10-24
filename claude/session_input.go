// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// Input sends native user data to the main session. User and nonempty Text are
// mutually exclusive. Text (including empty text) uses the same writer/path.
// Supported blocks: text, image, document, tool_result; other variants are
// explicitly unsupported even if an output decoder can retain them.
type Input struct {
	Text string
	User *types.UserInput
}

func dispatchFailure(op string, err error) error {
	if err == nil {
		return nil
	}
	var native *ControlError
	if errors.As(err, &native) {
		return err
	}
	state := NotSent
	if errors.Is(err, ErrUnknownOutcome) {
		state = OutcomeUnknown
	}
	return &DispatchError{Op: op, State: state, Err: err}
}

func (c *Client) sendInput(ctx context.Context, input Input) error {
	fail := func(err error) error { return dispatchFailure("submit", err) }
	if ctx == nil {
		return fail(queue.ErrNilContext)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(input.Text) > c.opts.Limits.FrameBytes {
		return fail(&ResourceLimitError{Resource: "outbound frame bytes", Limit: c.opts.Limits.FrameBytes})
	}
	user := types.UserInput{Message: types.InputMessage{Role: "user", Content: types.Content{Text: &input.Text}}}
	if input.User != nil {
		if input.Text != "" {
			return fail(fmt.Errorf("%w: both Text and User", ErrInvalidInput))
		}
		user = *input.User
	}
	if err := validateInputEncoding(user); err != nil {
		return fail(err)
	}
	data, err := json.Marshal(user)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrInvalidInput, err))
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fail(err)
	}
	// These envelope values are stream ownership markers, not target selectors.
	for k, want := range map[string]string{"type": `"user"`, "session_id": `""`, "parent_tool_use_id": `null`} {
		if got, ok := envelope[k]; ok && !bytes.Equal(bytes.TrimSpace(got), []byte(want)) {
			return fail(fmt.Errorf("%w: fixed %s", ErrInvalidInput, k))
		}
		envelope[k] = json.RawMessage(want)
	}
	var message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err = json.Unmarshal(envelope["message"], &message); err != nil || message.Role != "user" {
		return fail(fmt.Errorf("%w: message role must be user", ErrInvalidInput))
	}
	if err = validateInputContent(message.Content, false); err != nil {
		return fail(err)
	}
	return fail(c.writeJSON(ctx, envelope))
}

func validateInputContent(data json.RawMessage, nested bool) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &blocks) != nil {
		return fmt.Errorf("%w: content must be string or blocks", ErrInvalidInput)
	}
	for _, b := range blocks {
		var kind string
		_ = json.Unmarshal(b["type"], &kind)
		switch kind {
		case "text":
			var text string
			if len(b["text"]) == 0 || bytes.Equal(b["text"], []byte("null")) || json.Unmarshal(b["text"], &text) != nil {
				return fmt.Errorf("%w: text block requires text", ErrInvalidInput)
			}
		case "image", "document":
			var source map[string]json.RawMessage
			if json.Unmarshal(b["source"], &source) != nil || source == nil {
				return fmt.Errorf("%w: %s source required", ErrInvalidInput, kind)
			}
			var sourceType string
			_ = json.Unmarshal(source["type"], &sourceType)
			switch sourceType {
			case "base64", "url": // Native source fields and extensions pass through unchanged.
			case "text":
				if kind != "document" {
					return fmt.Errorf("%w: image text source", ErrUnsupported)
				}
			case "content":
				if kind != "document" {
					return fmt.Errorf("%w: image content source", ErrUnsupported)
				}
				if err := validateInputContent(source["content"], true); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: %s source %q", ErrUnsupported, kind, sourceType)
			}

		case "tool_result":
			if nested {
				return fmt.Errorf("%w: nested tool_result", ErrUnsupported)
			}
			var id string
			if json.Unmarshal(b["tool_use_id"], &id) != nil || id == "" {
				return fmt.Errorf("%w: tool_use_id required", ErrInvalidInput)
			}
			if content, ok := b["content"]; ok {
				if err := validateInputContent(content, true); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%w: input block %q", ErrUnsupported, kind)
		}
	}
	return nil
}
