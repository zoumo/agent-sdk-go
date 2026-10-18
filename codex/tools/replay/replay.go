// Package recording provides protocol-specific capture and offline replay tooling.
package recording

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// fixtures is the shared embedded fixture source used by offline tooling.
var fixtures = fixturedata.Files

type Frame struct {
	Ordinal   int             `json:"ordinal"`
	Direction string          `json:"direction"`
	Channel   string          `json:"channel"`
	Epoch     int             `json:"epoch"`
	ElapsedMS int64           `json:"elapsedMs"`
	Wire      json.RawMessage `json:"wire"`
}

func Load(story string) ([]Frame, error) {
	if story != "s1" && story != "s2" {
		return nil, errors.New("unknown story")
	}
	b, err := fixtures.ReadFile("recordings/real-sanitized/" + story + "/protocol.jsonl")
	if err != nil {
		return nil, err
	}
	return Decode(bytes.NewReader(b))
}
func Decode(r io.Reader) ([]Frame, error) {
	s := bufio.NewScanner(io.LimitReader(r, 2<<20+1))
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
func object(b []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	err := json.Unmarshal(b, &m)
	if m == nil && err == nil {
		err = errors.New("not object")
	}
	return m, err
}
func equal(a, b any) bool { return reflect.DeepEqual(a, b) }
func canonical(b []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&v)
	return v
}

// Peer follows captured dependencies, never wall-clock delays. Each client RPC ID
// is rebound to the current SDK ID; reverse IDs remain in their separate namespace.
// The final private assertion proves that every retained frame was consumed.
func Peer(in io.Reader, out io.Writer, frames []Frame) error {
	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 4096), 2<<20)
	ids := map[string]json.RawMessage{}
	active := map[string]bool{}
	reverse := map[string]bool{}
	for _, f := range frames {
		want, err := object(f.Wire)
		if err != nil {
			return err
		}
		if f.Direction == "c2s" {
			if !s.Scan() {
				return errors.New("early client EOF")
			}
			got, err := object(s.Bytes())
			if err != nil {
				return err
			}
			if _, rpc := want["method"]; rpc && want["id"] != nil {
				key := string(want["id"])
				if active[key] || ids[key] != nil {
					return errors.New("duplicate recorded request ID")
				}
				if got["id"] == nil {
					return errors.New("missing client ID")
				}
				for _, id := range ids {
					if bytes.Equal(id, got["id"]) {
						return errors.New("duplicate current request ID")
					}
				}
				ids[key] = append(json.RawMessage(nil), got["id"]...)
				active[key] = true
				got["id"] = want["id"]
			} else if want["id"] != nil {
				key := string(want["id"])
				if !reverse[key] {
					return errors.New("unknown or duplicate reverse answer")
				}
				delete(reverse, key)
			}
			if !equal(canonical(mustJSON(got)), canonical(f.Wire)) {
				return fmt.Errorf("client frame mismatch at ordinal %d", f.Ordinal)
			}
		} else {
			if id := want["id"]; id != nil {
				key := string(id)
				if want["method"] != nil {
					if reverse[key] {
						return errors.New("duplicate reverse ID")
					}
					reverse[key] = true
				} else {
					if !active[key] || ids[key] == nil {
						return errors.New("response without request")
					}
					want["id"] = ids[key]
					delete(active, key)
				}
			}
			if _, err = fmt.Fprintln(out, string(mustJSON(want))); err != nil {
				return err
			}
		}
	}
	if len(active) != 0 || len(reverse) != 0 {
		return errors.New("unfinished protocol requests")
	}
	if !s.Scan() {
		return errors.New("missing completion assertion")
	}
	m, err := object(s.Bytes())
	if err != nil {
		return err
	}
	if string(m["method"]) != `"replay/assert"` || m["id"] == nil || m["params"] != nil {
		return errors.New("extra client request")
	}
	if _, err = fmt.Fprintf(out, "{\"id\":%s,\"result\":{}}\n", m["id"]); err != nil {
		return err
	}
	if s.Scan() {
		return errors.New("request after assertion")
	}
	return s.Err()
}
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Handler exposes two bounded operations on synthetic data only. Paths must be
// exact basenames, regular files, and resolve into the isolated root. No shell.
type Handler struct {
	Root      string
	Story     string
	Calls     int
	Questions int
}

