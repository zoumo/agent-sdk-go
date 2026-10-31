package codexsim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// AcceptExistingChildren restores a public Session and enumerates stored
// descendants without submitting input or projecting saved turns into Receive.
func AcceptExistingChildren(ctx context.Context, root, executable string, args []string, rawPath string, policy codex.ChildDetailPolicy, deliveries io.Writer) (err error) {
	var plan struct {
		Model       string
		HistorySeed struct {
			SessionID string
			AgentIDs  []string
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &plan); err != nil {
		return err
	}
	f, err := os.Open(rawPath)
	if err != nil {
		return err
	}
	frames, err := recording.Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	type rpc struct {
		ID     json.RawMessage
		Method string
		Params json.RawMessage
		Result json.RawMessage
	}
	var requests []rpc
	responses := map[string]json.RawMessage{}
	var expectedEvents []json.RawMessage
	for _, frame := range frames {
		var r rpc
		if err = json.Unmarshal(frame.Wire, &r); err != nil {
			return err
		}
		if frame.Direction == "c2s" {
			requests = append(requests, r)
		} else if len(r.ID) > 0 {
			responses[string(r.ID)] = r.Result
		} else if r.Method != "" {
			var identity struct {
				ThreadID string
				Thread   struct{ ID string }
			}
			if err = json.Unmarshal(r.Params, &identity); err != nil {
				return err
			}
			if identity.ThreadID == plan.HistorySeed.SessionID || identity.Thread.ID == plan.HistorySeed.SessionID {
				expectedEvents = append(expectedEvents, frame.Wire)
			}
		}
	}
	if len(requests) < 5 || requests[0].Method != "initialize" || requests[1].Method != "initialized" || requests[2].Method != "thread/resume" || requests[len(requests)-1].Method != "thread/unsubscribe" {
		return errors.New("existing-child recording lacks resume/query/detach flow")
	}
	var init types.InitializeParams
	if err = json.Unmarshal(requests[0].Params, &init); err != nil {
		return err
	}
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: args, Env: []string{}, Initialize: init})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	var resume types.ThreadResumeParams
	if err = json.Unmarshal(requests[2].Params, &resume); err != nil {
		return err
	}
	if resume.ThreadID != plan.HistorySeed.SessionID || resume.Model.ValueOr("") != plan.Model {
		return errors.New("resume selection differs from frozen seed")
	}
	s, err := codex.Open(ctx, codex.SessionOptions{Client: c, Resume: &resume, ChildDetailPolicy: policy})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	native, ok := s.ResumeResponse()
	if !ok || native.Model != plan.Model || native.Thread.ID != plan.HistorySeed.SessionID {
		return errors.New("Go Session resume response differs")
	}
	encoded, err := json.Marshal(native)
	if err != nil {
		return err
	}
	if !equal(canonical(encoded), canonical(responses[string(requests[2].ID)])) {
		return errors.New("Go resume response loses native fields")
	}
	if _, ok := s.StartResponse(); ok {
		return errors.New("resume masquerades as a start")
	}
	page, err := s.ListSubagents(ctx, codex.SubagentQuery{Limit: types.Some(uint32(10)), SourceKinds: types.Some([]types.ThreadSourceKind{types.ThreadSourceKindSubAgentThreadSpawn})})
	if err != nil {
		return err
	}
	if !page.Coverage.EnumerationComplete || !page.Coverage.EndReached || len(page.Pages) != len(requests)-4 {
		return errors.New("Go descendant coverage differs")
	}
	var agents []string
	for i, p := range page.Pages {
		r := requests[i+3]
		if r.Method != "thread/list" {
			return errors.New("unexpected operation during observation")
		}
		encoded, err = json.Marshal(p)
		if err != nil {
			return err
		}
		if !equal(canonical(encoded), canonical(responses[string(r.ID)])) {
			return errors.New("Go child list loses native fields or order")
		}
		for _, child := range p.Data {
			if child.ParentThreadID.ValueOr("") != s.ThreadID() {
				return errors.New("discovered child has different parent")
			}
			agents = append(agents, child.ID)
		}
	}
	slices.Sort(agents)
	slices.Sort(plan.HistorySeed.AgentIDs)
	if len(agents) == 0 || !slices.Equal(agents, plan.HistorySeed.AgentIDs) {
		return errors.New("Go Session did not discover the frozen children")
	}
	if err = s.Close(); err != nil {
		return err
	}
	observed := json.NewEncoder(deliveries)
	count := 0
	for {
		got, e := s.Receive(ctx)
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if got.Event == nil || got.Request != nil || got.Observation != nil || got.Origin != "live" || got.Event.History != nil || got.Event.DecodeError != nil {
			return errors.New("history discovery generated live authority or synthetic events")
		}
		if count >= len(expectedEvents) || !equal(canonical(got.Raw), canonical(expectedEvents[count])) {
			return fmt.Errorf("resume delivery %d differs from native live order", count)
		}
		if err = observed.Encode(struct {
			Raw    string `json:"raw"`
			Origin string `json:"origin"`
		}{string(got.Raw), got.Origin}); err != nil {
			return err
		}
		count++
	}
	if count != len(expectedEvents) {
		return errors.New("resume lost live notifications")
	}
	return nil
}
