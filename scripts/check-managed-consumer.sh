#!/usr/bin/env bash
# Compile an independent public consumer without using any model or installed CLI.
set -euo pipefail
sdk_root=$(cd "$(dirname "$0")/.." && pwd)
consumer_dir=$(mktemp -d)
trap 'rm -rf "$consumer_dir"' EXIT
cat > "$consumer_dir/go.mod" <<MOD
module managed-consumer

go 1.26.9

require github.com/zoumo/claude-agent-sdk-go v0.0.0
replace github.com/zoumo/claude-agent-sdk-go => "$sdk_root"
MOD
cat > "$consumer_dir/consumer_test.go" <<'GO'
package consumer

import (
    "context"
    claude "github.com/zoumo/claude-agent-sdk-go"
    "github.com/zoumo/claude-agent-sdk-go/types"
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
pathlib.Path(sys.argv[2]).write_text('package consumer\nimport ("context"; "fmt"; "encoding/json"; claude "github.com/zoumo/claude-agent-sdk-go")\n'+code)
PYDOC
(cd "$consumer_dir" && go test ./...)
