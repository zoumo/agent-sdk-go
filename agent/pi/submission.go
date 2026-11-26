package agentpi

import (
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/client/pi"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

// submissionState is guarded by session.mu, which also protects session closure
// and interaction registration against changes to the active work.
type submissionState struct {
	prompt        pendingPrompt
	active        string
	promptUnknown bool
	injecting     bool
	interrupting  bool
}

type pendingPrompt struct {
	id string
	// A settlement observed during this prompt takes precedence over its reply.
	settled bool
}

func (state *submissionState) inputBusy() bool {
	return state.prompt.id != "" || state.injecting || state.promptUnknown
}

func (s *session) admitPrompt(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, closedError()
	}
	if s.submission.inputBusy() || s.submission.interrupting {
		return false, nil
	}
	s.submission.prompt = pendingPrompt{id: id}
	return true, nil
}

func (s *session) finishPrompt(disposition string, sendErr error) (agent.InputReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := &s.submission
	prompt := state.prompt
	state.prompt = pendingPrompt{}
	if sendErr != nil {
		receipt := agent.InputReceipt{Acceptance: commandAcceptance(sendErr)}
		var rejected *pi.CommandError
		if errors.As(sendErr, &rejected) {
			receipt.Result = agent.InputNotApplied
		}
		err := operationError("start_or_steer", sendErr)
		if agent.CodeOf(err) == agent.ErrorOutcomeUnknown && !prompt.settled {
			state.promptUnknown = true
		}
		return receipt, err
	}
	receipt := agent.InputReceipt{Submission: prompt.id, Acceptance: agent.Acceptance{Sent: new(true), NativeAccepted: new(true)}}
	switch disposition {
	case "started":
		receipt.Result = agent.InputAppliedNewWork
		if !prompt.settled && state.active == "" {
			state.active = prompt.id
		}
	case "queued", "handled":
		// Pi does not identify which run consumes the input in these replies.
		receipt.Result = agent.InputAppliedBestEffort
	default:
		if !prompt.settled {
			state.promptUnknown = true
		}
		return receipt, &agent.Error{Code: agent.ErrorOutcomeUnknown, Reason: "prompt_disposition_unknown", Message: "Pi returned an unknown prompt disposition"}
	}
	return receipt, nil
}

func (s *session) admitInjection() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return closedError()
	}
	if s.submission.inputBusy() {
		return &agent.Error{
			Code: agent.ErrorFailedPrecondition, Reason: agent.ErrorReasonForCode("inject", agent.ErrorFailedPrecondition),
			Message: "another input is in flight or its outcome is unknown",
		}
	}
	s.submission.injecting = true
	return nil
}

func (s *session) finishInjection() {
	s.mu.Lock()
	s.submission.injecting = false
	s.mu.Unlock()
}

func (s *session) admitInterrupt() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.submission.active
	switch {
	case s.closed:
		return "", closedError()
	case target == "":
		return "", nil
	case s.submission.interrupting:
		return "", &agent.Error{Code: agent.ErrorFailedPrecondition, Reason: "interrupt_in_progress", Message: "Pi interrupt is already in progress"}
	}
	s.submission.interrupting = true
	return target, nil
}

func (s *session) finishInterrupt() {
	s.mu.Lock()
	s.submission.interrupting = false
	s.mu.Unlock()
}

func (state *submissionState) agentStarted() error {
	if state.active == "" {
		id := state.prompt.id
		if id == "" {
			var err error
			id, err = uuidv4.New()
			if err != nil {
				return err
			}
		}
		state.active = id
	}
	state.promptUnknown = false
	return nil
}

func (state *submissionState) agentSettled() {
	if state.prompt.id != "" {
		state.prompt.settled = true
	}
	state.active = ""
	state.promptUnknown = false
}
