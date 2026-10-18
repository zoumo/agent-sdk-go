#!/usr/bin/env bash
# Fetch the published module into an independent consumer; no replace/go.work.
# Run after pushing the requested commit; dependency downloads require network access.
set -euo pipefail
sdk_root=$(cd "$(dirname "$0")/.." && pwd)
version=${1:?usage: check-managed-consumer.sh COMMIT_OR_VERSION}
consumer_dir=$(mktemp -d)
trap 'rm -rf "$consumer_dir"' EXIT
cat > "$consumer_dir/go.mod" <<MOD
module managed-consumer

go 1.26.9

MOD
cat > "$consumer_dir/consumer_test.go" <<'GO'
package consumer

import (
    "context"
    claude "github.com/zoumo/agent-sdk-go/claude"
    "github.com/zoumo/agent-sdk-go/claude/types"
)

var _ func(context.Context) (claude.ReceivedEvent, error) = (*claude.Client)(nil).Receive

// Compile-only: go test runs no CLI or model calls.
func consumeConfig(ctx context.Context, config types.Options) error {
    opts := claude.OptionsFromTypes(config)
    opts.Subagents = claude.SubagentOptions{MaxNodes: 8}
    c, err := claude.Open(ctx, opts)
    if err != nil { return err }
    defer c.Close()
    r, err := c.Receive(ctx)
    if err != nil { return err }
    if native, ok := r.(types.Event); ok {
        var official *types.MessageOrigin = native.Origin
        var message *types.Message = native.Assistant
        _ = official
        _ = message
        _ = native.Raw
    }
    return nil
}
GO
python3 - "$sdk_root/docs/api-comparison.md" "$consumer_dir/comparison.go" <<'PYDOC'
import pathlib, sys
text = pathlib.Path(sys.argv[1]).read_text()
section = text.split("## Go: configuration,", 1)[1]
code = section.split("```go\n", 1)[1].split("```", 1)[0]
pathlib.Path(sys.argv[2]).write_text('package consumer\nimport ("context"; "fmt"; "encoding/json"; claude "github.com/zoumo/agent-sdk-go/claude")\n'+code)
PYDOC
(cd "$consumer_dir" && GOWORK=off go get "github.com/zoumo/agent-sdk-go@$version" && GOWORK=off go mod tidy && GOWORK=off go test ./...)
