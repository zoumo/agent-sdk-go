#!/usr/bin/env bash
# Retrieve a pushed commit into a fresh external consumer; never run a real CLI.
set -euo pipefail
revision=${1:?usage: check-consumers.sh FULL_PUSHED_COMMIT_SHA}
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo 'expected a full 40-character pushed commit SHA' >&2
  exit 2
fi
consumer_dir=$(mktemp -d /tmp/agent-sdk-consumer.XXXXXXXX)
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

go 1.26.9
MOD
GOPROXY=direct go mod download -json "github.com/zoumo/agent-sdk-go@$revision" > "$consumer_dir/module.json"
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

    codex "github.com/zoumo/agent-sdk-go/codex"
    codextypes "github.com/zoumo/agent-sdk-go/codex/types"
    claude "github.com/zoumo/agent-sdk-go/claude"
    claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
    codexrecording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
    claudetranscript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

var _ func(context.Context, codex.SessionOptions) (*codex.Session, error) = codex.Open
var _ func(*claude.Session, context.Context) (claude.Received, error) = (*claude.Session).Receive

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
            assert.Equal(t, "github.com/zoumo/agent-sdk-go/codex/types", reflect.TypeOf(params).PkgPath())
        }},
        {"claude package identity", func(t *testing.T) {
            assert.Equal(t, "github.com/zoumo/agent-sdk-go/claude/types", reflect.TypeOf(config).PkgPath())
        }},
    } {
        t.Run(tc.name, tc.check)
    }
}

// Tooling smoke check: the downloaded module must embed its relocated fixtures.
// This is independent of the consumer's cwd and never opens a CLI or client.
func TestEmbeddedRecordingFixtures(t *testing.T) {
    for _, story := range []string{"s1", "s2"} {
        frames, err := codexrecording.Load(story)
        require.NoError(t, err)
        require.NotEmpty(t, frames)
    }
    prompt, err := claudetranscript.Prompt()
    require.NoError(t, err)
    require.NotEmpty(t, prompt)
    require.NotEmpty(t, claudetranscript.Wire)
}

GO
python3 - "$module_dir" <<'PY'
import pathlib, shutil, sys
source = pathlib.Path(sys.argv[1])
# Copy the fetched module's pure API/configuration consumer separately from SDK demos.
target = pathlib.Path('consumers/codex-public')
target.mkdir(parents=True)
shutil.copyfile(source / 'tests/consumers/codex-public/main.go', target / 'main.go')
target = pathlib.Path('consumers/mock')
target.mkdir(parents=True)
shutil.copyfile(source / 'tests/consumers/mock/mock_test.go', target / 'mock_test.go')
# Compile consumer copies of the fetched module's public examples, excluding
# example tests and all examples that import implementation-only packages.
for name, relative in (
    ('codex-api', 'codex/examples/apiusage/usage.go'),
    ('codex-observe', 'codex/examples/observe-turn/main.go'),
    ('codex-resume', 'codex/examples/resume-thread/main.go'),
    ('claude-subagents', 'claude/examples/subagents-offline/main.go'),
    ('claude-metadata', 'claude/examples/session-metadata-offline/main.go'),
):
    target = pathlib.Path('examples') / name
    target.mkdir(parents=True)
    shutil.copyfile(source / relative, target / pathlib.Path(relative).name)
    testdata = (source / relative).parent / 'testdata'
    if testdata.is_dir():
        shutil.copytree(testdata, target / 'testdata')
for native in ('codex', 'claude'):
    code = (source / native / 'README.md').read_text().split('```go\n', 1)[1].split('```', 1)[0]
    target = pathlib.Path('docs') / native
    target.mkdir(parents=True)
    (target / 'main.go').write_text(code)
comparison = (source / 'claude/docs/api-comparison.md').read_text()
section = comparison.split('## Go: configuration,', 1)[1]
code = section.split('```go\n', 1)[1].split('```', 1)[0]
target = pathlib.Path('docs/claude-configuration')
target.mkdir(parents=True)
(target / 'configuration.go').write_text('package configuration\nimport ("context"; "fmt"; "encoding/json"; claude "github.com/zoumo/agent-sdk-go/claude")\n' + code)
PY
go mod tidy
go mod verify
go mod edit -json | python3 -c 'import json, sys; assert not json.load(sys.stdin).get("Replace"), "consumer unexpectedly contains a replace directive"'
test ! -e go.work
mkdir -p "$consumer_dir/bin"
GOPROXY=off GOSUMDB=off go build -o "$consumer_dir/bin/mock-codex" github.com/zoumo/agent-sdk-go/codex/cmd/mock
GOPROXY=off GOSUMDB=off go build -o "$consumer_dir/bin/mock-claude" github.com/zoumo/agent-sdk-go/claude/cmd/mock
export SDK_MOCK_CODEX_BIN="$consumer_dir/bin/mock-codex"
export SDK_MOCK_CLAUDE_BIN="$consumer_dir/bin/mock-claude"
# Downloads are complete: consumer/example compilation and mock checks now run offline.
GOPROXY=off GOSUMDB=off go test -count=1 ./...
GOPROXY=off GOSUMDB=off go run ./consumers/codex-public
GOPROXY=off GOSUMDB=off go run ./examples/claude-metadata
printf 'External consumer verified pushed commit %s\n' "$revision"
