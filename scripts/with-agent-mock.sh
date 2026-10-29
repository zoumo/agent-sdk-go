#!/usr/bin/env bash
# Build this checkout’s mock once, then run a caller-selected SDK check.
# Usage: bash scripts/with-agent-mock.sh mise exec -- go test ./...
# Or supply an already built absolute AGENT_MOCK_BIN; no source checkout needed.
set -euo pipefail
if [[ $# == 0 ]]; then
  echo "usage: $0 COMMAND [ARG ...] (optionally set AGENT_MOCK_BIN)" >&2
  exit 2
fi
if [[ -n "${AGENT_MOCK_BIN:-}" ]]; then
  [[ "$AGENT_MOCK_BIN" = /* && -f "$AGENT_MOCK_BIN" && -x "$AGENT_MOCK_BIN" ]] || {
    echo "AGENT_MOCK_BIN must be an absolute executable file" >&2
    exit 2
  }
else
  mock_source=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
  test "$(mise exec -C "$mock_source" -- go env GOVERSION)" = go1.26.9
  mock_build_dir=$(mktemp -d "${TMPDIR:-/tmp}/sdk-agent-mock.XXXXXXXX")
  trap 'rm -rf "$mock_build_dir"' EXIT
  export AGENT_MOCK_BIN="$mock_build_dir/agent-mock"
  GOPROXY=off GOSUMDB=off mise exec -C "$mock_source" -- go -C "$mock_source" build -o "$AGENT_MOCK_BIN" ./tools/agent-mock
fi
export AGENT_MOCK_BIN
"$AGENT_MOCK_BIN" version >&2
"$@"
