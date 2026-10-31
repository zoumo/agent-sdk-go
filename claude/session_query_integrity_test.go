package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Focused fault injection for mutations between initial read and final
// verification; ordinary selection tests use the public API in stories.
func TestQuerySourceIntegrity(t *testing.T) {
	for _, mode := range []string{"full", "sample"} {
		for _, change := range []string{"append", "rewrite", "replacement", "directory-replacement", "truncate"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(dir, "project"), 0700))
				path := filepath.Join(dir, "project", "source")
				data := []byte("original\n")
				require.NoError(t, os.WriteFile(path, data, 0600))
				root, e := os.OpenRoot(dir)
				require.NoError(t, e)
				defer root.Close()
				project, e := queryDirectory(root, "project")
				require.NoError(t, e)
				defer project.Close()
				source, e := openQuerySource(context.Background(), root, project, filepath.Join("project", "source"), nil)
				require.NoError(t, e)
				defer source.close()
				if mode == "full" {
					_, e = source.readAll(int64(len(data)))
				} else {
					_, e = source.sample(4, int64(len(data)))
				}
				require.NoError(t, e)
				initial := source.info
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
				e = source.verify()
				var qe *SessionQueryError
				require.ErrorAs(t, e, &qe)
				require.Equal(t, "changed", qe.Kind)
			})
		}
	}
}

// A sample must verify exactly the bytes it read, including its tail probe;
// it must not silently become a full-source read or promise whole-file coverage.
func TestQuerySourceSampleCoverage(t *testing.T) {
	for _, change := range []string{"head", "tail-probe", "tail", "gap"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "source")
			data := []byte("head-middle-tail\n")
			require.NoError(t, os.WriteFile(path, data, 0600))
			root, err := os.OpenRoot(dir)
			require.NoError(t, err)
			defer root.Close()
			sample, err := openQuerySource(context.Background(), root, root, "source", nil)
			require.NoError(t, err)
			defer sample.close()
			windows, err := sample.sample(4, 9)
			require.NoError(t, err)
			require.True(t, windows.sampled)
			require.Equal(t, data[:4], windows.head)
			require.Equal(t, data[len(data)-5:], windows.tail)
			full, err := openQuerySource(context.Background(), root, root, "source", nil)
			require.NoError(t, err)
			defer full.close()
			_, err = full.readAll(int64(len(data)))
			require.NoError(t, err)
			position := map[string]int{"head": 0, "tail-probe": len(data) - 5, "tail": len(data) - 1, "gap": 5}[change]
			data[position] = 'X'
			require.NoError(t, os.WriteFile(path, data, 0600))
			require.NoError(t, os.Chtimes(path, sample.info.ModTime(), sample.info.ModTime()))
			childQueryKind(t, full.verify(), "changed")
			if change == "gap" {
				require.NoError(t, sample.verify())
			} else {
				childQueryKind(t, sample.verify(), "changed")
			}
		})
	}
}
