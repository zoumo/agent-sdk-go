// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// LoadOptions fixes the native resume identity, data home and bounded history.
// WorkDir and ResumeID may be omitted; supplied values must match History.
type LoadOptions struct {
	Options Options
	History HistoryOptions
}

var (
	ErrLoadPending       = errors.New("claude: session load has not completed")
	ErrLoadFailed        = errors.New("claude: session load failed")
	ErrResumeUnconfirmed = errors.New("claude: native resume identity is unconfirmed")
	ErrIdentityMismatch  = errors.New("claude: native resume identity mismatch")
)

type LoadStatus string

const (
	LoadNotRequested LoadStatus = "not-requested"
	LoadPending      LoadStatus = "pending"
	LoadSucceeded    LoadStatus = "succeeded"
	LoadFailed       LoadStatus = "failed"
)

// LoadState separates requested and actually observed identity. Evidence is a
// resume confirmation source, not a statement that initialize confirmed it.
type LoadState struct {
	Status      LoadStatus
	RequestedID string
	ObservedID  string
	Evidence    string
	Err         error
}

// IdentityMismatchError retains the actual native frame for diagnosis. Raw may
// contain private content; callers must redact it before logging.
type IdentityMismatchError struct {
	RequestedID, ObservedID string
	Raw                     json.RawMessage
}

func (e *IdentityMismatchError) Error() string {
	return "claude: loaded native session identity differs from requested identity"
}
func (e *IdentityMismatchError) Unwrap() []error { return []error{ErrIdentityMismatch, ErrProtocol} }

func (s *Session) LoadState() LoadState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSnapshotLocked()
}

// inputAllowed gates only normal input/configuration, never real request
// answers or native interrupt. No deferred input or configuration queue exists.
func (s *Session) inputAllowed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.load.Status {
	case LoadPending:
		return ErrLoadPending
	case LoadFailed:
		return errors.Join(ErrLoadFailed, s.load.Err)
	}
	return nil
}

// Load returns a consumable Session before startup completes. The fixed local
// TS SDK 0.3.258 / CLI 2.1.258 public contract has no proven no-input resume
// confirmation path. After initialize, Load therefore fails explicitly, releases
// the process and never admits history or ordinary input. See
// docs/research/claude-session-native-evidence.md for the fixed evidence limits.
func Load(ctx context.Context, o LoadOptions) (*Session, error) {
	opts, path, err := prepareHistory(ctx, o.History)
	if err != nil {
		return nil, err
	}
	process := o.Options
	if process.ResumeID != "" && process.ResumeID != opts.SessionID {
		return nil, errors.New("claude load: conflicting resume identity")
	}
	if process.WorkDir != "" {
		cwd, e := filepath.EvalSymlinks(process.WorkDir)
		if e != nil {
			return nil, e
		}
		if cwd != opts.CWD {
			return nil, errors.New("claude load: conflicting cwd")
		}
	}
	process.ResumeID = opts.SessionID
	process.WorkDir = opts.CWD
	env := process.Env
	if env == nil {
		env = os.Environ()
	}
	process.Env = make([]string, 0, len(env)+1)
	for _, v := range env {
		if !strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") {
			process.Env = append(process.Env, v)
		}
	}
	process.Env = append(process.Env, "CLAUDE_CONFIG_DIR="+opts.ConfigDir)
	if err := process.defaults(); err != nil {
		return nil, err
	}
	s, err := newSession(process)
	if err != nil {
		return nil, err
	}
	loadCtx, cancel := context.WithCancel(ctx)
	s.load = LoadState{Status: LoadPending, RequestedID: opts.SessionID}
	s.requestedID = opts.SessionID
	s.loadCancel = cancel
	s.loadDone = make(chan struct{})
	go func() {
		defer close(s.loadDone)
		defer cancel()
		err := replayTranscript(loadCtx, opts, path, func(Event) error {
			// No confirmed recovery route exists on this baseline. This callback is
			// intentionally unreachable; a future route must admit to s.fifo directly.
			return ErrResumeUnconfirmed
		}, func(ctx context.Context) error {
			_, err := openInteraction(ctx, process, opts.SessionID, s)
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return ErrResumeUnconfirmed
		})
		if err == nil {
			err = ErrResumeUnconfirmed
		}
		s.mu.Lock()
		c := s.client
		s.mu.Unlock()
		if c != nil {
			c.mu.Lock()
			if !c.stopped {
				c.failLocked(err)
			}
			c.mu.Unlock()
			_ = c.Close()
		} else {
			s.finish(err)
		}
	}()
	return s, nil
}

// Snapshots detach mutable native diagnostics as well as the state value. A
// caller inspecting a boundary must not change subsequent LoadState results.
func (s *Session) loadSnapshotLocked() LoadState {
	state := s.load
	state.Err = snapshotLoadError(state.Err)
	return state
}

type loadErrorCauses struct {
	message string
	causes  []error
}

func (e *loadErrorCauses) Error() string   { return e.message }
func (e *loadErrorCauses) Unwrap() []error { return e.causes }

func snapshotLoadError(err error) error {
	switch e := err.(type) {
	case *IdentityMismatchError:
		copy := *e
		copy.Raw = append(json.RawMessage(nil), e.Raw...)
		return &copy
	case *HistoryError:
		copy := *e
		copy.Raw = append(json.RawMessage(nil), e.Raw...)
		return &copy
	case *ControlError:
		copy := *e
		copy.Raw = append(json.RawMessage(nil), e.Raw...)
		return &copy
	case *Error:
		copy := *e
		copy.Err = snapshotLoadError(e.Err)
		if e.ExitCode != nil {
			code := *e.ExitCode
			copy.ExitCode = &code
		}
		return &copy
	case *ResourceLimitError:
		copy := *e
		return &copy
	case *os.PathError:
		copy := *e
		copy.Err = snapshotLoadError(e.Err)
		return &copy
	case interface{ Unwrap() []error }:
		causes := e.Unwrap()
		copies := make([]error, len(causes))
		for i, cause := range causes {
			copies[i] = snapshotLoadError(cause)
		}
		return &loadErrorCauses{message: err.Error(), causes: copies}
	case interface{ Unwrap() error }:
		return &loadErrorCauses{message: err.Error(), causes: []error{snapshotLoadError(e.Unwrap())}}
	default:
		return err
	}
}
