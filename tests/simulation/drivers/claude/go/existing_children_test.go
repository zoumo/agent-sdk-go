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
		path   = "history-seed/config/projects/project/session.jsonl"
		digest = "sha256:seed"
	)
	files := map[string]existingChildSeedFile{path: {SHA256: digest}}
	root := t.TempDir()
	writeAssertionInputs(t, root, path, digest)
	require.NoError(t, verifyDescribedSeed(root, files))

	writeAssertionInputs(t, root, path, "sha256:changed")
	require.ErrorContains(t, verifyDescribedSeed(root, files), "frozen seed bytes changed")

	writeAssertionInputs(t, root, "history-seed/other.jsonl", digest)
	require.ErrorContains(t, verifyDescribedSeed(root, files), "frozen seed bytes changed")
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
