package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type command struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type document struct {
	ID, Format, SHA256 string
	Original           []byte `json:"originalBase64"`
}
type connectionInput struct {
	ID, Format, SHA256 string
	Version            int
	Original           []byte `json:"originalBase64"`
	Frames             []json.RawMessage
}
type assertionInputs struct {
	SchemaVersion                                                   int
	Provider, CaseID, Attempt, ManifestSHA256, SourceManifestSHA256 string
	SourcePlan                                                      document
	SourceReport                                                    *document
	SavedHistory                                                    []document
	Connections                                                     []connectionInput
}
type scene struct {
	SchemaVersion                                                                                            int
	ToolVersion, ManifestSHA256, Provider, CaseID, Requirement, Adoption, Verification, MissingReason, Route string
	Available                                                                                                bool
	Plan                                                                                                     json.RawMessage
	AssertionInputs                                                                                          *assertionInputs
	original                                                                                                 json.RawMessage
}
type connection struct {
	command
	ID, Launcher, PlanSHA256, ManifestSHA256, RecordingSHA256, Receipt, ProcessReceipt string
}
type descriptor struct {
	SchemaVersion                                                                                                                                            int
	RunID, Provider, CaseID, ToolVersion, ToolSHA256, ResourceRoot, Workspace, ProtocolCwd, Contract, ContractSHA256, Materialization, MaterializationSHA256 string
	Tool                                                                                                                                                     command
	Env                                                                                                                                                      map[string]string
	Connections                                                                                                                                              []connection
	Finish                                                                                                                                                   command
	Builtin                                                                                                                                                  *struct{ ManifestSHA256 string }
}
type toolIdentity struct{ Command, Version, SHA256 string }

func discoverTool(ctx context.Context) (toolIdentity, error) {
	path := os.Getenv("AGENT_SDK_TESTIFY_BIN")
	if path == "" {
		return toolIdentity{}, errors.New("no published testify pin; set absolute AGENT_SDK_TESTIFY_BIN for local integration")
	}
	if !filepath.IsAbs(path) {
		return toolIdentity{}, errors.New("AGENT_SDK_TESTIFY_BIN must be absolute")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return toolIdentity{}, err
	}
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return toolIdentity{}, err
	}
	return toolIdentity{path, strings.TrimSpace(string(out)), digest(b)}, nil
}
func toolJSON(ctx context.Context, path string, args []string, v any) error {
	cmd := exec.CommandContext(ctx, path, append([]string{"mock"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if len(b) > 0 {
		if e := json.Unmarshal(b, v); e != nil {
			return errors.Join(err, e, fmt.Errorf("tool diagnostics: %s", stderr.String()))
		}
	}
	if err != nil {
		return fmt.Errorf("mock %v: %w: %s", args, err, stderr.String())
	}
	if len(b) == 0 {
		return errors.New("mock returned no descriptor")
	}
	return nil
}
func validateScene(s scene) error {
	if s.SchemaVersion != 1 {
		return errors.New("unsupported describe schemaVersion")
	}
	if !s.Available {
		return fmt.Errorf("material unavailable: %s", s.MissingReason)
	}
	a := s.AssertionInputs
	if a == nil || a.SchemaVersion != 1 {
		return errors.New("unsupported assertionInputs schemaVersion")
	}
	if a.Provider != s.Provider || a.CaseID != s.CaseID || a.ManifestSHA256 != s.ManifestSHA256 {
		return errors.New("describe assertion identity mismatch")
	}
	docs := append([]document{a.SourcePlan}, a.SavedHistory...)
	if a.SourceReport != nil {
		docs = append(docs, *a.SourceReport)
	}
	for _, d := range docs {
		if (d.Format != "vendor-native-json" && d.Format != "vendor-native-jsonl") || digest(d.Original) != d.SHA256 {
			return fmt.Errorf("assertion document hash mismatch: %s", d.ID)
		}
	}
	seen := map[string]bool{}
	for _, c := range a.Connections {
		if seen[c.ID] || (c.ID != "main" && c.ID != "probe") || c.Version != 1 || c.Format != "agent-mock-jsonl" || digest(c.Original) != c.SHA256 {
			return errors.New("assertion connection identity/format/hash mismatch")
		}
		seen[c.ID] = true
	}
	if !seen["main"] {
		return errors.New("main assertion connection missing")
	}
	return nil
}
func validateRun(d descriptor, s scene, tool toolIdentity) error {
	if d.SchemaVersion != 1 {
		return errors.New("unsupported run schemaVersion")
	}
	if d.RunID == "" || !strings.HasSuffix(tool.Version, d.ToolVersion) || d.Provider != s.Provider || d.CaseID != s.CaseID || d.Tool.Command != tool.Command || d.ToolSHA256 != tool.SHA256 {
		return errors.New("prepared identity mismatch")
	}
	materialization, e := os.ReadFile(d.Materialization)
	if e != nil {
		return e
	}
	if digest(materialization) != d.MaterializationSHA256 {
		return errors.New("materialization identity mismatch")
	}
	a := s.AssertionInputs
	b, e := os.ReadFile(d.Contract)
	if e != nil {
		return e
	}
	if digest(b) != a.SourcePlan.SHA256 || digest(b) != d.ContractSHA256 {
		return errors.New("immutable contract differs from describe")
	}
	if s.ManifestSHA256 != "" && (d.Builtin == nil || d.Builtin.ManifestSHA256 != s.ManifestSHA256) {
		return errors.New("builtin manifest differs")
	}
	if len(d.Connections) != len(a.Connections) {
		return errors.New("selected connection count differs")
	}
	seen := map[string]bool{}
	for _, c := range d.Connections {
		match := false
		for _, input := range a.Connections {
			if c.ID == input.ID && c.RecordingSHA256 == input.SHA256 {
				match = true
			}
		}
		if !match || seen[c.ID] || c.PlanSHA256 != a.SourcePlan.SHA256 || c.ManifestSHA256 != a.SourceManifestSHA256 {
			return errors.New("selected connection hashes/identity differ")
		}
		seen[c.ID] = true
	}
	return nil
}
func finishRun(tool toolIdentity, path string, d descriptor) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type finish struct {
		SchemaVersion        int
		RunID, Status, Error string
		CleanupComplete      bool
	}
	var attempts []finish
	defer func() { err = errors.Join(err, writeJSON(filepath.Join(path, "sdk-finish-attempts.json"), attempts)) }()
	for {
		var f finish
		e := toolJSON(ctx, tool.Command, []string{"--finish", filepath.Join(path, "run.json")}, &f)
		attempts = append(attempts, f)
		if f.SchemaVersion != 1 || f.RunID != d.RunID || f.Status != "passed" || !f.CleanupComplete {
			e = errors.Join(e, fmt.Errorf("finish failed for %s: %s", d.RunID, f.Error))
		}
		err = errors.Join(err, e)
		if e == nil || f.CleanupComplete || !strings.Contains(f.Error, "mock run busy") {
			return err
		}
		// A killed descendant may take a moment to release its inherited lock. Busy
		// is retained as failure, while retrying cleanup prevents a leaked resource.
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *scene) UnmarshalJSON(b []byte) error {
	type plain scene
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = scene(p)
	s.original = append([]byte(nil), b...)
	return nil
}
func (s scene) MarshalJSON() ([]byte, error) {
	if s.original != nil {
		return s.original, nil
	}
	type plain scene
	return json.Marshal(plain(s))
}
