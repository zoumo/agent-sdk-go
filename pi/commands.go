package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// State reads Pi's current state. A changed native identity invalidates this attachment.
func (s *Session) State(ctx context.Context) (State, error) {
	raw, err := s.request(ctx, "get_state", nil)
	if err != nil {
		return State{}, err
	}
	var state State
	if err = json.Unmarshal(raw, &state); err != nil {
		return State{}, &DispatchError{Sent: true, Err: err}
	}
	if err := s.checkIdentity(state.SessionID); err != nil {
		return State{}, err
	}
	state.Raw = raw
	return state, nil
}

func (s *Session) checkIdentity(id string) error {
	if id == "" || (s.id != "" && s.id != id) {
		err := errors.New("pi: missing or changed native session ID")
		s.fail(err)
		return &DispatchError{Sent: true, Err: err}
	}
	return nil
}

// Prompt starts work when idle. While streaming Pi requires "steer" or "followUp" to queue it.
func (s *Session) Prompt(ctx context.Context, message string, images []Image, streamingBehavior string) (PromptResult, error) {
	return s.submit(ctx, "prompt", message, images, streamingBehavior)
}

func (s *Session) Steer(ctx context.Context, message string, images []Image) (PromptResult, error) {
	return s.submit(ctx, "steer", message, images, "")
}

func (s *Session) FollowUp(ctx context.Context, message string, images []Image) (PromptResult, error) {
	return s.submit(ctx, "follow_up", message, images, "")
}

func (s *Session) submit(ctx context.Context, command, message string, images []Image, behavior string) (PromptResult, error) {
	fields := map[string]any{"message": message}
	if len(images) > 0 {
		for _, img := range images {
			if img.Type != "image" || img.MIMEType == "" || img.Data == "" {
				return PromptResult{}, errors.New("pi: invalid image block")
			}
		}
		fields["images"] = images
	}
	if behavior != "" {
		if behavior != "steer" && behavior != "followUp" {
			return PromptResult{}, fmt.Errorf("pi: unknown streaming behavior %q", behavior)
		}
		fields["streamingBehavior"] = behavior
	}
	raw, err := s.request(ctx, command, fields)
	if err != nil {
		return PromptResult{}, err
	}
	var result PromptResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, &DispatchError{Sent: true, Err: err}
	}
	if result.Disposition == "" {
		return result, &DispatchError{Sent: true, Err: errors.New("pi: no input disposition in response")}
	}
	return result, nil
}

// Abort waits for the native abort response. Queue continuation follows Pi's own scheduling policy.
func (s *Session) Abort(ctx context.Context) error {
	_, err := s.request(ctx, "abort", nil)
	return err
}

func (s *Session) SetModel(ctx context.Context, provider, id string) error {
	if provider == "" || id == "" {
		return errors.New("pi: provider and model ID are required")
	}
	_, err := s.request(ctx, "set_model", map[string]any{"provider": provider, "modelId": id})
	return err
}

// SetThinkingLevel reports native acceptance. State reports the value actually selected.
func (s *Session) SetThinkingLevel(ctx context.Context, level string) error {
	if level == "" {
		return errors.New("pi: empty thinking level")
	}
	_, err := s.request(ctx, "set_thinking_level", map[string]any{"level": level})
	return err
}

// SetSteeringMode sets how Pi delivers queued steering input between turns.
// Read State to observe the effective mode.
func (s *Session) SetSteeringMode(ctx context.Context, mode QueueMode) error {
	return s.setQueueMode(ctx, "set_steering_mode", mode)
}

// SetFollowUpMode sets how Pi delivers follow-up input after work completes.
// Read State to observe the effective mode.
func (s *Session) SetFollowUpMode(ctx context.Context, mode QueueMode) error {
	return s.setQueueMode(ctx, "set_follow_up_mode", mode)
}

func (s *Session) setQueueMode(ctx context.Context, command string, mode QueueMode) error {
	switch mode {
	case QueueAll, QueueOneAtATime:
		_, err := s.request(ctx, command, map[string]any{"mode": mode})
		return err
	default:
		return fmt.Errorf("pi %s: invalid queue mode %q", command, mode)
	}
}
