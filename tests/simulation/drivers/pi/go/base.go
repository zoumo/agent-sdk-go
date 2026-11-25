package pisim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	"github.com/zoumo/agent-sdk-go/pi"
)

// Equal to the 64-by-64 RGB image produced by Testify's deterministic stimulus.
const bluePNG = "iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAeklEQVR4nO3PUQkAIBTAwBfFaEY3iiH8OITBAtxm7fN1wwUNaEEDWtCAFjSgBQ1oQQNa0IAWNKAFDWhBA1rQgBY0oAUNaEEDWtCAFjSgBQ1oQQNa0IAWNKAFDWhBA1rQgBY0oAUNaEEDWtCAFjSgBQ1oQQNa0IAWPHYBSp0BSxN310EAAAAASUVORK5CYII="

type basePlan struct {
	CaseID, Model, InitialModel string
	HistoryBindings             []struct{ SessionID, Project, NativeTarget, SHA256 string }
	Prompts                     []string
	Scenario                    struct {
		Parameters struct {
			Effort, TargetEffort, ContextValue string
			FreeText                           bool
		}
		Answers struct {
			Question, Choice string
			Options          []string
		}
	}
}

// AcceptBase drives Pi's public native APIs from a Testify case plan.
func AcceptBase(ctx context.Context, mock *mocktest.Session, root string, observations io.Writer) (err error) {
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	var plan basePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	code, ok := strings.CutPrefix(plan.CaseID, "SDK-CASE-")
	if !ok || !slices.Contains([]string{"002", "005", "006", "013", "014", "021", "022", "023", "024", "027", "030", "031", "033", "036", "037", "038"}, code) {
		return fmt.Errorf("pi: unsupported base case %q", plan.CaseID)
	}
	count := 1
	if slices.Contains([]string{"005", "022", "023", "027", "036", "037", "038"}, code) {
		count = 2
	}
	if len(plan.Prompts) != count {
		return fmt.Errorf("pi: %s requires %d inputs", plan.CaseID, count)
	}

	run, err := mock.NewRun(ctx, mocktest.RunOptions{NativeArgs: []string{"--mode", "rpc"}})
	if err != nil {
		if run != nil {
			err = errors.Join(err, run.Abort())
		}
		return err
	}
	defer func() { err = errors.Join(err, run.CloseSDK(), run.WaitError(ctx)) }()
	session, err := pi.Attach(ctx, run.Writer, run.Reader, "")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.CloseAndDiscard()) }()

	flow := baseFlow{ctx: ctx, session: session, plan: plan, code: code, output: json.NewEncoder(observations)}
	switch code {
	case "013", "014", "037", "038":
		return flow.runOfficialExtension()
	}
	state, err := session.State(ctx)
	if err != nil {
		return err
	}
	if err := flow.run(state); err != nil {
		return err
	}
	if code != "031" && code != "033" {
		return nil
	}
	if err := session.CloseAndDiscard(); err != nil {
		return err
	}
	if _, err := session.Prompt(ctx, "unexpected post-close input", nil, ""); err == nil {
		return errors.New("pi: accepted input after close")
	}
	if code == "033" {
		return flow.readSavedHistory(run, state.SessionFile)
	}
	return flow.output.Encode(map[string]bool{"attachedTransportClosed": true, "postCloseRejected": true})
}

type baseFlow struct {
	ctx     context.Context
	session *pi.Session
	plan    basePlan
	code    string
	output  *json.Encoder
}

func (f *baseFlow) run(initial pi.State) error {
	effort := f.plan.Scenario.Parameters
	modelChange := slices.Contains([]string{"022", "023", "024"}, f.code)
	want := f.plan.Model
	if modelChange {
		want = f.plan.InitialModel
	}
	if (modelChange || f.code == "021") && (want == "" || modelIdentity(initial.Model) != want) {
		return fmt.Errorf("pi: initial model differs from %q", want)
	}
	if f.code == "027" && (effort.Effort == "" || initial.ThinkingLevel != effort.Effort) {
		return fmt.Errorf("pi: initial effort differs from %q", effort.Effort)
	}
	if f.code == "022" {
		if err := f.changeModel(); err != nil {
			return err
		}
	}
	prompts := f.plan.Prompts
	if f.code == "005" {
		prompts = prompts[:1]
	}
	cursor := ""
	for i, prompt := range prompts {
		if i == 1 {
			switch f.code {
			case "023":
				if err := f.changeModel(); err != nil {
					return err
				}
			case "027":
				if err := f.changeThinking(effort.Effort, effort.TargetEffort); err != nil {
					return err
				}
			}
		}
		turn, err := f.runTurn(prompt)
		if err != nil {
			return err
		}
		if slices.Contains([]string{"021", "022", "023"}, f.code) {
			expected := f.plan.Model
			if f.code == "023" && i == 0 {
				expected = f.plan.InitialModel
			}
			if turn.Provider+"/"+turn.Model != expected {
				return fmt.Errorf("pi: assistant used a model other than %q", expected)
			}
		}
		if err := f.output.Encode(turn); err != nil {
			return err
		}
		if f.code == "036" {
			next, err := f.entries(cursor)
			if err != nil {
				return err
			}
			cursor = next
		}
	}
	if f.code == "036" {
		if err := f.checkSessionViews(cursor); err != nil {
			return err
		}
	}
	state, err := f.session.State(f.ctx)
	if err != nil {
		return err
	}
	if state.SessionID != f.session.ID() || state.IsStreaming {
		return errors.New("pi: session identity changed or remained active")
	}
	return f.output.Encode(map[string]string{"sessionId": state.SessionID, "type": "get_state"})
}

