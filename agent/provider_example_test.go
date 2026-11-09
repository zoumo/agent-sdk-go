package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
)

// ExampleProvider shows one application flow over two synthetic providers.
// The production adapter constructors are agentcodex.New and agentclaude.New.
// This example keeps both providers offline so it never launches a native CLI.
func ExampleProvider() {
	ctx := context.Background()
	for _, providerName := range []string{"codex", "claude"} {
		provider := newExampleProvider(providerName)
		capabilities, err := provider.Start(ctx)
		mustExample(err)
		fmt.Printf("%s text_input=%t\n", providerName, capabilities.Supports(agent.CapabilityTextInput))

		options := agent.OpenOptions{Cwd: "/synthetic"}
		session, err := provider.Open(ctx, options)
		mustExample(err)
		fmt.Printf("%s open=%s\n", providerName, session.ID())

		stream, err := session.Events()
		mustExample(err)
		receipt, err := session.StartOrSteer(ctx, agent.Input{
			Content: []agent.ContentBlock{agent.TextBlock{Text: "Explain the change"}},
		})
		mustExample(err)
		fmt.Printf("%s start_or_steer=%s\n", providerName, receipt.Submission)

		idle := false
		for !idle {
			event, nextErr := stream.Next(ctx)
			if errors.Is(nextErr, io.EOF) {
				panic("event stream ended before idle")
			}
			mustExample(nextErr)
			switch payload := event.Payload.(type) {
			case agent.UserMessage:
				fmt.Printf("%s event=%s text=%q\n",
					providerName, event.Type(), exampleMessageText(payload.Content))
			case agent.InteractionRequestedPayload:
				fmt.Printf("%s interaction=%s tool=%s\n",
					providerName, payload.Interaction, payload.Request.ToolCallID)
				answerReceipt, answerErr := session.Answer(
					ctx,
					payload.Interaction,
					agent.PermissionAnswer{
						Outcome:  agent.PermissionSelected,
						OptionID: "allow-once",
					},
				)
				mustExample(answerErr)
				fmt.Printf("%s answer=%s\n", providerName, answerReceipt.Interaction)
			case agent.InteractionClosedPayload:
				fmt.Printf("%s event=%s outcome=%s\n",
					providerName, event.Type(), payload.Outcome)
			case agent.AgentMessage:
				fmt.Printf("%s event=%s text=%q\n",
					providerName, event.Type(), exampleMessageText(payload.Content))
			case agent.StateUpdatePayload:
				idle = payload.State != nil && *payload.State == agent.StateIdle
			}
		}
		mustExample(session.Close(ctx))
		fmt.Printf("%s close=ok\n", providerName)

		resumed, err := provider.Resume(ctx, agent.ResumeOptions{
			SessionID:   session.ID(),
			OpenOptions: options,
		})
		mustExample(err)
		fmt.Printf("%s resume=%s\n", providerName, resumed.ID())
		mustExample(resumed.Close(ctx))
		mustExample(provider.Stop(ctx))
		fmt.Printf("%s stop=ok\n", providerName)
	}

	var _ agent.Provider = agentcodex.New(agentcodex.Config{})
	var _ agent.Provider = agentclaude.New(agentclaude.Config{})
	fmt.Println("codex and claude adapters implement agent.Provider")

	// Output:
	// codex text_input=true
	// codex open=codex-session
	// codex start_or_steer=codex-submission
	// codex event=user.message text="Explain the change"
	// codex interaction=codex/interaction-1 tool=codex-tool
	// codex answer=codex/interaction-1
	// codex event=session.interaction_closed outcome=answered
	// codex event=agent.message text="Offline reply for codex"
	// codex close=ok
	// codex resume=codex-session
	// codex stop=ok
	// claude text_input=true
	// claude open=claude-session
	// claude start_or_steer=claude-submission
	// claude event=user.message text="Explain the change"
	// claude interaction=claude/interaction-1 tool=claude-tool
	// claude answer=claude/interaction-1
	// claude event=session.interaction_closed outcome=answered
	// claude event=agent.message text="Offline reply for claude"
	// claude close=ok
	// claude resume=claude-session
	// claude stop=ok
	// codex and claude adapters implement agent.Provider
}

type exampleProvider struct {
	name     string
	mu       sync.Mutex
	started  bool
	stopped  bool
	sessions map[string]*exampleSession
}

func newExampleProvider(name string) *exampleProvider {
	return &exampleProvider{name: name, sessions: make(map[string]*exampleSession)}
}

func (p *exampleProvider) Start(context.Context) (agent.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return nil, errors.New("provider stopped")
	}
	p.started = true
	return agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}, nil
}

func (p *exampleProvider) Open(
	_ context.Context,
	options agent.OpenOptions,
) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return p.newSession(p.name + "-session")
}

func (p *exampleProvider) Resume(
	_ context.Context,
	options agent.ResumeOptions,
) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return p.newSession(options.SessionID)
}

func (p *exampleProvider) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	return nil
}

func (p *exampleProvider) newSession(id string) (agent.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.stopped {
		return nil, errors.New("provider is not active")
	}
	created := newExampleSession(p.name, id)
	p.sessions[id] = created
	return created, nil
}

