package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// AcceptSavedHistory runs captured setup through Session and saved queries through
// typed Client methods. The caller verifies provenance; the Go mock owns matching.
// It supports history and sequential continuation only, and fails closed on other
// operations. It never writes recorded client frames directly to the transport.
func AcceptSavedHistory(ctx context.Context, executable, recordingPath, receiptPath string, policy codex.ChildDetailPolicy, deliveries io.Writer) (err error) {
	f, err := os.Open(recordingPath)
	if err != nil {
		return err
	}
	frames, err := Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	type rpc struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
	}
	var requests []rpc
	responses := map[string]json.RawMessage{}
	for _, frame := range frames {
		var r rpc
		if err := json.Unmarshal(frame.Wire, &r); err != nil {
			return err
		}
		if frame.Direction == "c2s" {
			if r.Method == "" {
				return errors.New("history acceptance does not support client answers")
			}
			requests = append(requests, r)
		} else if len(r.ID) != 0 && len(r.Result) != 0 {
			responses[string(r.ID)] = r.Result
		}
	}
	if len(requests) < 4 || requests[0].Method != "initialize" || requests[1].Method != "initialized" || requests[2].Method != "thread/start" {
		return errors.New("history recording has no single initialization/setup prefix")
	}
	var init types.InitializeParams
	if err := json.Unmarshal(requests[0].Params, &init); err != nil {
		return err
	}
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable,
		Args: []string{"protocol", "mock", "--provider", "codex", "--scenario", recordingPath, "--receipt", receiptPath},
		Env:  []string{}, Initialize: init})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	var start types.ThreadStartParams
	if err := json.Unmarshal(requests[2].Params, &start); err != nil {
		return err
	}
	s, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: start, ChildDetailPolicy: policy})
	if err != nil {
		return err
	}
	queryPhase := false
	observed := json.NewEncoder(deliveries)
	completed := 0
	for _, r := range requests[3:] {
		var result any
		var callErr error
		switch r.Method {
		case "turn/start":
			if queryPhase {
				return errors.New("history query resumed live execution")
			}
			var p types.TurnStartParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return err
			}
			// These recipes intentionally submit only native text inputs. Reject
			// options rather than silently losing them in the Session translation.
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(r.Params, &fields)
			if len(fields) != 2 || p.ThreadID != s.ThreadID() {
				return errors.New("unsupported history input options or identity")
			}
			sub, e := s.Submit(ctx, codex.Input{Input: p.Input})
			if e != nil {
				return e
			}
			result = sub.Response
			for {
				got, e := s.Receive(ctx)
				if e != nil {
					return e
				}
				if got.Request != nil {
					return errors.New("unexpected approval in history setup")
				}
				if err := observed.Encode(struct {
					Kind        codex.ReceivedKind  `json:"kind"`
					Raw         string              `json:"raw,omitempty"`
					Source      types.EventIdentity `json:"source"`
					Origin      string              `json:"origin"`
					Observation *codex.Observation  `json:"observation,omitempty"`
				}{got.Kind, string(got.Raw), got.Source, got.Origin, got.Observation}); err != nil {
					return err
				}
				if got.Event == nil {
					continue
				}
				if got.Event.DecodeError != nil {
					return got.Event.DecodeError
				}
				if got.Event.Method != "turn/completed" {
					continue
				}
				var terminal types.TurnCompletedNotification
				if e := json.Unmarshal(got.Event.Params, &terminal); e != nil {
					return e
				}
				if terminal.ThreadID != s.ThreadID() || terminal.Turn.ID != sub.Response.Turn.ID {
					continue
				}
				if terminal.Turn.Status != types.TurnStatusCompleted {
					return errors.New("recorded setup execution did not succeed")
				}
				completed++
				break
			}
		case "thread/unsubscribe":
			if completed == 0 || queryPhase {
				return errors.New("invalid setup/query boundary")
			}
			queryPhase = true
			if err := s.Close(); err != nil {
				return err
			}
			continue
		case "thread/list":
			if !queryPhase {
				return errors.New("query before setup detached")
			}
			var p types.ThreadListParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return err
			}
			result, callErr = c.ListThreads(ctx, p)
		case "thread/read":
			if !queryPhase {
				return errors.New("query before setup detached")
			}
			var p types.ThreadReadParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return err
			}
			result, callErr = c.ReadThread(ctx, p)
		case "thread/turns/list":
			if !queryPhase {
				return errors.New("query before setup detached")
			}
			var p types.ThreadTurnsListParams
			if err := json.Unmarshal(r.Params, &p); err != nil {
				return err
			}
			result, callErr = c.ListThreadTurns(ctx, p)
		default:
			return fmt.Errorf("unsupported recording operation %s", r.Method)
		}
		if callErr != nil {
			return callErr
		}
		encoded, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if !equal(canonical(encoded), canonical(responses[string(r.ID)])) {
			return fmt.Errorf("Go SDK %s result differs from recorded native response", r.Method)
		}
	}
	if completed == 0 {
		return errors.New("recording has no successful execution")
	}
	return nil
}