func modelIdentity(raw json.RawMessage) string {
	var model struct{ Provider, ID string }
	if json.Unmarshal(raw, &model) != nil {
		return ""
	}
	return model.Provider + "/" + model.ID
}

func (f *baseFlow) changeModel() error {
	provider, model, ok := strings.Cut(f.plan.Model, "/")
	if !ok || f.plan.Model == f.plan.InitialModel {
		return errors.New("pi: expected distinct explicit models")
	}
	if err := f.session.SetModel(f.ctx, provider, model); err != nil {
		var rejected *pi.CommandError
		if f.code == "024" && errors.As(err, &rejected) {
			return f.output.Encode(map[string]string{"requested": f.plan.Model, "outcome": "rejected"})
		}
		return err
	}
	state, err := f.session.State(f.ctx)
	if err != nil {
		return err
	}
	if modelIdentity(state.Model) != f.plan.Model {
		return fmt.Errorf("pi: set_model acknowledged without readback of %q", f.plan.Model)
	}
	return f.output.Encode(map[string]string{"requested": f.plan.Model, "observed": modelIdentity(state.Model)})
}

func (f *baseFlow) changeThinking(initial, target string) error {
	state, err := f.session.State(f.ctx)
	if err != nil {
		return err
	}
	if state.ThinkingLevel != initial || target == initial {
		return errors.New("pi: effort update requires two different observed levels")
	}
	available, err := f.session.AvailableThinkingLevels(f.ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(available, target) {
		return fmt.Errorf("pi: %q effort unavailable", target)
	}
	if err := f.session.SetThinkingLevel(f.ctx, target); err != nil {
		return err
	}
	state, err = f.session.State(f.ctx)
	if err != nil {
		return err
	}
	if state.ThinkingLevel != target {
		return fmt.Errorf("pi: target effort %q absent from state", target)
	}
	return f.output.Encode(map[string]string{"requested": target, "observed": state.ThinkingLevel})
}

func (f *baseFlow) entries(cursor string) (string, error) {
	page, err := f.session.Entries(f.ctx, cursor)
	if err != nil {
		return "", err
	}
	if len(page.Entries) == 0 || page.LeafID == nil || *page.LeafID != page.Entries[len(page.Entries)-1].ID {
		return "", errors.New("pi: append window has no usable leaf")
	}
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.ID == cursor || (cursor != "" && len(ids) == 0 && (entry.ParentID == nil || *entry.ParentID != cursor)) {
			return "", errors.New("pi: append window did not follow the previous cursor")
		}
		ids = append(ids, entry.ID)
	}
	return *page.LeafID, f.output.Encode(map[string]any{"since": cursor, "entries": ids})
}

func (f *baseFlow) checkSessionViews(cursor string) error {
	page, err := f.session.Entries(f.ctx, cursor)
	if err != nil {
		return err
	}
	tree, err := f.session.Tree(f.ctx)
	if err != nil {
		return err
	}
	messages, err := f.session.Messages(f.ctx)
	if err != nil {
		return err
	}
	stats, err := f.session.Stats(f.ctx)
	if err != nil {
		return err
	}
	if len(page.Entries) != 0 || page.LeafID == nil || *page.LeafID != cursor ||
		tree.LeafID == nil || *tree.LeafID != cursor || len(tree.Roots) == 0 ||
		len(messages) < 4 || stats.SessionID != f.session.ID() || stats.UserMessages < 2 {
		return errors.New("pi: current session views did not agree with the native cursor")
	}
	return nil
}
