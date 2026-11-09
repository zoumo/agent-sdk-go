package claudesim

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// AcceptSavedHistory uses a real captured mock process for Session execution,
// then the public local-history API for the captured immutable saved files.
func AcceptSavedHistory(ctx context.Context, mock *mocktest.Session, root string, host Host, policy claude.ChildDetailPolicy, deliveries io.Writer) (err error) {
	var plan struct {
		Project, Config, DriverProfile string
		FixtureProject                 string `json:"_fixtureProject"`
	}
	b, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	host, err = resolveHost(root, plan.Project, plan.FixtureProject, plan.Config, host)
	if err != nil {
		return err
	}
	b, err = os.ReadFile(filepath.Join(root, "raw", "raw.jsonl"))
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var inputs []types.UserInput
	var initialize types.InitializeOptions
	var terminals [][]json.RawMessage
	for scanner.Scan() {
		var frame struct {
			Direction string
			Wire      json.RawMessage
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		var wire struct{ Type string }
		if err := json.Unmarshal(frame.Wire, &wire); err != nil {
			return err
		}
		if frame.Direction == "c2s" && wire.Type == "control_request" {
			var control struct{ Request json.RawMessage }
			if err := json.Unmarshal(frame.Wire, &control); err != nil {
				return err
			}
			if err := json.Unmarshal(control.Request, &initialize); err != nil {
				return err
			}
		}
		if frame.Direction == "c2s" && wire.Type == "user" {
			var input types.UserInput
			if err := json.Unmarshal(frame.Wire, &input); err != nil {
				return err
			}
			inputs = append(inputs, input)
			terminals = append(terminals, nil)
		}
		if frame.Direction == "s2c" && wire.Type == "result" {
			if len(terminals) == 0 {
				return errors.New("history result precedes input")
			}
			last := len(terminals) - 1
			terminals[last] = append(terminals[last], frame.Wire)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(inputs) == 0 {
		return errors.New("history setup has no inputs")
	}
	for _, results := range terminals {
		if len(results) == 0 {
			return errors.New("history input has no recorded result")
		}
	}
	s, run, inputClosed, err := openStreamSession(ctx, mock, claude.SessionOptions{
		WorkDir: host.Workspace,
		Env:     host.Env, Initialize: &initialize, ChildDetailPolicy: policy,
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, finishStream(s, run, inputClosed)) }()
	observed := json.NewEncoder(deliveries)
	for i := range inputs {
		if _, err := s.Submit(ctx, claude.Input{User: &inputs[i]}); err != nil {
			return err
		}
		// Background work may produce another result without a new submission.
		// Consume every recorded result before the next captured input.
		for terminal := 0; terminal < len(terminals[i]); {
			got, err := s.Receive(ctx)
			if err != nil {
				return err
			}
			if got.Request != nil {
				return errors.New("unexpected history permission request")
			}
			if err := observed.Encode(struct {
				Kind        claude.ReceivedKind      `json:"kind"`
				Raw         string                   `json:"raw,omitempty"`
				Source      claude.SubagentIdentity  `json:"source"`
				Parent      *claude.SubagentIdentity `json:"parent,omitempty"`
				Origin      string                   `json:"origin"`
				Observation *claude.Observation      `json:"observation,omitempty"`
			}{got.Kind, string(got.Raw), got.Source, got.Parent, got.Origin, got.Observation}); err != nil {
				return err
			}
			if got.Event == nil || got.Event.Result == nil {
				continue
			}
			if got.Event.Result.IsError || !sameJSON(got.Raw, terminals[i][terminal]) {
				return errors.New("go session terminal differs from native recording")
			}
			terminal++
		}
	}
	id, known := s.SessionID()
	if !known {
		return errors.New("go session did not establish native identity")
	}
	if err := s.Close(); err != nil {
		return err
	}
	if plan.DriverProfile == "continuation" {
		return nil
	}
	host, err = executionHostFromRun(run)
	if err != nil {
		return err
	}
	scope := claude.SessionQueryScope{ConfigDir: host.Config, ProjectDir: host.Workspace}
	listed, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope})
	if err != nil {
		return err
	}
	found := false
	for _, item := range listed.Sessions {
		if item.SessionID == id {
			found = true
		}
	}
	if !found {
		return errors.New("go history discovery omitted recorded session")
	}
	info, err := claude.GetSessionInfo(ctx, id, claude.SessionInfoOptions{Scope: scope})
	if err != nil {
		return err
	}
	if info.Info == nil || info.Info.SessionID != id {
		return errors.New("go metadata identity mismatch")
	}
	var report struct{ Receipts []map[string]json.RawMessage }
	b, err = os.ReadFile(filepath.Join(root, "report.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return err
	}
	checked := 0
	for _, receipt := range report.Receipts {
		if raw, ok := receipt["get_subagent_messages"]; ok {
			var saved struct {
				SessionID, AgentID string
				Messages           []claude.SessionMessage
			}
			if err := json.Unmarshal(raw, &saved); err != nil {
				return err
			}
			if saved.SessionID != id || saved.AgentID == "" {
				return errors.New("saved child expectation has wrong parent identity")
			}
			listed, err := claude.ListSubagents(ctx, id, claude.ListSubagentsOptions{Scope: scope})
			if err != nil {
				return err
			}
			found := false
			for _, record := range listed.Records {
				if record.AgentID == saved.AgentID && record.SessionID == id {
					found = true
				}
			}
			if listed.Coverage.Status != "complete" || !found {
				return errors.New("go saved child was not independently discovered")
			}
			page, err := claude.GetSubagentMessages(ctx, id, saved.AgentID, claude.SubagentMessagesOptions{Scope: scope, Limit: 100})
			if err != nil {
				return err
			}
			if page.Coverage.Status != "complete" || len(page.Messages) != len(saved.Messages) {
				return errors.New("go saved child content coverage differs")
			}
			for i, message := range page.Messages {
				want := saved.Messages[i]
				if message.AgentID != saved.AgentID || message.UUID != want.UUID || message.SessionID != want.SessionID || message.Type != want.Type || !sameJSON(message.Message, want.Message) {
					return errors.New("go saved child content or ordered identity differs")
				}
			}
			checked++
			continue
		}
		var expected []claude.SessionMessage
		limit, offset := 100, 0
		if raw, ok := receipt["get_session_messages"]; ok {
			if err := json.Unmarshal(raw, &expected); err != nil {
				return err
			}
		} else if raw, ok := receipt["history-slice"]; ok {
			var page struct {
				Limit, Offset int
				Messages      []claude.SessionMessage
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return err
			}
			expected, limit, offset = page.Messages, page.Limit, page.Offset
		} else {
			continue
		}
		actual, err := claude.GetSessionMessages(ctx, id, claude.SessionMessagesOptions{Scope: scope, Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		if actual.Coverage.Status != "complete" || len(actual.Messages) != len(expected) {
			return errors.New("go history coverage or slice size differs")
		}
		for i, message := range actual.Messages {
			want := expected[i]
			if message.UUID != want.UUID || message.SessionID != want.SessionID || message.Type != want.Type || !sameJSON(message.Message, want.Message) {
				return errors.New("go history content or ordered identity differs")
			}
		}
		checked++
	}
	if checked == 0 {
		return errors.New("recording has no saved history expectations")
	}
	return nil
}

func sameJSON(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&value)
		return value, err
	}
	x, e1 := decode(a)
	y, e2 := decode(b)
	return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
}
