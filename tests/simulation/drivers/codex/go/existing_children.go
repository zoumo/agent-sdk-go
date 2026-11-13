package codexsim

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// AcceptExistingChildren creates real child history on main, then resumes the
// same execution on probe and enumerates descendants without new input.
func AcceptExistingChildren(ctx context.Context, root string, mock *mocktest.Session, rawPath string, policy codex.ChildDetailPolicy, deliveries io.Writer) (err error) {
	var plan struct {
		Model                 string
		ExistingChildrenSetup struct {
			SessionID string
			AgentIDs  []string
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	seed := plan.ExistingChildrenSetup
	if seed.SessionID == "" || len(seed.AgentIDs) == 0 {
		return errors.New("completed existing-child setup required")
	}
	if err := AcceptFlow(ctx, mock, rawPath, policy, deliveries); err != nil {
		return err
	}
	executions := mock.ExecutionIDs()
	if len(executions) != 1 {
		return errors.New("existing-child setup did not produce one execution")
	}

	f, err := os.Open(filepath.Join(root, "remote-probe", "raw", "raw.jsonl"))
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
	expectedEvents := map[string]int{}
	for _, frame := range frames {
		var r rpc
		if err := json.Unmarshal(frame.Wire, &r); err != nil {
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
			if len(r.Params) > 0 && string(r.Params) != "null" {
				if err := json.Unmarshal(r.Params, &identity); err != nil {
					return err
				}
			}
			if identity.ThreadID != seed.SessionID && identity.Thread.ID != seed.SessionID {
				continue
			}
			key, err := canonicalJSON(frame.Wire)
			if err != nil {
				return err
			}
			expectedEvents[key]++
		}
	}
	if len(requests) < 5 || requests[0].Method != "initialize" || requests[1].Method != "initialized" ||
		requests[2].Method != "thread/resume" || requests[len(requests)-1].Method != "thread/unsubscribe" {
		return errors.New("existing-child recording lacks resume/query/detach flow")
	}
	for _, request := range requests[3 : len(requests)-1] {
		if request.Method != "thread/list" {
			return errors.New("existing-child observation submitted or queried unrelated work")
		}
	}
	var init types.InitializeParams
	if err := json.Unmarshal(requests[0].Params, &init); err != nil {
		return err
	}
	c, run, inputClosed, err := openStreamSessionOnConnection(ctx, mock, "probe", executions[0], init)
	if err != nil {
		return err
	}
	var s *codex.Session
	defer func() { err = errors.Join(err, finishStream(run, c, s, inputClosed)) }()
	var resume types.ThreadResumeParams
	if err := json.Unmarshal(requests[2].Params, &resume); err != nil {
		return err
	}
	if resume.ThreadID != seed.SessionID || resume.Model.ValueOr("") != plan.Model {
		return errors.New("resume selection differs from completed setup")
	}
	s, err = codex.Open(ctx, codex.SessionOptions{Client: c, Resume: &resume, ChildDetailPolicy: policy})
	if err != nil {
		return err
	}
	native, ok := s.ResumeResponse()
	if !ok || native.Model != plan.Model || native.Thread.ID != seed.SessionID {
		return errors.New("go session resume response differs")
	}
	encoded, err := json.Marshal(native)
	if err != nil {
		return err
	}
	if !equal(canonical(encoded), canonical(responses[string(requests[2].ID)])) {
		return errors.New("go resume response loses native fields")
	}
	if _, ok := s.StartResponse(); ok {
		return errors.New("resume masquerades as a start")
	}
	page, err := s.ListSubagents(ctx, codex.SubagentQuery{
		Limit:       types.Some(uint32(10)),
		SourceKinds: types.Some([]types.ThreadSourceKind{types.ThreadSourceKindSubAgentThreadSpawn}),
	})
	if err != nil {
		return err
	}
	if !page.Coverage.EnumerationComplete || !page.Coverage.EndReached || len(page.Pages) != len(requests)-4 {
		return errors.New("go descendant coverage differs")
	}
	var agents []string
	for i, p := range page.Pages {
		r := requests[i+3]
		encoded, err = json.Marshal(p)
		if err != nil {
			return err
		}
		if !equal(canonical(encoded), canonical(responses[string(r.ID)])) {
			return errors.New("go child list loses native fields or order")
		}
		for _, child := range p.Data {
			if child.ParentThreadID.ValueOr("") != s.ThreadID() {
				return errors.New("discovered child has different parent")
			}
			agents = append(agents, child.ID)
		}
	}
	slices.Sort(agents)
	want := slices.Clone(seed.AgentIDs)
	slices.Sort(want)
	if len(agents) == 0 || !slices.Equal(agents, want) {
		return errors.New("go session did not discover the setup children")
	}
	if err := s.Close(); err != nil {
		return err
	}
	observed := json.NewEncoder(deliveries)
	deliveredEvents := map[string]int{}
	for {
		got, err := s.Receive(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if got.Event == nil || got.Request != nil || got.Observation != nil || got.Origin != "live" || got.Event.DecodeError != nil {
			return errors.New("history discovery generated live authority or synthetic events")
		}
		key, err := canonicalJSON(got.Raw)
		if err != nil {
			return err
		}
		if deliveredEvents[key] >= expectedEvents[key] {
			return errors.New("resume delivery differs from native observation")
		}
		deliveredEvents[key]++
		if err := observed.Encode(struct {
			Kind   codex.ReceivedKind `json:"kind"`
			Raw    string             `json:"raw"`
			Origin string             `json:"origin"`
		}{got.Kind, string(got.Raw), got.Origin}); err != nil {
			return err
		}
	}
	for key, expected := range expectedEvents {
		if deliveredEvents[key] != expected {
			return errors.New("resume lost native notifications")
		}
	}
	return nil
}

func canonicalJSON(raw json.RawMessage) (string, error) {
	encoded, err := json.Marshal(canonical(raw))
	return string(encoded), err
}
