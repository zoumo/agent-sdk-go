package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A deterministic barrier after initial read/inventory, before verification.
// This fault seam supplements public API acceptance, without sleep races.
func TestChildReadSetIntegrity(t *testing.T) {
	for _, change := range []string{"append", "same-size-rewrite", "file-replace", "directory-replace", "inventory-add", "inventory-delete", "sidecar-appear", "sidecar-disappear", "container-appear"} {
		t.Run(change, func(t *testing.T) {
			const parent = "550e8400-e29b-41d4-a716-446655440000"
			config := t.TempDir()
			project := filepath.Join(config, "projects", "synthetic")
			child := filepath.Join(project, parent, "subagents", "agent-x.jsonl")
			require.NoError(t, os.MkdirAll(filepath.Dir(child), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(project, parent+".jsonl"), nil, 0600))
			data := []byte("{\"type\":\"user\",\"uuid\":\"u\",\"message\":{\"content\":\"a\"}}\n")
			require.NoError(t, os.WriteFile(child, data, 0600))
			side := filepath.Join(filepath.Dir(child), "agent-x.meta.json")
			if change == "sidecar-disappear" {
				require.NoError(t, os.WriteFile(side, []byte(`{}`), 0600))
			}
			if change == "container-appear" {
				require.NoError(t, os.RemoveAll(filepath.Join(project, parent)))
			}
			scope := SessionQueryScope{ConfigDir: config}
			c := childCoverage(parent, "x", scope, SubagentHistoryLimits{}, 0, 0, 0)
			require.NoError(t, prepareChildQuery(context.Background(), parent, "x", &scope, &c))
			reader, e := newChildReader(context.Background(), scope, &c)
			require.NoError(t, e)
			defer reader.close()
			inventory, e := reader.inventory(parent)
			require.NoError(t, e)
			if change != "container-appear" {
				_, _, e = reader.child(inventory[0])
				require.NoError(t, e)
			}
			switch change {
			case "append":
				f, e := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0)
				require.NoError(t, e)
				_, e = f.WriteString("\n")
				require.NoError(t, e)
				require.NoError(t, f.Close())
			case "same-size-rewrite":
				st, e := os.Stat(child)
				require.NoError(t, e)
				data[len(data)-5] = 'b'
				require.NoError(t, os.WriteFile(child, data, 0600))
				require.NoError(t, os.Chtimes(child, st.ModTime(), st.ModTime()))
			case "file-replace":
				require.NoError(t, os.Rename(child, child+".old"))
				require.NoError(t, os.WriteFile(child, data, 0600))
			case "directory-replace":
				require.NoError(t, os.Rename(filepath.Dir(child), filepath.Dir(child)+"-old"))
				require.NoError(t, os.Mkdir(filepath.Dir(child), 0700))
				require.NoError(t, os.WriteFile(child, data, 0600))
			case "inventory-add":
				require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(child), "agent-y.jsonl"), nil, 0600))
			case "inventory-delete":
				require.NoError(t, os.Remove(child))
			case "sidecar-appear":
				require.NoError(t, os.WriteFile(side, []byte(`{}`), 0600))
			case "sidecar-disappear":
				require.NoError(t, os.Remove(side))
			case "container-appear":
				require.NoError(t, os.MkdirAll(filepath.Dir(child), 0700))
			}
			e = reader.verify()
			var qe *SessionQueryError
			require.ErrorAs(t, e, &qe)
			require.Equal(t, "changed", qe.Kind)
		})
	}
}
