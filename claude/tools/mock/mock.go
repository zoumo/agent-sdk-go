// SPDX-License-Identifier: Apache-2.0

// Package mock runs deterministic native Claude Code stream-json peers. It never
// invokes an agent or executes a tool. The SDK and its transport remain real.
package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	replay "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

const (
	Version          = 1
	MaxScenarioBytes = 16 << 20
	MaxFrames        = 10000
	MaxFrameBytes    = 2 << 20
	// DefaultTimeout bounds the entire conversation, including waiting for EOF.
	DefaultTimeout = 10 * time.Second
)

// Frame is the replay engine's native frame. Ordinals start at one; channels
// are protocol or control. Direction is client (exact structural expectation),
// server (emit), or disconnect (no Data, last frame). DelayMillis is bounded to
// 30000. Client control IDs bind dynamically; reverse request IDs remain exact.
type Frame = replay.Frame

// Scenario is a versioned conversation, explicitly labeled synthetic or recording.
// Recording scenarios should retain their original capture's separate manifest;
// this wrapper does not claim that synthetic additions are recorded evidence.
type Scenario struct {
	Version    int     `json:"version"`
	Provenance string  `json:"provenance"`
	Source     string  `json:"source,omitempty"`
	Frames     []Frame `json:"frames"`
}

// Load reads a bounded strict scenario document. Unknown wrapper/frame fields,
// unsupported versions, invalid JSON and trailing documents are rejected.
func Load(input io.Reader) (Scenario, error) {
	var scenario Scenario
	data, err := io.ReadAll(io.LimitReader(input, MaxScenarioBytes+1))
	if err != nil {
		return scenario, err
	}
	if len(data) > MaxScenarioBytes {
		return scenario, fmt.Errorf("scenario exceeds %d bytes", MaxScenarioBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return scenario, fmt.Errorf("scenario: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return scenario, fmt.Errorf("scenario: trailing JSON")
	}
	if err := scenario.Validate(); err != nil {
		return scenario, err
	}
	return scenario, nil
}

// Validate checks the envelope and all frame limits before any protocol output.
// Request correlation and structural client equality are checked during Run.
func (s Scenario) Validate() error {
	if s.Version != Version {
		return fmt.Errorf("unsupported scenario version %d", s.Version)
	}
	if s.Provenance != "synthetic" && s.Provenance != "recording" {
		return fmt.Errorf("provenance must be synthetic or recording")
	}
	if s.Provenance == "recording" && s.Source == "" {
		return fmt.Errorf("recording scenario requires source")
	}
	if len(s.Frames) == 0 || len(s.Frames) > MaxFrames {
		return fmt.Errorf("scenario requires 1..%d frames", MaxFrames)
	}
	total := 0
	for i, f := range s.Frames {
		if f.Ordinal != i+1 {
			return fmt.Errorf("invalid ordinal at %d", i+1)
		}
		if f.Channel != "protocol" && f.Channel != "control" {
			return fmt.Errorf("invalid channel at %d", i+1)
		}
		if f.DelayMillis < 0 || f.DelayMillis > 30000 {
			return fmt.Errorf("invalid delay at %d", i+1)
		}
		if s.Provenance == "recording" && (f.DelayMillis != 0 || f.Direction == "disconnect") {
			return fmt.Errorf("delay/disconnect additions require synthetic provenance")
		}
		switch f.Direction {
		case "client", "server":
			if !json.Valid(f.Data) || len(bytes.TrimSpace(f.Data)) == 0 || bytes.TrimSpace(f.Data)[0] != '{' {
				return fmt.Errorf("native frame must be a JSON object at %d", i+1)
			}
		case "disconnect":
			if len(f.Data) != 0 || i != len(s.Frames)-1 {
				return fmt.Errorf("disconnect must be last with no data")
			}
		default:
			return fmt.Errorf("invalid direction at %d", i+1)
		}
		raw, err := json.Marshal(f)
		if err != nil {
			return err
		}
		if len(raw) >= MaxFrameBytes {
			return fmt.Errorf("frame too large at %d", i+1)
		}
		total += len(raw) + 1
		if total > MaxScenarioBytes {
			return fmt.Errorf("scenario exceeds %d bytes", MaxScenarioBytes)
		}
	}
	return nil
}

// Run owns both streams and closes them before returning. Their Close methods
// must unblock pending Read/Write calls (as os.File, net.Conn and io.Pipe do).
// A shorter caller deadline is honored; otherwise DefaultTimeout is the upper
// bound. Successful scripts require client EOF after all frames, so unexpected
// trailing messages cannot silently pass. An intentional disconnect returns
// replay.ErrDisconnected, distinct from an incomplete or mismatched scenario.
func Run(ctx context.Context, input io.ReadCloser, output io.WriteCloser, scenario Scenario) error {
	if ctx == nil || input == nil || output == nil {
		return fmt.Errorf("owned input and output streams required")
	}
	defer input.Close()
	defer output.Close()
	if err := scenario.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			input.Close()
			output.Close()
		case <-stop:
		}
	}()
	defer func() { close(stop); <-done }()
	var capture bytes.Buffer
	for _, frame := range scenario.Frames {
		if err := json.NewEncoder(&capture).Encode(frame); err != nil {
			return err
		}
	}
	err := replay.ReplayWithOptions(input, output, &capture, nil, replay.ReplayOptions{Context: ctx, MaxFrames: MaxFrames})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
