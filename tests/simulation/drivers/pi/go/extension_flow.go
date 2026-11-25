package pisim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

// ExtensionFlow runs the same unified SDK operations against the recording
// launcher and the strict replay peer. Command must be an explicit executable.
type ExtensionFlow struct {
	Case, Workspace, Command string
	Args, Env                []string
}

func RunExtensionFlow(ctx context.Context, options ExtensionFlow, output io.Writer, beforeClose func(context.Context, <-chan struct{}) error) (err error) {
	if !filepath.IsAbs(options.Workspace) || !filepath.IsAbs(options.Command) {
		return errors.New("pi extension flow requires absolute workspace and command paths")
	}
	interaction, ok := extensionInteractions[options.Case]
	injecting := options.Case == "037" || options.Case == "038"
	if !ok && !injecting {
		return fmt.Errorf("unsupported Pi extension case %q", options.Case)
	}
	piCore := agentpi.New(agentpi.Config{
		CLIPath: options.Command, Args: options.Args, Env: options.Env, Provider: "controlled",
		EnableInject: injecting,
	})
	var core agent.Core = piCore
	if _, err := core.Start(ctx); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, core.Stop(cleanup))
	}()
	open := agent.OpenOptions{Cwd: options.Workspace, Config: agent.SessionConfig{Model: "fixture"}}
	if interaction.question != "" {
		open.Features.AgentQuestions = new(true)
	}
	session, err := piCore.OpenWithOptions(ctx, open, agentpi.SessionOptions{
		ToolApproval: ok && interaction.question == "", InteractionTimeout: 15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("open unified Pi session: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, session.Close(cleanup), session.Err())
	}()
	stream, err := session.Events()
	if err != nil {
		return err
	}
	flow := extensionFlow{ctx: ctx, session: session, stream: stream, output: json.NewEncoder(output)}
	if injecting {
		err = flow.runInjection(options.Case)
	} else {
		err = flow.runInteraction(options.Case, interaction)
	}
	if err != nil {
		return err
	}
	if beforeClose != nil {
		if err := beforeClose(ctx, session.Done()); err != nil {
			return err
		}
	}
	if err := session.Close(ctx); err != nil {
		return err
	}
	select {
	case <-session.Done():
	default:
		return errors.New("unified Pi session did not terminate on close")
	}
	for {
		event, err := flow.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if injecting {
			return fmt.Errorf("pi: unexpected event %q after the final Inject query settled", nativeEventType(event))
		}
	}
	return flow.output.Encode(map[string]any{"type": "session.closed", "sessionId": session.ID()})
}

type extensionFlow struct {
	ctx     context.Context
	session agent.Session
	stream  agent.EventStream
	output  *json.Encoder
}

func (f extensionFlow) next() (agent.Event, error) {
	event, err := f.stream.Next(f.ctx)
	if err != nil {
		return event, err
	}
	if event.SessionID != f.session.ID() {
		return event, errors.New("pi: event changed session identity")
	}
	if err := f.output.Encode(event); err != nil {
		return event, err
	}
	if failure, ok := event.Payload.(agent.SessionErrorPayload); ok {
		return event, fmt.Errorf("pi: reported session error %s", failure.Reason)
	}
	return event, nil
}

func (f extensionFlow) prompt(prompt string) error {
	receipt, err := f.session.StartOrSteer(f.ctx, extensionTextInput(prompt))
	if err != nil {
		return fmt.Errorf("submit Pi prompt: %w", err)
	}
	if receipt.Submission == "" || receipt.Result != agent.InputAppliedNewWork || !nativeAccepted(receipt.Acceptance) {
		return fmt.Errorf("pi: prompt did not start work: %s", receipt.Result)
	}
	return f.output.Encode(map[string]any{"type": "input.receipt", "receipt": receipt})
}

func extensionTextInput(text string) agent.Input {
	return agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: text}}}}
}

func nativeAccepted(acceptance agent.Acceptance) bool {
	return acceptance.Sent != nil && *acceptance.Sent && acceptance.NativeAccepted != nil && *acceptance.NativeAccepted
}

func extensionText(content []agent.ContentBlock) string {
	var text strings.Builder
	for _, block := range content {
		if block.Text != nil {
			text.WriteString(block.Text.Text)
		}
	}
	return text.String()
}

func nativeEventType(event agent.Event) string {
	metadata, _ := event.Meta["pi"].(map[string]any)
	kind, _ := metadata["nativeType"].(string)
	return kind
}
