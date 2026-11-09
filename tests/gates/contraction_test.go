// Package gates holds repository-level static and contraction gates.
package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "repository go.mod not found")
		dir = parent
	}
}

func gate(t *testing.T, root string) ([]byte, error) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "scripts", "check-removed-cli-surfaces.sh")
	cmd := exec.CommandContext(t.Context(), "bash", script, root)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	return cmd.CombinedOutput()
}

func TestRemovedTestifyCLISurfacesStayRemoved(t *testing.T) {
	output, err := gate(t, repoRoot(t))
	require.NoError(t, err, string(output))
}

func TestRemovedTestifyCLISurfaceGateCatchesRegressions(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		body string
		want string
	}{
		{
			name: "environment variable",
			file: "scripts/run.sh",
			body: "export AGENT_SDK_TESTIFY_BIN=/tmp/agent-sdk-testify\n",
			want: "external testify executable environment",
		},
		{
			name: "Python environment variable",
			file: "scripts/run.py",
			body: "import os\nos.environ[\"AGENT_SDK_TESTIFY_BIN\"] = \"/tmp/agent-sdk-testify\"\n",
			want: "external testify executable environment",
		},
		{
			name: "extensionless environment variable",
			file: "scripts/run",
			body: "#!/bin/sh\nAGENT_SDK_TESTIFY_BIN=/tmp/agent-sdk-testify exec \"$@\"\n",
			want: "external testify executable environment",
		},
		{
			name: "Dockerfile environment variable",
			file: "Dockerfile",
			body: "FROM scratch\nENV AGENT_SDK_TESTIFY_BIN=/tmp/agent-sdk-testify\n",
			want: "external testify executable environment",
		},
		{
			name: "harness environment variable",
			file: "harness/modules/agent-sdk-testify/adoption.md",
			body: "Use AGENT_SDK_TESTIFY_BIN to select the executable.\n",
			want: "external testify executable environment",
		},
		{
			name: "harness wrapper",
			file: "harness/modules/agent-sdk-testify/adoption.md",
			body: "Run scripts/with-agent-sdk-testify.sh before the test.\n",
			want: "external testify wrapper",
		},
		{
			name: "harness executable launch",
			file: "harness/modules/agent-sdk-testify/adoption.md",
			body: "exec agent-sdk-testify mock fixture\n",
			want: "external testify executable must not be launched",
		},
		{
			name: "documented result descriptor",
			file: "harness/modules/agent-sdk-testify/design.md",
			body: "Read the result descriptor before validating the run.\n",
			want: "retired testify process workflow terms",
		},
		{
			name: "documented progress descriptor",
			file: "harness/modules/agent-sdk-testify/library-mode.md",
			body: "The progress descriptor announces completion.\n",
			want: "retired testify process workflow terms",
		},
		{
			name: "documented process receipt",
			file: "README.md",
			body: "Validate the process receipt after exit.\n",
			want: "retired testify process workflow terms",
		},
		{
			name: "documented launcher",
			file: "harness/modules/agent-sdk-testify/design.md",
			body: "Start the launcher before the test.\n",
			want: "retired testify process workflow terms",
		},
		{
			name: "wrapper script",
			file: "scripts/run.sh",
			body: "bash scripts/with-agent-sdk-testify.sh true\n",
			want: "external testify wrapper",
		},
		{
			name: "wrapper at a new path",
			file: "scripts/wrapper-at-new-path.sh",
			body: "#!/bin/sh\nexec agent-sdk-testify mock \"$@\"\n",
			want: "external testify executable must not be launched",
		},
		{
			name: "launcher outside the bridge",
			file: "codex/tests/replay/launch.go",
			body: "package replay\n\nimport \"os/exec\"\n\nfunc launch() { _, _ = exec.Command(\"agent-sdk-testify\", \"mock\").Output() }\n",
			want: "external testify executable must not be launched",
		},
		{
			name: "recreated wrapper path",
			file: "scripts/with-agent-sdk-testify.sh",
			body: "#!/bin/sh\nexit 0\n",
			want: "removed testify wrapper path",
		},
		{
			name: "recreated version manifest",
			file: "scripts/agent-sdk-testify-version.json",
			body: "{}\n",
			want: "removed testify wrapper path",
		},
		{
			name: "completion callback",
			file: "internal/mocktest/library.go",
			body: "package mocktest\n\ntype Options struct{}\n\ntype Session struct{ options Options }\n\nfunc badCompletion(s *Session) { _ = s.options.Complete }\n",
			want: "bridge byte-copy",
		},
		{
			name: "completion callback field",
			file: "internal/mocktest/peer.go",
			body: "package mocktest\n\ntype Options struct {\n\tComplete func() error\n}\n",
			want: "bridge byte-copy",
		},
		{
			name: "bridge run",
			file: "internal/mocktest/library.go",
			body: "package mocktest\n\ntype Session struct{}\n\nfunc (s *Session) Run() {}\n",
			want: "bridge byte-copy",
		},
		{
			name: "schema comparison",
			file: "tests/simulation/simulate/session.go",
			body: "package main\n\nfunc badSchema() bool { return schemaVersion == 3 }\n",
			want: "schema-3 and schema-4 comparison",
		},
		{
			name: "schema switch",
			file: "tests/simulation/simulate/session.go",
			body: "package main\n\nfunc badSchema(v int) {\n\tswitch schemaVersion {\n\tcase 3, 4:\n\t\t_ = v\n\t}\n}\n",
			want: "schema-3 and schema-4 switch",
		},
		{
			name: "receipt parser",
			file: "tests/simulation/simulate/session.go",
			body: "package main\n\nvar badReceipt = \"executions/receipt.json\"\n",
			want: "result notice, progress descriptor, and process receipt",
		},
		{
			name: "launcher field",
			file: "tests/simulation/simulate/session.go",
			body: "package main\n\ntype badConnection struct {\n\tLauncher string `json:\"launcher\"`\n}\n",
			want: "process-oriented simulation connection contract",
		},
		{
			name: "executable launch import",
			file: "internal/mocktest/imports.go",
			body: "package mocktest\n\nimport _ \"os/exec\"\n",
			want: "must not launch an executable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeGateFixture(t, root, "go.mod", "module example.com/gatefixture\n\ngo 1.26\n")
			writeGateFixture(t, root, "internal/mocktest/doc.go", "package mocktest\n")
			writeGateFixture(t, root, tc.file, tc.body)

			output, err := gate(t, root)
			require.Error(t, err, "gate accepted regression %q:\n%s", tc.name, output)
			require.Contains(t, string(output), tc.want, "gate failed for an unrelated reason:\n%s", output)
		})
	}
}

func writeGateFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}
