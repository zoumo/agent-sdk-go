package pisim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestPiExtensionAnswerRequiresOriginalReference(t *testing.T) {
	const ref = agent.InteractionRef("pi-original")
	for _, scenario := range []struct {
		name            string
		allowWrongRef   bool
		wrongReceiptRef bool
		wantError       string
	}{
		{name: "original"},
		{name: "unrelated accepted", allowWrongRef: true, wantError: "unrelated interaction"},
		{name: "receipt mismatched", wrongReceiptRef: true, wantError: "original interaction write"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session := &extensionAnswerSession{
				original: ref, allowWrongRef: scenario.allowWrongRef, wrongReceiptRef: scenario.wrongReceiptRef,
			}
			flow := extensionFlow{ctx: t.Context(), session: session, output: json.NewEncoder(io.Discard)}
			answer := agent.FormAnswer{Outcome: agent.FormAccept, Content: map[string]any{"value": "Green"}}
			err := flow.answer(ref, answer)
			if scenario.wantError == "" {
				require.NoError(t, err)
				require.Equal(t, []agent.InteractionRef{ref}, session.sent)
				return
			}
			require.ErrorContains(t, err, scenario.wantError)
		})
	}
}

type extensionAnswerSession struct {
	agent.Session
	original                       agent.InteractionRef
	sent                           []agent.InteractionRef
	allowWrongRef, wrongReceiptRef bool
}

func (s *extensionAnswerSession) Answer(_ context.Context, ref agent.InteractionRef, answer agent.Answer) (agent.InteractionReceipt, error) {
	if ref != s.original && !s.allowWrongRef {
		return agent.InteractionReceipt{}, &agent.Error{Code: agent.ErrorInvalidArgument, Reason: agent.ErrorReasonInvalidInteractionRef}
	}
	form, ok := answer.(agent.FormAnswer)
	if !ok || form.Outcome != agent.FormAccept || form.Content["value"] != "Green" {
		return agent.InteractionReceipt{}, errors.New("wrong form output")
	}
	s.sent = append(s.sent, ref)
	if s.wrongReceiptRef {
		ref = "pi-unrelated"
	}
	return agent.InteractionReceipt{Interaction: ref, Acceptance: agent.Acceptance{Sent: new(true)}}, nil
}
