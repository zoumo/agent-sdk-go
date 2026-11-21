package mcp_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/mcp"
)

func TestResolveMCPFilesMergesAndReloads(t *testing.T) {
	files := fstest.MapFS{
		"lower.env":    {Data: []byte("API_MODE='read-only'\nEMPTY='default'\n")},
		"higher.env":   {Data: []byte("API_MODE='write'\n")},
		"defaults.env": {Data: []byte("# common headers\nX-Version='1'\nx-previous='old'\n")},
		"headers.env":  {Data: []byte("x-version='2'\nX-PREVIOUS='new'\n")},
		"binding.env":  {Data: []byte("AUTH='Bearer first'\nRAW='${untouched} # and \\literal'\nMULTI='first\nsecond'\n")},
	}
	servers := []mcp.Server{
		{
			Name: "local", Transport: mcp.TransportStdio, Command: "fixture-mcp",
			EnvFrom: []mcp.ImportSource{mcpImport("lower.env"), mcpImport("higher.env")},
			Env: []mcp.NamedValue{
				mcp.Literal("EMPTY", ""),
				mcpFileKey("UNEXPANDED", "binding.env", "RAW"),
				mcpFileKey("MULTILINE", "binding.env", "MULTI"),
			},
		},
		{
			Name: "remote", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
			HeadersFrom: []mcp.ImportSource{mcpImport("defaults.env"), mcpImport("headers.env")},
			Headers: []mcp.NamedValue{
				mcpFileKey("Authorization", "binding.env", "AUTH"),
				mcp.Literal("X-Version", "explicit"),
			},
		},
	}
	original := mcp.CloneServers(servers)
	counted := &countingFiles{files: files, opens: make(map[string]int)}
	resolved, err := mcp.ResolveServers(servers, counted)
	require.NoError(t, err)
	assert.Equal(t, original, servers)
	assert.Equal(t, 1, counted.opens["binding.env"])
	assert.Nil(t, resolved[0].EnvFrom)
	assert.Nil(t, resolved[1].HeadersFrom)
	env, err := mcp.LiteralValues(resolved[0].Env)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"API_MODE": "write", "EMPTY": "", "UNEXPANDED": `${untouched} # and \literal`,
		"MULTILINE": "first\nsecond",
	}, env)
	headers, err := mcp.LiteralValues(resolved[1].Headers)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"Authorization": "Bearer first", "X-Version": "explicit", "X-PREVIOUS": "new"}, headers)

	files["binding.env"].Data = []byte("AUTH='Bearer second'\nRAW=''\nMULTI=''\n")
	resumed, err := mcp.ResolveServers(servers, files)
	require.NoError(t, err)
	newHeaders, err := mcp.LiteralValues(resumed[1].Headers)
	require.NoError(t, err)
	assert.Equal(t, "Bearer second", newHeaders["Authorization"])
	assert.Equal(t, "Bearer first", headers["Authorization"])
}

type countingFiles struct {
	files fstest.MapFS
	opens map[string]int
}

func (f *countingFiles) Stat(path string) (fs.FileInfo, error) {
	return f.files.Stat(path)
}

func (f *countingFiles) Open(path string) (fs.File, error) {
	f.opens[path]++
	return f.files.Open(path)
}

func TestResolveMCPFilesFromLocalAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.env")
	require.NoError(t, os.WriteFile(path, []byte("TOKEN='fixture'\n"), 0o600))
	servers, err := mcp.ResolveServers([]mcp.Server{
		{Name: "local", Transport: mcp.TransportStdio, Command: "fixture-mcp", EnvFrom: []mcp.ImportSource{mcpImport(path)}},
		{Name: "remote", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp", Headers: []mcp.NamedValue{mcpFileKey("Authorization", path, "TOKEN")}},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, []mcp.NamedValue{mcp.Literal("TOKEN", "fixture")}, servers[0].Env)
	assert.Equal(t, []mcp.NamedValue{mcp.Literal("Authorization", "fixture")}, servers[1].Headers)
}

func TestResolveMCPFilesRejectsInvalidFilesAndEntries(t *testing.T) {
	const secret = "MCP-SENSITIVE-SENTINEL"
	for _, tc := range []struct {
		name    string
		file    string
		entries []mcp.NamedValue
		imports []mcp.ImportSource
		files   fs.FS
		want    string
	}{
		{name: "filesystem required", entries: []mcp.NamedValue{mcpFileKey("Authorization", "values.env", "AUTH")}, want: "file_system_required"},
		{name: "missing file", entries: []mcp.NamedValue{mcpFileKey("Authorization", "missing.env", "AUTH")}, files: fstest.MapFS{}, want: "file_invalid"},
		{name: "missing key", file: "OTHER='" + secret + "'\n", entries: []mcp.NamedValue{mcpFileKey("Authorization", "values.env", "AUTH")}, want: "file_key_missing"},
		{name: "missing value", entries: []mcp.NamedValue{{Name: "Authorization"}}, want: "value_choice"},
		{name: "literal and source", entries: []mcp.NamedValue{{Name: "Authorization", Value: new(secret), ValueFrom: &mcp.ValueSource{}}}, want: "value_choice"},
		{name: "unknown source", entries: []mcp.NamedValue{{Name: "Authorization", ValueFrom: &mcp.ValueSource{}}}, want: "file_key_ref"},
		{name: "unknown import", imports: []mcp.ImportSource{{}}, want: "file_ref"},
		{name: "duplicate explicit header", entries: []mcp.NamedValue{mcp.Literal("Authorization", secret), mcp.Literal("authorization", secret)}, want: "duplicate"},
		{name: "duplicate imported header", file: "Authorization='" + secret + "'\nauthorization='other'\n", imports: []mcp.ImportSource{mcpImport("values.env")}, want: "duplicate"},
		{name: "duplicate file key", file: "AUTH='" + secret + "'\nAUTH='other'\n", imports: []mcp.ImportSource{mcpImport("values.env")}, want: "file_invalid"},
		{name: "unquoted value", file: "AUTH=" + secret + "\n", imports: []mcp.ImportSource{mcpImport("values.env")}, want: "file_invalid"},
		{name: "oversized file", file: "AUTH='" + strings.Repeat(secret, 5000) + "'\n", imports: []mcp.ImportSource{mcpImport("values.env")}, want: "file_invalid"},
		{name: "newline in header", file: "AUTH='" + secret + "\nother'\n", entries: []mcp.NamedValue{mcpFileKey("Authorization", "values.env", "AUTH")}, want: "value_invalid"},
		{name: "bad header name", entries: []mcp.NamedValue{mcp.Literal("X-invalid:header", secret)}, want: "key_invalid"},
		{name: "parent path", entries: []mcp.NamedValue{mcpFileKey("Authorization", "../values.env", "AUTH")}, want: "file_key_ref"},
		{name: "absolute path with supplied filesystem", imports: []mcp.ImportSource{mcpImport(filepath.Join(t.TempDir(), "values.env"))}, files: fstest.MapFS{}, want: "file_ref"},
		{name: "nonregular path", imports: []mcp.ImportSource{mcpImport("nested")}, files: fstest.MapFS{"nested/values.env": {}}, want: "file_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			if tc.file != "" {
				files = fstest.MapFS{"values.env": {Data: []byte(tc.file)}}
			}
			servers, err := mcp.ResolveServers([]mcp.Server{{
				Name: "remote", Transport: mcp.TransportHTTP, URL: "https://example.invalid/mcp",
				Headers: tc.entries, HeadersFrom: tc.imports,
			}}, files)
			require.Nil(t, servers)
			var validation *mcp.ValidationError
			require.ErrorAs(t, err, &validation)
			assert.Contains(t, validation.Reason, tc.want)
			assert.NotContains(t, err.Error(), secret)
		})
	}
}

func TestResolveMCPFilesWithRootRejectsSymlinkEscape(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "private.env")
	require.NoError(t, os.WriteFile(outside, []byte("TOKEN='secret'\n"), 0o600))
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "values.env")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	_, err = mcp.ResolveServers([]mcp.Server{{
		Name: "local", Transport: mcp.TransportStdio, Command: "fixture-mcp",
		EnvFrom: []mcp.ImportSource{mcpImport("values.env")},
	}}, root.FS())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), outside)
}

func mcpImport(path string) mcp.ImportSource {
	return mcp.ImportSource{FileRef: &mcp.FileRef{Path: path}}
}

func mcpFileKey(name, path, key string) mcp.NamedValue {
	return mcp.NamedValue{Name: name, ValueFrom: &mcp.ValueSource{FileKeyRef: &mcp.FileKeyRef{Path: path, Key: key}}}
}
