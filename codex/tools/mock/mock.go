// Package mock provides a deterministic offline Codex app-server protocol peer.
// It executes explicit scenarios, never a model, real CLI, shell, or tool.
package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
)

const Version = 1
const MaxScenarioBytes = 8 << 20

// ErrDisconnected is the explicit terminal disconnect action, not normal EOF.
var ErrDisconnected = recording.ErrDisconnected

// Scenario is a versioned strict transcript. Provenance must be "synthetic" or
// "recording"; recording scenarios must identify their source. Frames retain
// captured wires and ordering; elapsedMs never causes an implicit delay. DelayMS
// and Disconnect explicitly add synthetic behavior to a scenario.
type Scenario struct {
	Version    int               `json:"version"`
	Provenance string            `json:"provenance"`
	Source     string            `json:"source,omitempty"`
	TimeoutMS  int64             `json:"timeoutMs,omitempty"`
	Frames     []recording.Frame `json:"frames"`
}

// Load reads one bounded JSON scenario and rejects unknown envelope/frame fields
// and trailing JSON. Wire payloads intentionally remain native arbitrary JSON.
func Load(r io.Reader) (Scenario, error) {
	var scenario Scenario
	b, err := io.ReadAll(io.LimitReader(r, MaxScenarioBytes+1))
	if err != nil {
		return scenario, err
	}
	if len(b) > MaxScenarioBytes {
		return scenario, errors.New("mock scenario byte limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&scenario); err != nil {
		return scenario, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return scenario, errors.New("trailing scenario JSON")
	}
	return scenario, scenario.Validate()
}

// Validate checks the scenario envelope and frame structure without executing it.
func (s Scenario) Validate() error {
	if s.Version != Version {
		return fmt.Errorf("unsupported mock scenario version %d", s.Version)
	}
	if s.Provenance != "synthetic" && s.Provenance != "recording" {
		return errors.New("mock provenance must be synthetic or recording")
	}
	if s.Provenance == "recording" && s.Source == "" {
		return errors.New("recording scenario requires source")
	}
	if s.TimeoutMS < 0 || s.TimeoutMS > 300000 {
		return errors.New("mock timeout must be between 0 and 300000 ms")
	}
	if len(s.Frames) == 0 || len(s.Frames) > 10000 {
		return errors.New("mock frame count limit")
	}
	total := 0
	for i, f := range s.Frames {
		total += len(f.Wire) + 1
		if total > MaxScenarioBytes || len(f.Wire) > recording.ProtocolLimit {
			return errors.New("mock frame byte limit")
		}
		if f.Ordinal <= 0 || (i > 0 && f.Ordinal <= s.Frames[i-1].Ordinal) || f.Epoch != 1 {
			return errors.New("mock frames require increasing ordinals and epoch 1")
		}
		if f.Direction != "c2s" && f.Direction != "s2c" {
			return errors.New("invalid mock direction")
		}
		if f.DelayMS < 0 || f.DelayMS > 300000 {
			return errors.New("mock delay out of range")
		}
		if s.Provenance == "recording" && (f.DelayMS != 0 || f.Disconnect) {
			return errors.New("scripted faults/delays require synthetic provenance")
		}
		if f.Disconnect {
			if i != len(s.Frames)-1 || f.Direction != "s2c" || (len(f.Wire) != 0 && string(f.Wire) != "null") {
				return errors.New("disconnect must be final server action without wire")
			}
			continue
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(f.Wire, &wire); err != nil || wire == nil {
			return errors.New("mock wire must be a JSON object")
		}
	}
	return nil
}

// Run owns and closes both streams. Close must interrupt outstanding I/O; use
// files, pipes or connections. All frames must match, all requests must finish,
// and client EOF must follow; a terminal Disconnect returns ErrDisconnected and
// explicitly allows unfinished requests. The deadline defaults to 30 seconds and is capped at five
// minutes, in addition to the supplied context deadline.
func Run(ctx context.Context, in io.ReadCloser, out io.WriteCloser, scenario Scenario) error {
	if ctx == nil || in == nil || out == nil {
		return errors.New("mock requires context and streams")
	}
	defer in.Close()
	defer out.Close()
	if err := scenario.Validate(); err != nil {
		return err
	}
	timeout := 30 * time.Second
	if scenario.TimeoutMS != 0 {
		timeout = time.Duration(scenario.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return recording.RunPeer(ctx, in, out, scenario.Frames)
}
