package claude

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Focused fault injection for mutations between initial read and final
// verification; ordinary selection tests use the public API in stories.
func TestQuerySourceIntegrity(t *testing.T) {
	for _, change := range []string{"append", "rewrite", "replacement", "directory-replacement", "truncate"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "project"), 0700))
			path := filepath.Join(dir, "project", "source")
			data := []byte("original\n")
			require.NoError(t, os.WriteFile(path, data, 0600))
			root, e := os.OpenRoot(dir)
			require.NoError(t, e)
			defer root.Close()
			f, e := os.Open(path)
			require.NoError(t, e)
			defer f.Close()
			initial, e := f.Stat()
			require.NoError(t, e)
			switch change {
			case "append":
				w, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				require.NoError(t, e)
				_, e = w.WriteString("new\n")
				require.NoError(t, e)
				require.NoError(t, w.Close())
			case "rewrite":
				require.NoError(t, os.WriteFile(path, []byte("rewritten"), 0600))
				require.NoError(t, os.Chtimes(path, initial.ModTime(), initial.ModTime()))
			case "replacement":
				require.NoError(t, os.Rename(path, path+".old"))
				require.NoError(t, os.WriteFile(path, data, 0600))
			case "directory-replacement":
				require.NoError(t, os.Rename(filepath.Dir(path), filepath.Dir(path)+".old"))
				require.NoError(t, os.Mkdir(filepath.Dir(path), 0700))
				require.NoError(t, os.WriteFile(path, data, 0600))
			case "truncate":
				require.NoError(t, os.Truncate(path, 1))
			}
			e = verifyQuerySource(context.Background(), f, root, filepath.Join("project", "source"), initial, sha256.Sum256(data))
			var qe *SessionQueryError
			require.ErrorAs(t, e, &qe)
			require.Equal(t, "changed", qe.Kind)
		})
	}
}
