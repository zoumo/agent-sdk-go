#!/usr/bin/env bash
# Retrieve a pushed commit into a fresh external consumer; never run a real CLI.
set -euo pipefail
revision=${1:?usage: check-consumers.sh FULL_PUSHED_COMMIT_SHA}
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo 'expected a full 40-character pushed commit SHA' >&2
  exit 2
fi
consumer_dir=$(python3 -c 'import pathlib, tempfile; print(pathlib.Path(tempfile.mkdtemp(prefix="agent-sdk-consumer-")).resolve())')
cleanup() {
  chmod -R u+w "$consumer_dir"
  rm -rf "$consumer_dir"
}
trap cleanup EXIT
export GOWORK=off GOTOOLCHAIN=local GOENV=off GOFLAGS=
export GOPATH="$consumer_dir/go" GOMODCACHE="$consumer_dir/module-cache"
export GOPROXY="${SDK_CONSUMER_GOPROXY:-https://proxy.golang.org,direct}"
export GOSUMDB="${SDK_CONSUMER_GOSUMDB:-sum.golang.org}"
mkdir "$consumer_dir/consumer"
cd "$consumer_dir/consumer"
cat > go.mod <<'MOD'
module consumer.invalid/agent-sdk-check

go 1.26
MOD
go mod download -json "github.com/zoumo/agent-sdk-go@$revision" > "$consumer_dir/module.json"
module_dir=$(python3 - "$consumer_dir/module.json" "$revision" "$GOMODCACHE" <<'PY'
import json, pathlib, sys
module = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert module['Path'] == 'github.com/zoumo/agent-sdk-go', module
assert module.get('Origin', {}).get('Hash') == sys.argv[2], module
directory = pathlib.Path(module['Dir']).resolve()
assert directory.is_relative_to(pathlib.Path(sys.argv[3]).resolve()), directory
print(directory)
PY
)
go get "github.com/zoumo/agent-sdk-go@$revision"
# The consumer owns its test dependency; resolve it normally in its fresh cache.
go get github.com/stretchr/testify@v1.12.1
cat > consumer_test.go <<'GO'
package consumer

import (
    "context"
    "encoding/json"
    "reflect"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    codex "github.com/zoumo/agent-sdk-go/client/codex"
    codextypes "github.com/zoumo/agent-sdk-go/client/codex/types"
    claude "github.com/zoumo/agent-sdk-go/client/claude"
    claudetypes "github.com/zoumo/agent-sdk-go/client/claude/types"
    "github.com/zoumo/agent-sdk-go/client/pi"
)

var _ func(context.Context, codex.SessionOptions) (*codex.Session, error) = codex.Open
var _ func(*claude.Session, context.Context) (claude.Received, error) = (*claude.Session).Receive
var _ func(context.Context, pi.Options) (*pi.Session, error) = pi.Open

// This executes only pure value/configuration operations; no client is opened.
func TestNativeConfiguration(t *testing.T) {
    params := codextypes.ThreadStartParams{Ephemeral: codextypes.Some(true)}
    config := claudetypes.Options{CLIPath: "/explicit/unused/claude", ResumeID: "native-session"}
    for _, tc := range []struct {
        name string
        check func(*testing.T)
    }{
        {"codex configuration roundtrip", func(t *testing.T) {
            data, err := json.Marshal(params)
            require.NoError(t, err)
            var decoded codextypes.ThreadStartParams
            require.NoError(t, json.Unmarshal(data, &decoded))
            assert.Equal(t, params, decoded, "configuration roundtrip: %s", data)
        }},
        {"claude native configuration", func(t *testing.T) {
            options := claude.OptionsFromTypes(config)
            assert.Equal(t, config.CLIPath, options.CLIPath, "configuration adaptation changed native CLI path")
            assert.Equal(t, config.ResumeID, options.ResumeID, "configuration adaptation changed native resume ID")
        }},
        {"codex package identity", func(t *testing.T) {
            assert.Equal(t, "github.com/zoumo/agent-sdk-go/client/codex/types", reflect.TypeOf(params).PkgPath())
        }},
        {"claude package identity", func(t *testing.T) {
            assert.Equal(t, "github.com/zoumo/agent-sdk-go/client/claude/types", reflect.TypeOf(config).PkgPath())
        }},
    } {
        t.Run(tc.name, tc.check)
    }
}

