// SDK-specific fixtures and process adapters; native protocol matching belongs to the tool.
package recording

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// fixtures is the shared embedded fixture source used by offline tooling.
var fixtures = fixturedata.Files

type Frame struct {
	Ordinal   int    `json:"ordinal"`
	Direction string `json:"direction"`
	Channel   string `json:"channel"`
	Epoch     int    `json:"epoch"`
	ElapsedMS int64  `json:"elapsedMs"`
	// DelayMS and Disconnect are synthetic scenario actions, never inferred from capture timing.
	DelayMS    int64           `json:"delayMs,omitempty"`
	Disconnect bool            `json:"disconnect,omitempty"`
	Wire       json.RawMessage `json:"wire"`
}

// QuestionFrames supplies an independently authored SDK integration scenario.
func QuestionFrames() ([]Frame, error) {
	b, err := fixtures.ReadFile("integration/question.jsonl")
	if err != nil {
		return nil, err
	}
	return Decode(bytes.NewReader(b))
}
func Decode(r io.Reader) ([]Frame, error) {
	b, err := io.ReadAll(io.LimitReader(r, ProtocolLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > ProtocolLimit {
		return nil, errors.New("capture decode byte limit")
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Buffer(make([]byte, 4096), 2<<20)
	var frames []Frame
	for s.Scan() {
		var f Frame
		if err := json.Unmarshal(s.Bytes(), &f); err != nil {
			return nil, err
		}
		if f.Ordinal <= 0 || len(f.Wire) == 0 || f.Epoch != 1 || (f.Direction != "c2s" && f.Direction != "s2c") {
			return nil, errors.New("invalid frame")
		}
		if len(frames) > 0 && f.Ordinal <= frames[len(frames)-1].Ordinal {
			return nil, errors.New("ordinal order")
		}
		frames = append(frames, f)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, errors.New("empty transcript")
	}
	return frames, nil
}

// Peer runs the synthetic SDK test protocol with answer and completion barriers.
func Peer(in io.Reader, out io.Writer, frames []Frame) error {
	return mockPeer(context.Background(), in, out, frames, "answer-and-assert")
}
func Params(frames []Frame) (types.InitializeParams, types.ThreadStartParams, types.TurnStartParams, error) {
	var init types.InitializeParams
	var thread types.ThreadStartParams
	var turn types.TurnStartParams
	count := 0
	for _, f := range frames {
		if f.Direction != "c2s" {
			continue
		}
		m, err := object(f.Wire)
		if err != nil {
			return init, thread, turn, err
		}
		switch string(m["method"]) {
		case `"initialize"`:
			err = json.Unmarshal(m["params"], &init)
			count++
		case `"thread/start"`:
			err = json.Unmarshal(m["params"], &thread)
			count++
		case `"turn/start"`:
			err = json.Unmarshal(m["params"], &turn)
			count++
		}
		if err != nil {
			return init, thread, turn, err
		}
	}
	if count != 3 {
		return init, thread, turn, errors.New("missing or repeated lifecycle request")
	}
	return init, thread, turn, nil
}

func object(b []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	err := json.Unmarshal(b, &m)
	if m == nil && err == nil {
		err = errors.New("not object")
	}
	return m, err
}