func (h *Handler) Answer(ctx context.Context, r codex.Request) error {
	switch r.Method() {
	case "item/tool/call":
		var p struct {
			Tool      string `json:"tool"`
			Arguments struct {
				Path string `json:"path"`
				Text string `json:"text"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params(), &p); err != nil {
			return err
		}
		if h.Story != "s1" || h.Calls >= 2 {
			return r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"})
		}
		var text string
		var err error
		switch {
		case p.Tool == "fixture_read" && p.Arguments.Path == "numbers.txt" && h.Calls == 0:
			var b []byte
			b, err = readSynthetic(h.Root)
			text = string(b)
		case p.Tool == "fixture_write" && p.Arguments.Path == "summary.txt" && p.Arguments.Text == "sum=42\n" && h.Calls == 1:
			err = writeSynthetic(h.Root, p.Arguments.Text)
			text = "written summary.txt"
		default:
			return r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"})
		}
		if err != nil {
			return err
		}
		h.Calls++
		return r.Handle().Answer(ctx, map[string]any{"contentItems": []any{map[string]any{"type": "inputText", "text": text}}, "success": true})
	case "item/tool/requestUserInput":
		var p types.ToolRequestUserInputParams
		if err := json.Unmarshal(r.Params(), &p); err != nil {
			return err
		}
		if h.Story != "s2" || h.Questions != 0 || len(p.Questions) != 1 {
			return r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "question denied"})
		}
		q := p.Questions[0]
		options, ok := q.Options.Get()
		if !ok || len(options) != 2 || options[0].Label != "Amber" || options[1].Label != "Indigo" || q.IsSecret.ValueOr(false) {
			return r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "question denied"})
		}
		h.Questions++
		return r.Handle().Answer(ctx, types.ToolRequestUserInputResponse{Answers: map[string]types.ToolRequestUserInputAnswer{q.ID: {Answers: []string{"Amber"}}}})
	default:
		return r.Handle().Reject(ctx, &codex.RPCError{Code: -32601, Message: "request denied"})
	}
}

type Observation struct {
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"`
	Method   string `json:"method,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}
type Result struct {
	Observations []Observation `json:"observations"`
	Tools        int           `json:"tools"`
	Questions    int           `json:"questions"`
	Completed    bool          `json:"completed"`
}

// Session uses only the final public managed API. Requests are answered on the
// original handle while the subscription continues to drain independently.
func Session(ctx context.Context, c *codex.Client, thread types.ThreadStartParams, turn types.TurnStartParams, h *Handler) (Result, error) {
	var result Result
	started, err := c.StartThread(ctx, thread)
	if err != nil {
		return result, err
	}
	sub, err := c.Subscribe(ctx, started.Thread.ID, types.SubscriptionOptions{EventQueue: 2048})
	if err != nil {
		return result, err
	}
	defer sub.Close()
	turn.ThreadID = started.Thread.ID
	turnResult := make(chan error, 1)
	go func() { _, e := c.StartTurn(ctx, turn); turnResult <- e }()
	turnResponded := false
	for !result.Completed || !turnResponded {
		select {
		case e := <-turnResult:
			if e != nil {
				return result, e
			}
			turnResponded = true
			turnResult = nil
		case r, ok := <-c.Requests():
			if !ok {
				return result, errors.New("requests closed")
			}
			answerCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			e := h.Answer(answerCtx, r)
			cancel()
			if e != nil {
				return result, e
			}
		case e, ok := <-sub.Events():
			if !ok {
				return result, errors.New("subscription closed early")
			}
			o := Observation{Sequence: uint64(len(result.Observations) + 1), Kind: "Native"}
			if e.Raw != nil {
				if e.DecodeError != nil {
					return result, errors.New("native decode contract")
				}
				raw, parseErr := object(e.Raw)
				if parseErr != nil || string(raw["method"]) != string(mustJSON(e.Method)) || !equal(canonical(raw["params"]), canonical(e.Params)) {
					return result, errors.New("Raw/Params contract")
				}
				var params map[string]json.RawMessage
				_ = json.Unmarshal(e.Params, &params)
				_ = json.Unmarshal(params["threadId"], &o.ThreadID)
				switch e.Method {
				case "turn/started", "turn/completed", "item/started", "item/completed", "item/agentMessage/delta", "serverRequest/resolved":
					if e.Data == nil {
						return result, errors.New("missing typed native data")
					}
				}
				o.Method = e.Method
				if o.Method == "turn/completed" {
					result.Completed = true
				}
			} else {
				if _, ok := e.Data.(*types.ObservationState); !ok || e.Method != "" || e.Params != nil {
					return result, errors.New("unexpected convenience event in root-only story")
				}
				o.Kind = "ObservationState"
			}
			result.Observations = append(result.Observations, o)
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
	result.Tools = h.Calls
	result.Questions = h.Questions
	return result, nil
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
func readSynthetic(root string) ([]byte, error) {
	p := root + "/numbers.txt"
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not regular")
	}
	b, err := os.ReadFile(p)
	if err == nil && string(b) != "17\n25\n" {
		err = errors.New("input changed")
	}
	return b, err
}
func writeSynthetic(root, text string) error {
	f, err := os.OpenFile(root+"/summary.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}

func Replay(ctx context.Context, executable, story string, out io.Writer) error {
	frames, err := Load(story)
	if err != nil {
		return err
	}
	init, thread, turn, err := Params(frames)
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "recorded-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err = os.WriteFile(root+"/numbers.txt", []byte("17\n25\n"), 0600); err != nil {
		return err
	}
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: []string{"--peer", story}, Env: []string{}, Initialize: init, Limits: codex.Limits{EventQueue: 2048}})
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := Session(ctx, c, thread, turn, &Handler{Root: root, Story: story})
	if err != nil {
		return err
	}
	if err = c.Call(ctx, "replay/assert", nil, nil); err != nil {
		return err
	}
	expectedRaw, err := fixtures.ReadFile("recordings/real-sanitized/" + story + "/expected-observations.json")
	if err != nil {
		return err
	}
	var expected Result
	if err = json.Unmarshal(expectedRaw, &expected); err != nil {
		return err
	}
	if result.Completed != expected.Completed || result.Tools != expected.Tools || result.Questions != expected.Questions {
		return errors.New("live/replay coverage mismatch")
	}
	counts := func(observations []Observation) map[string]int {
		m := map[string]int{}
		for _, o := range observations {
			if o.Method != "" && o.Method != "thread/started" {
				m[o.Method]++
			}
		}
		return m
	}
	if !reflect.DeepEqual(counts(result.Observations), counts(expected.Observations)) {
		return errors.New("live/replay notification mismatch")
	}
	for _, o := range result.Observations {
		if o.Method != "" {
			fmt.Fprintf(out, "%s %s source=%s\n", o.Kind, o.Method, o.ThreadID)
		}
	}
	fmt.Fprintf(out, "%s completed=%t tools=%d questions=%d; coverage remains best-effort\n", story, result.Completed, result.Tools, result.Questions)
	return c.Close()
}
