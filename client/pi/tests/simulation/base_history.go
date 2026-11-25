package pisim

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zoumo/agent-sdk-go/client/pi"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
)

func (f *baseFlow) readSavedHistory(run *mocktest.Run, sessionFile string) error {
	id := f.session.ID()
	if !filepath.IsAbs(sessionFile) || !filepath.IsAbs(run.Start.Config) {
		return errors.New("pi: missing absolute session or mock config path")
	}
	target := filepath.ToSlash(filepath.Join("config", "sessions", filepath.Base(filepath.Dir(sessionFile)), filepath.Base(sessionFile)))
	for _, binding := range f.plan.HistoryBindings {
		if binding.SessionID != id || binding.NativeTarget != target {
			continue
		}
		if binding.SHA256 == "" || run.Start.HistoryFiles[target] != binding.SHA256 {
			return errors.New("pi: history materialization differs from its bound source")
		}
		dir := filepath.Join(filepath.Dir(run.Start.Config), filepath.FromSlash(filepath.Dir(target)))
		return f.checkSavedEntries(id, pi.HistoryScope{Cwd: binding.Project, SessionDir: dir})
	}
	return errors.New("pi: no bound saved history for the recording's session")
}

func (f *baseFlow) checkSavedEntries(id string, scope pi.HistoryScope) error {
	inventory, err := pi.ListSessions(f.ctx, scope)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(inventory, func(info pi.SessionInfo) bool { return info.ID == id }) {
		return errors.New("pi: saved history discovery omitted the session")
	}
	info, err := pi.GetSessionInfo(f.ctx, scope, id)
	if err != nil {
		return err
	}
	history, err := pi.ReadSessionHistory(f.ctx, scope, id, pi.HistoryWindow{})
	if err != nil {
		return err
	}
	if info.Version != 3 || info.Timestamp == "" || history.Info.ID != id ||
		history.TrailingPartial || history.HasMore || !hasSavedConversation(history.Entries, f.plan.Prompts[0]) {
		return fmt.Errorf("pi: incomplete metadata or main session conversation for %s", id)
	}
	return f.output.Encode(map[string]any{"sessionId": id, "discovered": len(inventory), "savedEntries": len(history.Entries)})
}

func hasSavedConversation(entries []pi.HistoryEntry, prompt string) bool {
	user, assistant := false, false
	for _, entry := range entries {
		if entry.Type != "message" {
			continue
		}
		var wire baseEvent
		if json.Unmarshal(entry.Raw, &wire) != nil {
			return false
		}
		switch wire.Message.Role {
		case "user":
			user = user || messageText(wire.Message.Content) == prompt
		case "assistant":
			assistant = assistant || strings.TrimSpace(messageText(wire.Message.Content)) == "SYNTHETIC_SAVED"
		}
	}
	return user && assistant
}
