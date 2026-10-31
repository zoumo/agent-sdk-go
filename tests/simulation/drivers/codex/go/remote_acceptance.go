package codexsim

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

type remoteRPC struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func remoteRequests(path string) ([]remoteRPC, map[string]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	frames, err := recording.Decode(f)
	_ = f.Close()
	if err != nil {
		return nil, nil, err
	}
	var requests []remoteRPC
	responses := map[string]json.RawMessage{}
	for _, frame := range frames {
		var rpc remoteRPC
		if err := json.Unmarshal(frame.Wire, &rpc); err != nil {
			return nil, nil, err
		}
		if frame.Direction == "c2s" {
			requests = append(requests, rpc)
		} else if len(rpc.ID) > 0 && len(rpc.Result) > 0 {
			responses[string(rpc.ID)] = rpc.Result
		}
	}
	return requests, responses, nil
}

// AcceptRemoteClose runs NewRemote against the runner-owned offline WebSocket service.
// The runner independently verifies both peer receipts and service cleanup. These are SDK simulation results, not native remote E2E.
// The caller verifies the case provenance before invoking this adapter.
func AcceptRemoteClose(ctx context.Context, root, endpoint, token string) (err error) {
	paths := []string{filepath.Join(root, "raw/raw.jsonl"), filepath.Join(root, "remote-probe/raw/raw.jsonl")}
	mainRequests, mainResponses, err := remoteRequests(paths[0])
	if err != nil {
		return err
	}
	probeRequests, probeResponses, err := remoteRequests(paths[1])
	if err != nil {
		return err
	}
	if len(mainRequests) != 4 || mainRequests[0].Method != "initialize" || mainRequests[1].Method != "initialized" || mainRequests[2].Method != "thread/start" || mainRequests[3].Method != "turn/start" {
		return errors.New("remote close recording must have one successful setup execution")
	}
	if len(probeRequests) != 3 || probeRequests[0].Method != "initialize" || probeRequests[1].Method != "initialized" || probeRequests[2].Method != "thread/read" {
		return errors.New("remote probe must independently initialize and read")
	}
	open := func(rpc remoteRPC) (*codex.Client, error) {
		var init types.InitializeParams
		if err := json.Unmarshal(rpc.Params, &init); err != nil {
			return nil, err
		}
		return codex.NewRemote(ctx, codex.RemoteOptions{URL: endpoint, BearerToken: token, Initialize: init})
	}
	check := func(value any, wanted json.RawMessage) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !equal(canonical(encoded), canonical(wanted)) {
			return errors.New("remote SDK result differs from the original native recording")
		}
		return nil
	}
	first, err := open(mainRequests[0])
	if err != nil {
		return err
	}
	defer first.Close()
	if err := check(first.ServerInfo(), mainResponses[string(mainRequests[0].ID)]); err != nil {
		return err
	}
	var start types.ThreadStartParams
	if err := json.Unmarshal(mainRequests[2].Params, &start); err != nil {
		return err
	}
	session, err := codex.Open(ctx, codex.SessionOptions{Client: first, Thread: start})
	if err != nil {
		return err
	}
	started, ok := session.StartResponse()
	if !ok {
		return errors.New("remote Session omitted its start response")
	}
	if err := check(started, mainResponses[string(mainRequests[2].ID)]); err != nil {
		return err
	}
	var turn types.TurnStartParams
	if err := json.Unmarshal(mainRequests[3].Params, &turn); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mainRequests[3].Params, &fields); err != nil {
		return err
	}
	if len(fields) != 2 || turn.ThreadID != session.ThreadID() {
		return errors.New("unsupported remote setup input options")
	}
	submitted, err := session.Submit(ctx, codex.Input{Input: turn.Input})
	if err != nil {
		return err
	}
	if err := check(submitted.Response, mainResponses[string(mainRequests[3].ID)]); err != nil {
		return err
	}
	for {
		received, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if received.Request != nil {
			return errors.New("unexpected permission in remote close setup")
		}
		if received.Event == nil {
			continue
		}
		if received.Event.DecodeError != nil {
			return received.Event.DecodeError
		}
		if received.Event.Method != "turn/completed" {
			continue
		}
		var result types.TurnCompletedNotification
		if err := json.Unmarshal(received.Event.Params, &result); err != nil {
			return err
		}
		if result.ThreadID != session.ThreadID() || result.Turn.ID != submitted.Response.Turn.ID || result.Turn.Status != types.TurnStatusCompleted {
			return errors.New("remote setup terminal is unsuccessful or has crossed identity")
		}
		break
	}
	if err := first.Close(); err != nil {
		return err
	}
	if _, err := session.Submit(ctx, codex.Input{Input: turn.Input}); !errors.Is(err, codex.ErrClosed) {
		return errors.New("closed remote Session did not reject input")
	}
	second, err := open(probeRequests[0])
	if err != nil {
		return err
	}
	defer second.Close()
	if err := check(second.ServerInfo(), probeResponses[string(probeRequests[0].ID)]); err != nil {
		return err
	}
	var read types.ThreadReadParams
	if err := json.Unmarshal(probeRequests[2].Params, &read); err != nil {
		return err
	}
	if read.ThreadID != session.ThreadID() {
		return errors.New("probe is not the same remote thread")
	}
	result, err := second.ReadThread(ctx, read)
	if err != nil {
		return err
	}
	if err := check(result, probeResponses[string(probeRequests[2].ID)]); err != nil {
		return err
	}
	if err := second.Close(); err != nil {
		return err
	}
	return nil
}
