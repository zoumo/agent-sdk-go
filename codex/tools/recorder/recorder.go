// Package recorder contains opt-in recording preflight and durable accounting.
// Offline scenario drivers live in codex/tools/replay. No live authority is
// inferred from an opt-in, historical marker or successful synthetic test.
package recorder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
)

var ErrFinancialGate = errors.New("Codex recording blocked: no verified independent dollar/token/provider-request/retry enforcement; #11 requires separate budget and isolation approval")

// LaunchProfile contains reviewed nonsecret values only. It never discovers
// user configuration, hooks, authentication, previous captures or conversation
// storage. A profile assertion is not proof of isolation or financial control.
type LaunchProfile struct {
	Version         int                      `json:"version"`
	Scene           string                   `json:"scene"`
	CLI             string                   `json:"cli"`
	CLIVersion      string                   `json:"cliVersion"`
	CLISHA256       string                   `json:"cliSHA256"`
	SDKRevision     string                   `json:"sdkRevision"`
	IsolatedHome    string                   `json:"isolatedHome"`
	Project         string                   `json:"project"`
	RawDir          string                   `json:"rawDir"`
	Ledger          string                   `json:"ledger"`
	IsolationReview string                   `json:"isolationReview"`
	Model           string                   `json:"model"`
	Effort          string                   `json:"effort"`
	SavedRootID     string                   `json:"savedRootId"`
	NativePolicy    []recording.ExactRequest `json:"nativePolicy"`
}

// Run deliberately stops before spawning even a proxy. The old S1/S2 marker
// launcher cannot supply the aggregate or financial guarantees required by #6.
// The scenario/peer/accounting support is usable offline; enabling a future
// financial adapter and approving a run are separate work, never a boolean here.
func Run() error {
	if len(os.Args) != 3 || (os.Args[1] != "--record" && os.Args[1] != "--private-proxy") {
		return errors.New("usage: record --record reviewed-nonsecret-profile.json; direct --proxy disabled")
	}
	if err := recording.Guard("record"); err != nil {
		return err
	}
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("unproven recorder build")
	}
	p, err := ValidateProfile(bytes.NewReader(b), info)
	if err != nil {
		return err
	}
	if err = financialGate(); err != nil {
		return err
	}
	if os.Args[1] == "--private-proxy" {
		return proxyProfile(p)
	}
	_, err = CaptureWithPeer(context.Background(), p, nativeOpener(os.Args[2]))
	return err
}

// ValidateProfile performs only offline validation; it grants no spawn authority.
func ValidateProfile(r io.Reader, info *debug.BuildInfo) (p LaunchProfile, err error) {
	b, err := io.ReadAll(io.LimitReader(r, 16385))
	if err != nil {
		return p, err
	}
	if len(b) > 16384 {
		return p, errors.New("profile byte limit")
	}
	if err = decodeLedger(b, &p); err != nil {
		return p, err
	}
	if p.Version != 1 || p.CLIVersion != "0.162.1" || p.IsolationReview == "" {
		return p, errors.New("unreviewed profile or unsupported pinned CLI version")
	}
	if !validScene(p.Scene) {
		return p, errors.New("unsupported scene; children disabled")
	}
	if err = validateSceneConfig(p); err != nil {
		return p, err
	}
	revision, clean := "", false
	if info != nil {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
			if s.Key == "vcs.modified" {
				clean = s.Value == "false"
			}
		}
	}
	if len(revision) != 40 || p.SDKRevision != revision || !clean {
		return p, errors.New("dirty or unproven recorder revision")
	}
	for _, path := range []string{p.CLI, p.IsolatedHome, p.Project, p.RawDir, p.Ledger} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return p, errors.New("absolute isolated paths required")
		}
		for part := path; part != "/"; part = filepath.Dir(part) {
			st, e := os.Lstat(part)
			if e != nil {
				return p, e
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return p, errors.New("profile symlink refused")
			}
		}
	}
	for _, path := range []string{p.RawDir, p.Project, p.IsolatedHome, p.Ledger} {
		for part := filepath.Dir(path); part != "/"; part = filepath.Dir(part) {
			if marker, e := os.Lstat(filepath.Join(part, ".git")); e == nil && (!marker.IsDir() || hasGitHead(filepath.Join(part, ".git", "HEAD"))) {
				return p, errors.New("capture/profile paths must be outside Git worktrees")
			}
		}
	}
	for _, dir := range []string{p.IsolatedHome, p.Project, p.RawDir} {
		st, e := os.Stat(dir)
		if e != nil {
			return p, e
		}
		if !st.IsDir() || st.Mode().Perm() != 0700 {
			return p, errors.New("private isolated directories required")
		}
	}
	paths := []string{p.IsolatedHome, p.Project, p.RawDir, p.Ledger}
	for i, a := range paths {
		for _, b := range paths[i+1:] {
			rel, _ := filepath.Rel(a, b)
			back, _ := filepath.Rel(b, a)
			if rel == "." || (rel != ".." && !bytes.HasPrefix([]byte(rel), []byte("../"))) || (back != ".." && !bytes.HasPrefix([]byte(back), []byte("../"))) {
				return p, errors.New("profile paths overlap")
			}
		}
	}
	cli, e := os.Stat(p.CLI)
	if e != nil {
		return p, e
	}
	if !cli.Mode().IsRegular() || cli.Mode().Perm()&0111 == 0 {
		return p, errors.New("CLI must be regular executable")
	}
	f, e := os.Open(p.CLI)
	if e != nil {
		return p, e
	}
	defer f.Close()
	hash := sha256.New()
	if _, e = io.Copy(hash, f); e != nil {
		return p, e
	}
	if hex.EncodeToString(hash.Sum(nil)) != p.CLISHA256 {
		return p, errors.New("CLI digest mismatch")
	}
	return p, nil
}

func financialGate() error { return ErrFinancialGate }

func validScene(scene string) bool {
	switch scene {
	case "s1", "s2", "r1", "r2", "r3", "r4":
		return true
	}
	return false
}

func validateSceneConfig(p LaunchProfile) error {
	switch p.Scene {
	case "r1":
		if p.Model == "" || p.Effort != "high" {
			return errors.New("r1 requires reviewed model and fixed high native effort")
		}
	case "r2":
		if p.Model == "" {
			return errors.New("r2 requires reviewed model and finite exact native policy")
		}
		if err := recording.ValidateR2Policy(p.NativePolicy); err != nil {
			return err
		}
	case "r3":
		if p.Model == "" {
			return errors.New("r3 requires reviewed model")
		}
	case "r4":
		if p.SavedRootID == "" {
			return errors.New("r4 requires fresh synthetic saved root identity")
		}
	}
	return nil
}

func hasGitHead(path string) bool { _, err := os.Lstat(path); return err == nil }
