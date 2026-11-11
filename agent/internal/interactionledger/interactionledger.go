// Package interactionledger owns adapter-neutral interaction authority state.
// It allocates opaque references and stores no native request, handle, reply
// callback, or reference-to-adapter lookup.
package interactionledger

import (
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

// Ledger allocates an interaction reference and registration sequence for one
// Session. Its reference prefix is only presentation; the ledger never resolves
// a reference back to adapter state.
type Ledger struct {
	mu       sync.Mutex
	prefix   string
	sequence uint64
}

// New creates a Session-scoped interaction ledger.
func New(prefix string) *Ledger {
	return &Ledger{prefix: prefix}
}

// Allocate returns an opaque reference and the next registration sequence.
func (l *Ledger) Allocate() (agent.InteractionRef, uint64, error) {
	value, err := uuidv4.New()
	if err != nil {
		return "", 0, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sequence++
	return agent.InteractionRef(l.prefix + value), l.sequence, nil
}

// State is the adapter-neutral authority phase of one interaction.
type State uint8

const (
	Pending State = iota
	Answering
	Sent
	Settled
)

// Result is settlement data that remains independent of a native request.
type Result struct {
	Outcome agent.InteractionOutcome
	Reason  string
}

// Slot serializes answer authority for one interaction. The zero value is
// pending and ready to use.
type Slot struct {
	mu          sync.Mutex
	state       State
	completion  chan struct{}
	sentOutcome agent.InteractionOutcome
	terminal    Result
	hasTerminal bool
	result      Result
	resolved    bool
}

// State returns the current authority phase.
func (s *Slot) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Claim admits one answer. It succeeds only while the interaction is pending.
func (s *Slot) Claim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != Pending {
		return false
	}
	s.state = Answering
	s.completion = make(chan struct{})
	return true
}

// CompleteSent records a successfully submitted answer and its adapter-neutral
// outcome.
func (s *Slot) CompleteSent(outcome agent.InteractionOutcome) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != Answering {
		return false
	}
	s.state = Sent
	s.sentOutcome = outcome
	s.finishAnswerLocked()
	return true
}

// ReleaseNotSent reopens authority after a definite local non-delivery.
func (s *Slot) ReleaseNotSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != Answering {
		return false
	}
	s.state = Pending
	s.finishAnswerLocked()
	return true
}

// CompleteTerminal consumes authority after an unknown or terminal native
// outcome. A terminal result takes precedence over a recorded sent result.
func (s *Slot) CompleteTerminal(
	outcome agent.InteractionOutcome,
	reason string,
) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolved || s.hasTerminal {
		return false
	}
	s.terminal = Result{Outcome: outcome, Reason: reason}
	s.hasTerminal = true
	s.state = Settled
	s.finishAnswerLocked()
	return true
}

// Resolve waits for an admitted answer without requiring or retaining an
// adapter lock. It applies terminal, sent, then fallback precedence and caches
// the stable closure result.
func (s *Slot) Resolve(fallback Result, answeredReason string) Result {
	for {
		s.mu.Lock()
		if s.resolved {
			result := s.result
			s.mu.Unlock()
			return result
		}
		if s.state == Answering {
			completion := s.completion
			s.mu.Unlock()
			<-completion
			continue
		}

		result := fallback
		switch {
		case s.hasTerminal:
			result = s.terminal
		case s.state == Sent:
			result = Result{
				Outcome: s.sentOutcome,
				Reason:  answeredReason,
			}
		}
		s.state = Settled
		s.result = result
		s.resolved = true
		s.mu.Unlock()
		return result
	}
}

func (s *Slot) finishAnswerLocked() {
	if s.completion == nil {
		return
	}
	close(s.completion)
	s.completion = nil
}