type exampleSession struct {
	provider string
	id       string
	events   []agent.Event
	next     int
	claimed  bool
	answered bool
	mu       sync.Mutex
	closed   bool
	done     chan struct{}
	close    sync.Once
}

func newExampleSession(provider, id string) *exampleSession {
	interaction := agent.InteractionRef(provider + "/interaction-1")
	idle := agent.StateIdle
	request := agent.InteractionRequest{
		Kind:       agent.InteractionPermission,
		ToolCallID: provider + "-tool",
		Title:      "Approve synthetic tool",
		Options: []agent.PermissionOption{{
			OptionID: "allow-once",
			Name:     "Allow once",
			Kind:     agent.PermissionAllowOnce,
		}},
	}
	return &exampleSession{
		provider: provider,
		id:       id,
		done:     make(chan struct{}),
		events: []agent.Event{
			exampleEvent(id, 1, "user-1", agent.UserMessage{
				MessageID: "user-1",
				Content:   []agent.ContentBlock{agent.TextBlock{Text: "Explain the change"}},
			}),
			exampleEvent(id, 2, "", agent.InteractionRequestedPayload{
				Interaction: interaction,
				Request:     request,
				Blocking:    true,
			}),
			exampleEvent(id, 3, "", agent.InteractionClosedPayload{
				Interaction: interaction,
				Outcome:     agent.InteractionAnswered,
				Reason:      "synthetic_answer",
			}),
			exampleEvent(id, 4, "agent-1", agent.AgentMessage{
				MessageID: "agent-1",
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: "Offline reply for " + provider},
				},
			}),
			exampleEvent(id, 5, "", agent.StateUpdatePayload{State: &idle}),
		},
	}
}

func (s *exampleSession) ID() string { return s.id }

func (s *exampleSession) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}
}

func (s *exampleSession) EffectiveFeatures() agent.EffectiveFeatures {
	return agent.EffectiveFeatures{AgentQuestions: false}
}

func (s *exampleSession) Events() (agent.EventStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("session closed")
	}
	if s.claimed {
		return nil, errors.New("event stream already claimed")
	}
	s.claimed = true
	return exampleStream{session: s}, nil
}

func (s *exampleSession) Submit(
	ctx context.Context,
	input agent.Input,
) (agent.InputReceipt, error) {
	if err := input.Validate(); err != nil {
		return agent.InputReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return agent.InputReceipt{}, err
	}
	sent := true
	return agent.InputReceipt{
		Submission: s.provider + "-submission",
		Acceptance: agent.Acceptance{Sent: &sent},
	}, nil
}

func (s *exampleSession) StartOrSteer(
	ctx context.Context,
	input agent.Input,
) (agent.InputReceipt, error) {
	receipt, err := s.Submit(ctx, input)
	if err != nil {
		return agent.InputReceipt{}, err
	}
	receipt.Result = agent.InputAppliedBestEffort
	return receipt, nil
}

func (s *exampleSession) Answer(
	ctx context.Context,
	interaction agent.InteractionRef,
	answer agent.Answer,
) (agent.InteractionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return agent.InteractionReceipt{}, err
	}
	if err := agent.ValidateAnswer(agent.InteractionPermission, answer); err != nil {
		return agent.InteractionReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return agent.InteractionReceipt{}, errors.New("session closed")
	}
	if s.answered {
		return agent.InteractionReceipt{}, errors.New("interaction already answered")
	}
	if interaction != agent.InteractionRef(s.provider+"/interaction-1") {
		return agent.InteractionReceipt{}, errors.New("interaction is not owned by session")
	}
	s.answered = true
	sent := true
	return agent.InteractionReceipt{
		Interaction: interaction,
		Acceptance:  agent.Acceptance{Sent: &sent},
	}, nil
}

func (s *exampleSession) Interrupt(
	context.Context,
	agent.InterruptOptions,
) (agent.InterruptReceipt, error) {
	return agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, nil
}

func (s *exampleSession) Close(context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.close.Do(func() { close(s.done) })
	return nil
}

func (s *exampleSession) Done() <-chan struct{} { return s.done }

func (s *exampleSession) Err() error { return nil }

type exampleStream struct {
	session *exampleSession
}

func (s exampleStream) Next(context.Context) (agent.Event, error) {
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.session.next == len(s.session.events) {
		return agent.Event{}, io.EOF
	}
	event := s.session.events[s.session.next]
	s.session.next++
	return event, nil
}

func exampleEvent(
	sessionID string,
	sequence int,
	itemID string,
	payload agent.Payload,
) agent.Event {
	return agent.Event{
		EventID:   fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence),
		SessionID: sessionID,
		Time:      time.Date(2026, 11, 8, 0, 0, sequence, 0, time.UTC),
		ItemID:    itemID,
		Payload:   payload,
	}
}

func exampleMessageText(content []agent.ContentBlock) string {
	if len(content) != 1 {
		return ""
	}
	text, _ := content[0].(agent.TextBlock)
	return text.Text
}

func mustExample(err error) {
	if err != nil {
		panic(err)
	}
}
