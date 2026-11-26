package agentcodex

import "github.com/zoumo/agent-sdk-go/agent"

// mainTurnOwnership is protected by session.mu so a turn transition and the
// corresponding interaction registration or settlement can happen together.
type mainTurnOwnership struct {
	submitting    bool
	injecting     bool
	submitUnknown bool
	workEpoch     uint64

	activeTurn   string
	startedTurn  string
	idleTurn     string
	lastFinished string
}

type submitSnapshot struct {
	activeTurn string
	workEpoch  uint64
}

func (m *mainTurnOwnership) inputBlocked() bool {
	return m.submitting || m.injecting || m.submitUnknown
}

func (m *mainTurnOwnership) beginSubmit() (submitSnapshot, bool) {
	if m.inputBlocked() {
		return submitSnapshot{}, false
	}
	m.submitting = true
	return submitSnapshot{activeTurn: m.activeTurn, workEpoch: m.workEpoch}, true
}

func (m *mainTurnOwnership) failSubmit(before submitSnapshot, unknown bool) {
	m.submitting = false
	// A late write failure cannot invalidate newer native work evidence.
	if unknown && m.workEpoch == before.workEpoch {
		m.submitUnknown = true
	}
}

func (m *mainTurnOwnership) acceptSubmit(before submitSnapshot, turnID string) agent.InputResult {
	m.submitting = false
	// Idle or completion can arrive before the response to this submission.
	if m.lastFinished != turnID && m.idleTurn != turnID {
		m.activeTurn = turnID
	}
	if before.activeTurn == turnID {
		return agent.InputAppliedCurrentWork
	}
	return agent.InputAppliedNewWork
}

func (m *mainTurnOwnership) beginInject() bool {
	if m.inputBlocked() {
		return false
	}
	m.injecting = true
	return true
}

func (m *mainTurnOwnership) endInject() {
	m.injecting = false
}

func (m *mainTurnOwnership) activeTurnID() string {
	return m.activeTurn
}

func (m *mainTurnOwnership) noteWorkEvidence() {
	m.workEpoch++
	m.submitUnknown = false
}

// An outstanding turn/start can identify new work through a notification or request.
func (m *mainTurnOwnership) canClaimPendingTurn(turnID string) bool {
	return m.activeTurn == "" && (m.submitting || m.submitUnknown) && m.lastFinished != turnID
}

func (m *mainTurnOwnership) matchesWork(turnID string) bool {
	return m.activeTurn == turnID || m.idleTurn == turnID || m.canClaimPendingTurn(turnID)
}

func (m *mainTurnOwnership) acceptsContent(turnID string) bool {
	// A newly attached session can receive a live delta before its first
	// lifecycle notification. Delivering that content does not claim the turn.
	return turnID != "" && (m.matchesWork(turnID) ||
		(m.activeTurn == "" && m.idleTurn == "" && m.lastFinished == ""))
}

func (m *mainTurnOwnership) observeStarted(turnID string) bool {
	if !m.acceptsContent(turnID) || m.startedTurn == turnID ||
		m.idleTurn == turnID || m.lastFinished == turnID {
		return false
	}
	m.activeTurn = turnID
	m.startedTurn = turnID
	m.idleTurn = ""
	m.noteWorkEvidence()
	return true
}

func (m *mainTurnOwnership) observeCompleted(turnID string) bool {
	if turnID == "" || !m.matchesWork(turnID) {
		return false
	}
	if m.activeTurn == turnID {
		m.activeTurn = ""
	}
	if m.startedTurn == turnID {
		m.startedTurn = ""
	}
	if m.idleTurn == turnID {
		m.idleTurn = ""
	}
	m.lastFinished = turnID
	m.noteWorkEvidence()
	return true
}

func (m *mainTurnOwnership) observeIdle() {
	if m.activeTurn != "" {
		m.idleTurn = m.activeTurn
	}
	m.activeTurn = ""
	m.startedTurn = ""
	if !m.submitting {
		m.noteWorkEvidence()
	}
}

func (m *mainTurnOwnership) acceptRequest(turnID string) bool {
	if turnID == "" {
		return false
	}
	if m.canClaimPendingTurn(turnID) {
		m.activeTurn = turnID
		m.noteWorkEvidence()
		return true
	}
	return m.activeTurn == turnID && m.lastFinished != turnID
}
