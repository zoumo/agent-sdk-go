// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// SessionOptions carries native owned-process options and observation bounds.
type SessionOptions Options

type ReceivedKind string

const (
	ReceivedNative      ReceivedKind = "event"
	ReceivedRequest     ReceivedKind = "request"
	ReceivedObservation ReceivedKind = "observation"
)

// Received retains the original native envelope. Parent knowledge is optional;
// it does not gate membership or determine the order of the live FIFO.
type Received struct {
	Kind        ReceivedKind
	Event       *Event
	Request     *IncomingRequest
	Observation *Observation
	Raw         json.RawMessage
	Params      json.RawMessage
	Source      SubagentIdentity
	Parent      *SubagentIdentity
	Origin      string
}
type Observation struct {
	Reason, Evidence string
	Source           SubagentIdentity
	Parent           *SubagentIdentity
}

// Submission proves only a complete local write, never CLI acceptance.
type (
	Submission       struct{ Written bool }
	InterruptOptions struct{}
)

// Session owns lifecycle and the single delivery FIFO. liveAdmission interprets
// observation evidence; interaction owns I/O and authoritative control handles.
type Session struct {
	discovery     discoveryScope
	admission     *liveAdmission
	client        *interaction
	done          chan struct{}
	clientReady   chan struct{}
	requestedID   string
	settingsGate  chan struct{}
	traceBaseline TraceContext
	traceCurrent  TraceContext
	traceKnown    bool
	mu            sync.Mutex
	fifo          queue.Queue[Received]
	closed        bool
	cause         error
}

// Open starts one owned native process and completes startup. SessionID assigns
// the main identity before input; ClaimSession obtains it from claim_session.
// Otherwise received evidence establishes it. The context bounds startup, not
// Session lifetime.
func Open(ctx context.Context, options SessionOptions) (*Session, error) {
	if ctx == nil {
		return nil, queue.ErrNilContext
	}
	o := Options(options)
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if err := o.defaults(); err != nil {
		return nil, err
	}
	s, err := newSession(o)
	if err != nil {
		return nil, err
	}
	_, err = openInteraction(ctx, o, s)
	if err != nil {
		_ = s.fifo.Abort(err)
		return nil, err
	}
	return s, nil
}

func newSession(o Options) (*Session, error) {
	s := &Session{
		done: make(chan struct{}), clientReady: make(chan struct{}),
		requestedID: o.ResumeID, discovery: freezeDiscoveryScope(&o),
		settingsGate: make(chan struct{}, 1),
	}
	s.traceBaseline = startupTrace(o.Env)
	s.traceCurrent, s.traceKnown = s.traceBaseline, true
	admission, err := newLiveAdmission(o, &s.mu)
	if err != nil {
		return nil, err
	}
	s.admission = admission
	if o.SessionID != "" {
		for _, record := range admission.confirmRoot(o.SessionID) {
			_ = s.fifo.Push(record, 0)
		}
	} else if !o.ClaimSession {
		// The assigned-root path already establishes its identity before input.
		// Do not manufacture a relationship diagnostic for that internal setup.
		_ = s.fifo.Push(liveObservation("forwarding-not-confirmed", "initialize-forwardSubagentText-is-a-request", SubagentIdentity{}, nil), 0)
	}
	return s, nil
}

// confirmClaimedRoot fixes the identity reported by claim_session and commits
// any observations the admission layer derives from caller-supplied history.
func (s *Session) confirmClaimedRoot(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	for _, record := range s.admission.claimRoot(sessionID) {
		if err := s.fifo.Push(record, 0); err != nil {
			return err
		}
	}
	return nil
}

// bindClient publishes the process before initialize completes.
func (s *Session) bindClient(c *interaction) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
	close(s.clientReady)
	return s.closed
}

func (s *Session) Receive(ctx context.Context) (Received, error) { return s.fifo.Pop(ctx) }
func (s *Session) SessionID() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.admission.sessionID()
	return id, id != ""
}
func (s *Session) RequestedResumeID() string { return s.requestedID }
func (s *Session) Done() <-chan struct{}     { return s.done }
func (s *Session) Err() error                { s.mu.Lock(); defer s.mu.Unlock(); return s.cause }
func (s *Session) Submit(ctx context.Context, input Input) (Submission, error) {
	if ctx == nil {
		return Submission{}, dispatchFailure("submit", queue.ErrNilContext)
	}
	if err := s.acquireSettingsGate(ctx); err != nil {
		return Submission{}, dispatchFailure("submit", err)
	}
	defer s.releaseSettingsGate()
	if err := s.applyPromptTrace(ctx); err != nil {
		return Submission{}, err
	}
	err := s.client.sendInput(ctx, input)
	return Submission{Written: err == nil}, err
}

func (s *Session) acquireSettingsGate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.settingsGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.settingsGate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) releaseSettingsGate() { <-s.settingsGate }

func (s *Session) Interrupt(ctx context.Context, _ InterruptOptions) error {
	if ctx == nil {
		return queue.ErrNilContext
	}
	select {
	case <-s.clientReady:
		s.mu.Lock()
		c := s.client
		s.mu.Unlock()
		return c.Interrupt(ctx)
	case <-s.done:
		if err := s.Err(); err != nil {
			return err
		}
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) Close() error {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		return c.Close()
	}
	return nil
}

// CloseInput half-closes the SDK outbound writer while retaining the reader for
// final native frames. It is available only for stream-backed Sessions.
func (s *Session) CloseInput() error {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c == nil {
		return ErrClosed
	}
	return c.CloseInput()
}

func (s *Session) Abort() error { err := s.Close(); _ = s.fifo.Abort(s.Err()); return err }
func (s *Session) finish(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if cause != nil && (s.cause == nil ||
			errors.Is(s.cause, ErrUnexpectedEOF) ||
			errors.Is(s.cause, ErrClosed)) {
			// Local Close and premature stream EOF are not authoritative peer
			// outcomes. A later completion failure still becomes observable.
			s.cause = cause
		}
		return
	}
	s.closed = true
	s.cause = cause
	close(s.done)
	s.admission.release()
	_ = s.fifo.Close(cause)
}
func (c *interaction) acceptEvent(e Event) error { return c.session.accept(e) }

// accept runs in the native reader. Keep admission and FIFO commit under the
// lifecycle lock so requests and termination cannot overtake an accepted frame.
func (s *Session) accept(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	records, err := s.admission.event(e)
	return s.deliver(records, err)
}

// deliver commits one frame's records while mu is held. A terminal admission
// error may accompany an offending native frame that must remain drainable.
func (s *Session) deliver(records []Received, admissionErr error) error {
	items := make([]queue.Item[Received], 0, len(records))
	for _, r := range records {
		items = append(items, queue.Item[Received]{Value: r, Bytes: int64(len(r.Raw))})
	}
	err := s.fifo.PushBatch(items)
	if admissionErr != nil {
		return admissionErr
	}
	return err
}

// UnattributedRecord is diagnostic material, never a live fact or answer handle.
type UnattributedRecord struct {
	Event      Event
	ReceivedAt time.Time
	Reason     string
}

// UnattributedSnapshot declares exactly the bounded retention and coverage gap.
type UnattributedSnapshot struct {
	Records              []UnattributedRecord
	Bytes                int
	Dropped, Expired     uint64
	MaxRecords, MaxBytes int
	Retention            time.Duration
	CoverageGap          bool
}

func (s *Session) Unattributed() UnattributedSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admission.snapshot()
}
