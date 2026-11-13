package claudesim

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type existingChildSeedFile struct {
	Source          string
	SHA256          string
	SessionID       string
	ParentSessionID string
	ChildSessionID  string
}

// AcceptExistingChildren creates real child history on main, then starts a
// fresh native observer on probe and queries the copied, verified child storage.
func AcceptExistingChildren(
	ctx context.Context,
	mock *mocktest.Session,
	root string,
	policy claude.ChildDetailPolicy,
	deliveries io.Writer,
) (err error) {
	var plan struct {
		Provider              string
		ExistingChildrenSetup struct {
			SchemaVersion   int
			Provider        string
			SessionID       string
			AgentIDs        []string
			HistoryBindings []struct {
				Role, Source, SHA256       string
				SessionID, ParentSessionID string
				ChildSessionID             string
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	setup := plan.ExistingChildrenSetup
	if plan.Provider != "claude" || setup.SchemaVersion != 1 || setup.Provider != "claude" ||
		setup.SessionID == "" || len(setup.AgentIDs) == 0 {
		return errors.New("completed Claude existing-child setup required")
	}
	files := map[string]existingChildSeedFile{}
	for _, binding := range setup.HistoryBindings {
		if binding.Role != "seed" || binding.Source == "" || binding.SHA256 == "" {
			continue
		}
		seed := existingChildSeedFile{
			Source:          binding.Source,
			SHA256:          binding.SHA256,
			SessionID:       binding.SessionID,
			ParentSessionID: binding.ParentSessionID,
			ChildSessionID:  binding.ChildSessionID,
		}
		if seed.SessionID == setup.SessionID {
			if seed.ParentSessionID != "" || seed.ChildSessionID != "" {
				return errors.New("existing-child setup parent has a parent identity")
			}
		} else if !slices.Contains(setup.AgentIDs, seed.SessionID) ||
			seed.ParentSessionID != setup.SessionID || seed.ChildSessionID != seed.SessionID {
			return errors.New("existing-child setup binding belongs to another parent")
		}
		if previous, exists := files[binding.Source]; exists &&
			(previous.SHA256 != seed.SHA256 || previous.SessionID != seed.SessionID ||
				previous.ParentSessionID != seed.ParentSessionID ||
				previous.ChildSessionID != seed.ChildSessionID) {
			return errors.New("existing-child setup repeats a source with different identity")
		}
		files[binding.Source] = seed
	}
	if len(files) == 0 {
		return errors.New("verified existing-child seed required")
	}
	for _, agentID := range setup.AgentIDs {
		var historyFile, metaFile bool
		for _, file := range files {
			if file.ChildSessionID != agentID {
				continue
			}
			historyFile = historyFile || strings.HasSuffix(file.Source, ".jsonl")
			metaFile = metaFile || strings.HasSuffix(file.Source, ".meta.json")
		}
		if !historyFile || !metaFile {
			return errors.New("existing-child setup omits child history or meta")
		}
	}
	if err := verifySeed(root, files, setup.SessionID); err != nil {
		return err
	}

	b, err = os.ReadFile(filepath.Join(root, "remote-probe", "raw", "raw.jsonl"))
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	requests := 0
	var initialize types.InitializeOptions
	for scanner.Scan() {
		var frame struct {
			Direction string
			Wire      struct {
				Type    string
				Request json.RawMessage
			}
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		if frame.Direction != "c2s" {
			continue
		}
		var request struct{ Subtype string }
		if err := json.Unmarshal(frame.Wire.Request, &request); err != nil {
			return err
		}
		requests++
		if frame.Wire.Type != "control_request" || request.Subtype != "initialize" {
			return errors.New("existing-child observation contains new work")
		}
		if err := json.Unmarshal(frame.Wire.Request, &initialize); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if requests != 1 {
		return errors.New("existing-child observation requires exactly one initialization")
	}

	var setupStart mocktest.ExecutionStart
	if err := acceptFlow(ctx, mock, root, Host{}, policy, deliveries, func(start mocktest.ExecutionStart) {
		setupStart = start
	}); err != nil {
		return err
	}
	if setupStart.ExecutionID == "" || setupStart.ConnectionID != "main" || len(setupStart.NativeArgs) == 0 {
		return errors.New("existing-child setup start is incomplete")
	}
	if executions := mock.ExecutionIDs(); len(executions) != 1 || executions[0] != setupStart.ExecutionID {
		return errors.New("existing-child setup did not produce one execution")
	}

	s, run, inputClosed, err := openStreamSessionWithNativeArgs(ctx, mock, "probe", setupStart.ExecutionID, claude.SessionOptions{
		ResumeID:   setup.SessionID,
		Initialize: &initialize,
	}, setupStart.NativeArgs)
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
		if native.Type == "system" && native.Subtype == "hook_response" &&
			native.HookName == "SessionStart:resume" && native.UUID != "" && native.SessionID == setup.SessionID {
			break
		}
	}
	id, known := s.SessionID()
	if !known || id != setup.SessionID {
		return errors.New("resume did not establish the native observed parent")
	}
	if err := s.Close(); err != nil {
		return err
	}
	closed = true
	if err := finishStream(s, run, inputClosed); err != nil {
		return err
	}
	execution, err := executionHostFromRun(run)
	if err != nil {
		return err
	}
	page, err := claude.ListSubagents(ctx, setup.SessionID, claude.ListSubagentsOptions{
		Scope: claude.SessionQueryScope{ConfigDir: execution.Config, ProjectDir: execution.Workspace},
	})
	if err != nil {
		return err
	}
	if page.Coverage.Status != "complete" ||
		page.Coverage.SessionID != setup.SessionID ||
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
	want := append([]string(nil), setup.AgentIDs...)
	sort.Strings(ids)
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		return errors.New("go session omitted or invented pre-existing children")
	}
	return verifySeed(root, files, setup.SessionID)
}

func verifySeed(root string, files map[string]existingChildSeedFile, parent string) error {
	if _, err := os.Stat(filepath.Join(root, "runtime.json")); err == nil {
		return verifyDescribedSeed(root, files, parent)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for name, expected := range files {
		clean := filepath.Clean(name)
		if clean != name ||
			(!strings.HasPrefix(clean, "config/projects/") &&
				!strings.HasPrefix(clean, "history-seed/config/projects/") &&
				!strings.HasPrefix(clean, "history/seed/config/projects/")) ||
			filepath.IsAbs(clean) {
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

func verifyDescribedSeed(root string, files map[string]existingChildSeedFile, parent string) error {
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
		if actual, exists := available[path]; exists {
			if actual != expected.SHA256 {
				return errors.New("described frozen seed bytes changed")
			}
			continue
		}
		if expected.ParentSessionID != "" || expected.SessionID != parent {
			return errors.New("described frozen child seed is missing")
		}
		runtimePath := strings.Replace(path, "history/seed/", "history/runtime/", 1)
		if runtimePath == path || available[runtimePath] == "" {
			return errors.New("described frozen parent seed lacks its runtime binding")
		}
	}
	return nil
}
