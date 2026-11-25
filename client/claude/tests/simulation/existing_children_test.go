package claudesim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyDescribedSeedBindsProjectedMaterial(t *testing.T) {
	const (
		parent = "parent"
		child  = "child"
		path   = "history/seed/config/projects/project/parent/subagents/agent-child.jsonl"
		digest = "sha256:seed"
	)
	files := map[string]existingChildSeedFile{path: {
		SessionID:       child,
		ParentSessionID: parent,
		ChildSessionID:  child,
		SHA256:          digest,
	}}
	root := t.TempDir()
	writeAssertionInputs(t, root, path, digest)
	require.NoError(t, verifyDescribedSeed(root, files, parent))

	writeAssertionInputs(t, root, path, "sha256:changed")
	require.ErrorContains(t, verifyDescribedSeed(root, files, parent), "frozen seed bytes changed")

	writeAssertionInputs(t, root, "history/seed/other.jsonl", digest)
	require.ErrorContains(t, verifyDescribedSeed(root, files, parent), "child seed is missing")
}

func TestVerifyDescribedSeedAllowsPreSetupParentWithRuntimeBinding(t *testing.T) {
	const (
		parent  = "parent"
		seed    = "history/seed/config/projects/project/parent.jsonl"
		runtime = "history/runtime/config/projects/project/parent.jsonl"
	)
	files := map[string]existingChildSeedFile{seed: {
		SessionID: parent,
		SHA256:    "sha256:seed",
	}}
	root := t.TempDir()
	writeAssertionInputs(t, root, runtime, "sha256:runtime")
	require.NoError(t, verifyDescribedSeed(root, files, parent))

	writeAssertionInputs(t, root, "history/seed/other.jsonl", "sha256:runtime")
	require.ErrorContains(t, verifyDescribedSeed(root, files, parent), "lacks its runtime binding")
}

func writeAssertionInputs(t *testing.T, root, path, digest string) {
	t.Helper()
	input := `{"schemaVersion":1,"savedHistory":[{"path":` +
		quoteJSON(path) + `,"sha256":` + quoteJSON(digest) + `}]}`
	require.NoError(t, os.WriteFile(filepath.Join(root, "assertion-inputs.json"), []byte(input), 0o600))
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