GO
cat > unified_test.go <<'GO'
package consumer

import (
    "github.com/zoumo/agent-sdk-go/agent"
    agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
    agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
    agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

var (
    _ agent.Core = (*agentclaude.Core)(nil)
    _ agent.Core = (*agentcodex.Core)(nil)
    _ agent.Core = (*agentpi.Core)(nil)
)
GO
cat > native_and_unified_test.go <<'GO'
package consumer

import (
    "github.com/zoumo/agent-sdk-go/agent"
    agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
    agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
    agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
    "github.com/zoumo/agent-sdk-go/client/claude"
    "github.com/zoumo/agent-sdk-go/client/codex"
    "github.com/zoumo/agent-sdk-go/client/pi"
)

func adaptersAndNativeOptions() ([]agent.Core, claude.SessionOptions, codex.SessionOptions, pi.Options) {
    codexStdio := codex.StdioOptions{Executable: "/unused/codex"}
    claudeOptions := claude.SessionOptions{CLIPath: "/unused/claude"}
    piOptions := pi.Options{Command: "/unused/pi"}
    return []agent.Core{
        agentcodex.New(agentcodex.Config{Stdio: codexStdio}),
        agentclaude.New(agentclaude.Config{CLIPath: claudeOptions.CLIPath}),
        agentpi.New(agentpi.Config{CLIPath: piOptions.Command}),
    }, claudeOptions, codex.SessionOptions{Stdio: &codexStdio}, piOptions
}
GO
python3 - "$module_dir" <<'PY'
import pathlib, shutil, sys
source = pathlib.Path(sys.argv[1])
# Compile consumer copies of the fetched module's public examples, excluding
# example tests and all examples that import implementation-only packages.
for name, relative in (
    ('codex-api', 'client/codex/examples/apiusage/usage.go'),
    ('codex-observe', 'client/codex/examples/observe-turn/main.go'),
    ('codex-resume', 'client/codex/examples/resume-thread/main.go'),
    ('codex-history', 'client/codex/examples/history-query-offline/main.go'),
    ('claude-subagents', 'client/claude/examples/subagents-offline/main.go'),
    ('claude-metadata', 'client/claude/examples/session-metadata-offline/main.go'),
    ('claude-messages', 'client/claude/examples/session-messages-offline/main.go'),
):
    target = pathlib.Path('examples') / name
    target.mkdir(parents=True)
    shutil.copyfile(source / relative, target / pathlib.Path(relative).name)
    testdata = (source / relative).parent / 'testdata'
    if testdata.is_dir():
        shutil.copytree(testdata, target / 'testdata')
for native in ('codex', 'claude'):
    code = (source / 'client' / native / 'README.md').read_text().split('```go\n', 1)[1].split('```', 1)[0]
    target = pathlib.Path('docs') / native
    target.mkdir(parents=True)
    (target / 'main.go').write_text(code)
comparison = (source / 'client/claude/README.md').read_text()
section = comparison.split('## Go: configuration,', 1)[1]
code = section.split('```go\n', 1)[1].split('```', 1)[0]
target = pathlib.Path('docs/claude-configuration')
target.mkdir(parents=True)
(target / 'configuration.go').write_text('package configuration\nimport ("context"; "fmt"; "encoding/json"; claude "github.com/zoumo/agent-sdk-go/client/claude")\n' + code)
PY
go mod tidy
go mod verify
go mod edit -json | python3 -c 'import json, sys; assert not json.load(sys.stdin).get("Replace"), "consumer unexpectedly contains a replace directive"'
test ! -e go.work
# Downloads are complete: packaging smoke tests and public examples now run offline.
GOPROXY=off GOSUMDB=off go test -count=1 ./...
GOPROXY=off GOSUMDB=off go run ./examples/codex-history
GOPROXY=off GOSUMDB=off go run ./examples/claude-metadata
GOPROXY=off GOSUMDB=off go run ./examples/claude-messages
printf 'External consumer verified pushed commit %s\n' "$revision"
