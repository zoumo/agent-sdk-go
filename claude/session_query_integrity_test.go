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
				require.NoError(t, os.Mkdir(filepath.Join(dir, "project"), 0o700))
				path := filepath.Join(dir, "project", "source")
				data := []byte("original\n")
				require.NoError(t, os.WriteFile(path, data, 0o600))
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
					w, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					require.NoError(t, err)
					_, err = w.WriteString("new\n")
					require.NoError(t, err)
					require.NoError(t, w.Close())
				case "rewrite":
					require.NoError(t, os.WriteFile(path, []byte("rewritten"), 0o600))
					require.NoError(t, os.Chtimes(path, initial.ModTime(), initial.ModTime()))
				case "replacement":
					require.NoError(t, os.Rename(path, path+".old"))
					require.NoError(t, os.WriteFile(path, data, 0o600))
				case "directory-replacement":
					require.NoError(t, os.Rename(filepath.Dir(path), filepath.Dir(path)+".old"))
					require.NoError(t, os.Mkdir(filepath.Dir(path), 0o700))
					require.NoError(t, os.WriteFile(path, data, 0o600))
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

func TestQuerySourceInventoryIdentityChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, path string, expected os.FileInfo)
	}{
		{
			name: "in-place-size-change",
			change: func(t *testing.T, path string, _ os.FileInfo) {
				require.NoError(t, os.WriteFile(path, []byte("changed after inventory\n"), 0o600))
			},
		},
		{
			name: "same-size-same-mtime-replacement",
			change: func(t *testing.T, path string, expected os.FileInfo) {
				// Keep the old inode alive so the replacement has a distinct identity.
				require.NoError(t, os.Rename(path, path+".old"))
				old, err := os.Lstat(path + ".old")
				require.NoError(t, err)
				require.True(t, os.SameFile(expected, old))
				require.NoError(t, os.WriteFile(path, []byte("replaced\n"), 0o600))
				require.NoError(t, os.Chtimes(path, expected.ModTime(), expected.ModTime()))
				replaced, err := os.Lstat(path)
				require.NoError(t, err)
				require.Equal(t, expected.Size(), replaced.Size())
				require.True(t, expected.ModTime().Equal(replaced.ModTime()))
				require.False(t, os.SameFile(expected, replaced))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "source")
			require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o600))
			root, err := os.OpenRoot(dir)
			require.NoError(t, err)
			defer root.Close()
			expected, err := os.Lstat(path)
			require.NoError(t, err)
			tc.change(t, path, expected)

			source, err := openQuerySource(context.Background(), root, root, "source", expected)
			childQueryKind(t, err, "changed")
			require.Nil(t, source.file)
		})
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
			require.NoError(t, os.WriteFile(path, data, 0o600))
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
			require.NoError(t, os.WriteFile(path, data, 0o600))
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
