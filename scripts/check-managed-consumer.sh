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

var _ func(context.Context) (claude.TreeRecord, error) = (*claude.Client)(nil).Receive

// Compile-only: go test runs no CLI or model calls.
func consume(ctx context.Context, config types.Options) error {
    opts := claude.OptionsFromTypes(config)
    opts.Tree = claude.TreeOptions{MaxNodes: 8}
    c, err := claude.Open(ctx, opts)
    if err != nil { return err }
    defer c.Close()
    r, err := c.Receive(ctx)
    if err != nil { return err }
    if r.Kind == "Native" {
        var native *types.Event = r.Native
        var official *types.MessageOrigin = native.Origin
        var message *types.Message = native.Assistant
        _ = official
        _ = message
        _ = native.Raw
    }
    return nil
}
GO
(cd "$consumer_dir" && go test ./...)
