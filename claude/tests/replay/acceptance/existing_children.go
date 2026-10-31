package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/zoumo/agent-sdk-go/claude"
)

// AcceptExistingChildren establishes a fresh native observer from a recorded
// resume hook before querying the copied, verified pre-existing child storage.
// It sends no input and never promotes stored messages into Session events.
func AcceptExistingChildren(ctx context.Context, launcher, root string) (err error) {
	var plan struct {
		Project, Config, HistoryProject string
		HistorySeed                     struct {
			SessionID     string
			SourceProject string
			AgentIDs      []string
			Files         map[string]struct{ Source, SHA256 string }
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	seed := plan.HistorySeed
	if seed.SessionID == "" || len(seed.AgentIDs) == 0 || len(seed.Files) == 0 {
		return errors.New("verified existing-child seed required")
	}
	if !filepath.IsAbs(plan.HistoryProject) || plan.HistoryProject != seed.SourceProject {
		return errors.New("history scope differs from the frozen source project")
	}
	verify := func() error {
		for name, expected := range seed.Files {
			clean := filepath.Clean(name)
			if clean != name || (!strings.HasPrefix(clean, "config/projects/") && !strings.HasPrefix(clean, "history-seed/config/projects/")) || filepath.IsAbs(clean) {
				return errors.New("seed copy path escaped its isolated project")
			}
			p := filepath.Join(root, name)
			info, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("seed copy is not a regular file")
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != expected.SHA256 {
				return errors.New("pre-existing child seed bytes changed")
			}
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	// The native capture must contain only initialization, not setup inputs.
	b, err = os.ReadFile(filepath.Join(root, "raw/raw.jsonl"))
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	requests := 0
	for scanner.Scan() {
		var frame struct {
			Direction string
			Wire      struct {
				Type    string
				Request struct{ Subtype string }
			}
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		if frame.Direction != "c2s" {
			continue
		}
		requests++
		if frame.Wire.Type != "control_request" || frame.Wire.Request.Subtype != "initialize" {
			return errors.New("existing-child recording contains new work")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if requests != 1 {
		return errors.New("existing-child recording requires exactly one initialization")
	}
	s, err := claude.Open(ctx, claude.SessionOptions{CLIPath: launcher, WorkDir: plan.HistoryProject,
		ResumeID: seed.SessionID, Env: []string{"CLAUDE_CONFIG_DIR=" + plan.Config}})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	for {
		got, err := s.Receive(ctx)
		if err != nil {
			return err
		}
		if got.Request != nil {
			return errors.New("unexpected request while discovering old children")
		}
		if got.Event == nil {
			continue
		}
		var native struct {
			Type, Subtype string
			SessionID     string `json:"session_id"`
			HookName      string `json:"hook_name"`
			UUID          string `json:"uuid"`
		}
		if err := json.Unmarshal(got.Raw, &native); err != nil {
			return err
		}
		if native.Type == "system" && (native.Subtype == "hook_started" || native.Subtype == "hook_response") && native.HookName == "SessionStart:resume" && native.UUID != "" && native.SessionID == seed.SessionID {
			break
		}
	}
	id, known := s.SessionID()
	if !known || id != seed.SessionID {
		return errors.New("resume did not establish the native observed parent")
	}
	page, err := s.ListSubagents(ctx, claude.SubagentQuery{})
	if err != nil {
		return err
	}
	if page.Coverage.Status != "complete" || page.Coverage.ScopeEvidence != "observed-main-uuid+exact-project-path" {
		return errors.New("existing child discovery coverage is not complete")
	}
	var ids []string
	for _, record := range page.Records {
		if record.Running != "unknown" {
			return errors.New("stored child was promoted to live state")
		}
		ids = append(ids, record.AgentID)
	}
	want := append([]string(nil), seed.AgentIDs...)
	sort.Strings(ids)
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		return errors.New("Go Session omitted or invented pre-existing children")
	}
	return verify()
}
