// Package pisim exercises the public Pi SDK against native recordings.
package pisim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	"github.com/zoumo/agent-sdk-go/pi"
)

// AcceptContinuation verifies that two inputs complete in the same native session.
func AcceptContinuation(ctx context.Context, mock *mocktest.Session, root string, observations io.Writer) (err error) {
	var plan struct {
		CaseID        string `json:"caseId"`
		DriverProfile string
		Prompts       []string
	}
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	if plan.CaseID != "SDK-CASE-001" || plan.DriverProfile != "continuation" || len(plan.Prompts) != 2 {
		return errors.New("pi: continuation requires SDK-CASE-001 with two prompts")
	}

	run, err := mock.NewRun(ctx, mocktest.RunOptions{NativeArgs: []string{"--mode", "rpc"}})
	if err != nil {
		if run != nil {
			err = errors.Join(err, run.Abort())
		}
		return err
	}
	defer func() {
		err = errors.Join(err, run.CloseSDK(), run.WaitError(ctx))
	}()
	session, err := pi.Attach(ctx, run.Writer, run.Reader, "")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.CloseAndDiscard()) }()

	encoder := json.NewEncoder(observations)
	for index, prompt := range plan.Prompts {
		if err := acceptTurn(ctx, session, prompt, encoder); err != nil {
			return fmt.Errorf("pi: turn %d: %w", index+1, err)
		}
	}
	state, err := session.State(ctx)
	if err != nil {
		return err
	}
	if state.SessionID != session.ID() || state.IsStreaming {
		return errors.New("pi: session did not stay the same and return to idle after both turns")
	}
	return encoder.Encode(map[string]string{"sessionId": state.SessionID, "type": "get_state"})
}

func acceptTurn(ctx context.Context, session *pi.Session, prompt string, observations *json.Encoder) error {
	result, err := session.Prompt(ctx, prompt, nil, "")
	if err != nil {
		return err
	}
	if result.Disposition != "started" {
		return fmt.Errorf("expected Pi to start the prompt, got %q", result.Disposition)
	}

	var reply, stopReason string
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		switch event.Type {
		case "message_end":
			var ended struct {
				Message struct {
					Role, StopReason string
					Content          json.RawMessage
				}
			}
			if err := json.Unmarshal(event.Raw, &ended); err != nil {
				return err
			}
			if ended.Message.Role == "assistant" {
				var content []struct{ Type, Text string }
				if err := json.Unmarshal(ended.Message.Content, &content); err != nil {
					return err
				}
				var text strings.Builder
				for _, block := range content {
					if block.Type == "text" {
						text.WriteString(block.Text)
					}
				}
				reply, stopReason = text.String(), ended.Message.StopReason
			}
		case "agent_settled":
			var terminal struct{ Aborted bool }
			if err := json.Unmarshal(event.Raw, &terminal); err != nil {
				return err
			}
			if terminal.Aborted || stopReason != "stop" || strings.TrimSpace(reply) == "" {
				return fmt.Errorf("pi: settled without a successful assistant reply (stop reason %q, aborted %t)", stopReason, terminal.Aborted)
			}
			return observations.Encode(map[string]string{
				"sessionId": session.ID(), "reply": reply, "stopReason": stopReason, "terminal": event.Type,
			})
		}
	}
}
