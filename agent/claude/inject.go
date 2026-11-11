package agentclaude

import (
	"context"
	"strconv"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

func (s *session) Inject(ctx context.Context, input agent.Input) (agent.InjectReceipt, error) {
	receipt := agent.InjectReceipt{Acceptance: configNotSent()}
	text, err := textInput(input)
	if err != nil {
		return receipt, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return receipt, adapterError(
			agent.ErrorClosed, agent.ErrorReasonSessionClosed, "session is closed", nil,
		)
	}
	if !s.injectSupported {
		s.mu.Unlock()
		return receipt, adapterError(
			agent.ErrorUnsupported, agent.ErrorReasonForCode("inject", agent.ErrorUnsupported),
			"Claude inject requires a confirmed stable CLI version 2.1.258 or later in the 2.x series", nil,
		)
	}
	if s.submitting || s.injecting || s.submitUnknown {
		s.mu.Unlock()
		return receipt, adapterError(
			agent.ErrorFailedPrecondition, agent.ErrorReasonForCode("inject", agent.ErrorFailedPrecondition),
			"another input is in flight or its outcome is unknown", nil,
		)
	}
	s.injecting = true
	s.mu.Unlock()

	shouldQuery := false
	submission, submitErr := s.native.Submit(ctx, claude.Input{User: &claudetypes.UserInput{
		Message: claudetypes.InputMessage{
			Role: "user", Content: claudetypes.Content{Text: &text},
		},
		ShouldQuery: &shouldQuery,
	}})
	s.mu.Lock()
	s.injecting = false
	s.mu.Unlock()
	if submitErr != nil {
		receipt.Acceptance = configAcceptance(submitErr)
		return receipt, nativeOperationError("inject", submitErr)
	}
	sent := true
	receipt.Acceptance = agent.Acceptance{Sent: &sent}
	if !submission.Written {
		return receipt, adapterError(
			agent.ErrorOutcomeUnknown, agent.ErrorReasonForCode("inject", agent.ErrorOutcomeUnknown),
			"Claude inject returned without write evidence", nil,
		)
	}
	return receipt, nil
}

func supportsInject(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 || parts[0] != "2" {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	return minorErr == nil && patchErr == nil && (minor > 1 || minor == 1 && patch >= 258)
}
