package claudesim

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
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type existingChildSeedFile struct {
	Source string
	SHA256 string
}

// AcceptExistingChildren establishes a fresh native observer from a recorded
// resume hook before querying the copied, verified pre-existing child storage.
// It sends no input and never promotes stored messages into Session events.
func AcceptExistingChildren(ctx context.Context, mock *mocktest.Session, root string) (err error) {
	var plan struct {
		Project, Config, HistoryProject string
		FixtureProject                  string `json:"_fixtureProject"`
		HistorySeed                     struct {
			SessionID     string
			SourceProject string
			AgentIDs      []string
			Files         map[string]existingChildSeedFile
		}
		HistoryBindings []struct {
			Role, Source, SHA256 string
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	plan.FixtureProject, plan.Config, _, err = runtimePaths(root, plan.FixtureProject, plan.Config)
	if err != nil {
		return err
	}
	seed := plan.HistorySeed
	if len(seed.Files) == 0 && len(plan.HistoryBindings) > 0 {
		seed.Files = map[string]existingChildSeedFile{}
		for _, binding := range plan.HistoryBindings {
			if binding.Role == "seed" {
				seed.Files[binding.Source] = existingChildSeedFile{Source: binding.Source, SHA256: binding.SHA256}
			}
		}
	}
	if seed.SessionID == "" || len(seed.AgentIDs) == 0 || len(seed.Files) == 0 {
		return errors.New("verified existing-child seed required")
	}
	if !filepath.IsAbs(plan.HistoryProject) || plan.HistoryProject != seed.SourceProject {
		return errors.New("history scope differs from the frozen source project")
	}
	verify := func() error {
		if _, e := os.Stat(filepath.Join(root, "runtime.json")); e == nil {
			return verifyDescribedSeed(root, seed.Files)
		}
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
	b, err = os.ReadFile(filepath.Join(root, "raw", "raw.jsonl"))
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
	s, run, inputClosed, err := openStreamSession(ctx, mock, claude.SessionOptions{
		WorkDir:  executionProject(plan.HistoryProject, plan.FixtureProject),
		ResumeID: seed.SessionID, Env: []string{"CLAUDE_CONFIG_DIR=" + plan.Config},
	})
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, finishStream(s, run, inputClosed))
		}
	}()
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
		if native.Type == "system" && native.Subtype == "hook_response" && native.HookName == "SessionStart:resume" && native.UUID != "" && native.SessionID == seed.SessionID {
			break
		}
	}
	id, known := s.SessionID()
	if !known || id != seed.SessionID {
		return errors.New("resume did not establish the native observed parent")
	}
	if err := s.Close(); err != nil {
		return err
	}
	closed = true
	err = finishStream(s, run, inputClosed)
	if err != nil {
		return err
	}
	execution, err := executionHostFromRun(run)
	if err != nil {
		return err
	}
	page, err := claude.ListSubagents(ctx, seed.SessionID, claude.ListSubagentsOptions{
		Scope: claude.SessionQueryScope{ConfigDir: execution.Config, ProjectDir: execution.Workspace},
	})
	if err != nil {
		return err
	}
	if page.Coverage.Status != "complete" ||
		page.Coverage.SessionID != seed.SessionID ||
		page.Coverage.ProjectDir != execution.Workspace {
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
		return errors.New("go session omitted or invented pre-existing children")
	}
	return verify()
}

func verifyDescribedSeed(root string, files map[string]existingChildSeedFile) error {
	// Described history IDs are opaque. Compare the seed's material paths and
	// hashes with the SDK projection; execution evidence binds the live copies.
	var described struct {
		SchemaVersion int
		SavedHistory  []struct {
			Path   string
			SHA256 string
		} `json:"savedHistory"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "assertion-inputs.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &described); err != nil {
		return err
	}
	if described.SchemaVersion != 1 {
		return errors.New("unsupported assertion history schema")
	}
	available := make(map[string]string, len(described.SavedHistory))
	for _, item := range described.SavedHistory {
		if item.Path == "" || item.SHA256 == "" {
			return errors.New("assertion history identity is incomplete")
		}
		if previous, exists := available[item.Path]; exists && previous != item.SHA256 {
			return errors.New("assertion history repeats a path with different bytes")
		}
		available[item.Path] = item.SHA256
	}
	for path, expected := range files {
		if available[path] != expected.SHA256 {
			return errors.New("described frozen seed bytes changed")
		}
	}
	return nil
}
