// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Ledger is one durable aggregate across tickets, not one budget per process.
// The coordinator must retain/reconcile spent historical ledgers; creating a new
// path never grants a fresh live budget. Ambiguous/crashed writes fail closed.
type Ledger struct {
	path     string
	identity os.FileInfo
}
type ledgerEntry struct {
	Sequence int    `json:"sequence"`
	Scene    string `json:"scene"`
	Action   string `json:"action"`
	Outcome  string `json:"outcome,omitempty"`
}
type attemptState struct {
	turns   int
	outcome string
}

// OpenLedger accepts only a canonical private directory. Legacy story markers
// cannot be silently migrated/reset: an operator must reconcile them separately.
func OpenLedger(path string) (*Ledger, error) {
	if err := canonicalPath(path); err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	// Persist the directory name even when an earlier creation/sync was interrupted.
	// Syncing the ledger itself does not make its parent's entry durable.
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("ledger must be a private directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name() != "attempts.jsonl" && e.Name() != "locked" {
			return nil, fmt.Errorf("unreconciled legacy/unknown ledger material")
		}
	}
	return &Ledger{path: path, identity: info}, nil
}

// Reserve durably charges an entire attempt and its maximum submitted-turn
// allowance before startup, including failures with no native telemetry.
func (l *Ledger) Reserve(scene string) error { return l.append(scene, "reserve", "") }

// Turn persists dispatch intent before Submit; unknown delivery never refunds it.
func (l *Ledger) Turn(scene string) error { return l.append(scene, "turn", "") }

// Finish records cleanup-inclusive success/failure. Failed or unfinished attempts
// stop all later scenes. An outcome must never be inferred from subtype alone.
func (l *Ledger) Finish(scene string, success bool) error {
	outcome := "failed"
	if success {
		outcome = "succeeded"
	}
	return l.append(scene, "finish", outcome)
}
func (l *Ledger) append(scene, action, outcome string) error {
	if _, err := Plan(scene); err != nil {
		return err
	}
	if err := canonicalPath(l.path); err != nil {
		return err
	}
	info, err := os.Lstat(l.path)
	if err != nil || !os.SameFile(l.identity, info) || info.Mode().Perm() != 0700 {
		return fmt.Errorf("ledger directory replaced or permissions changed")
	}
	// Atomic directory creation also serializes independent processes. A stale
	// crash lock is intentionally not removed automatically.
	lock := filepath.Join(l.path, "locked")
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("ledger locked or unavailable: %w", err)
	}
	defer os.Remove(lock)
	if err := syncDirectory(l.path); err != nil {
		return err
	}
	path := filepath.Join(l.path, "attempts.jsonl")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return fmt.Errorf("ledger file must be private and regular")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	state := map[string]attemptState{}
	scanner := bufio.NewScanner(io.LimitReader(f, (256<<10)+1))
	count, total := 0, 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		count++
		var e ledgerEntry
		d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		d.DisallowUnknownFields()
		if d.Decode(&e) != nil || e.Sequence != count {
			return fmt.Errorf("invalid ledger; no launch")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return fmt.Errorf("invalid ledger JSON")
		}
		if err := applyEntry(state, e); err != nil {
			return fmt.Errorf("invalid ledger: %w", err)
		}
	}
	if scanner.Err() != nil || total > 256<<10 {
		return fmt.Errorf("ledger exceeds bounds or is corrupt")
	}
	// Every committed record ends in a newline; a torn append must not be healed.
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if size > 0 {
		var last [1]byte
		if _, err = f.ReadAt(last[:], size-1); err != nil || last[0] != '\n' {
			return fmt.Errorf("torn ledger write")
		}
	}
	next := ledgerEntry{Sequence: count + 1, Scene: scene, Action: action, Outcome: outcome}
	if err := applyEntry(state, next); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(b)+1+total > 256<<10 {
		return fmt.Errorf("ledger byte limit")
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return syncDirectory(l.path)
}
func applyEntry(state map[string]attemptState, e ledgerEntry) error {
	plan, err := Plan(e.Scene)
	if err != nil {
		return err
	}
	s, exists := state[e.Scene]
	switch e.Action {
	case "reserve":
		if exists || e.Outcome != "" {
			return fmt.Errorf("scene already reserved")
		}
		roots, turns := 0, 0
		for name, previous := range state {
			if previous.outcome != "succeeded" {
				return fmt.Errorf("prior attempt unfinished or failed")
			}
			p, _ := Plan(name)
			roots++
			turns += p.Turns
		}
		if roots+1 > 3 || turns+plan.Turns > 6 {
			return fmt.Errorf("aggregate root/turn budget exhausted")
		}
		state[e.Scene] = attemptState{}
	case "turn":
		if !exists || s.outcome != "" || s.turns >= plan.Turns || e.Outcome != "" {
			return fmt.Errorf("turn not authorized")
		}
		s.turns++
		state[e.Scene] = s
	case "finish":
		if !exists || s.outcome != "" || (e.Outcome != "failed" && e.Outcome != "succeeded") || (e.Outcome == "succeeded" && s.turns != plan.Turns) {
			return fmt.Errorf("invalid attempt outcome")
		}
		s.outcome = e.Outcome
		state[e.Scene] = s
	default:
		return fmt.Errorf("unknown ledger action")
	}
	return nil
}
func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Reject symlink aliases in every existing component; never chmod existing data.
func canonicalPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("canonical absolute path required")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path refused")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
