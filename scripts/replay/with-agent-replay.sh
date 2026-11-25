#!/usr/bin/env bash
# Build once for the selected mode, then share the actual CLI with all test packages.
set -euo pipefail
race=false
if [[ "${1:-}" == --race ]]; then race=true; shift; fi
if [[ $# == 0 ]]; then
  echo "usage: $0 [--race] COMMAND [ARG ...] (optionally set AGENT_REPLAY_BIN)" >&2
  exit 2
fi
testify_go=${GO:-go}
if [[ -n "${AGENT_REPLAY_BIN:-}" ]]; then
  [[ "$AGENT_REPLAY_BIN" = /* && -f "$AGENT_REPLAY_BIN" && -x "$AGENT_REPLAY_BIN" ]] || {
    echo "AGENT_REPLAY_BIN must be an absolute executable file" >&2
    exit 2
  }
else
  testify_source=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
  testify_build_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-replay.XXXXXXXX")
  trap 'rm -rf "$testify_build_dir"' EXIT
  export AGENT_REPLAY_BIN="$testify_build_dir/agent-replay"
  version=$(git -C "$testify_source" describe --tags --exact-match 2>/dev/null || printf devel)
  build_flags=(build)
  if "$race"; then build_flags+=(-race); fi
  bash "$testify_source/scripts/replay/with-build-checkout.sh" "$testify_go" "${build_flags[@]}" \
    -ldflags="-X=github.com/zoumo/agent-sdk-go/replay/internal/version.buildVersion=$version" \
    -o "$AGENT_REPLAY_BIN" ./cmd/agent-replay
fi
identity=$("$testify_go" version -m "$AGENT_REPLAY_BIN")
package=$(awk '$1 == "path" { print $2 }' <<< "$identity")
actual_race=$(awk '$1 == "build" && $2 == "-race=true" { print "true" }' <<< "$identity")
[[ "$package" == github.com/zoumo/agent-sdk-go/cmd/agent-replay ]] || { echo "unexpected CLI package: $package" >&2; exit 2; }
[[ "${actual_race:-false}" == "$race" ]] || { echo "CLI race mode mismatch: artifact race=${actual_race:-false}, requested race=$race" >&2; exit 2; }
export AGENT_REPLAY_BIN
AGENT_REPLAY_VERSION="$("$AGENT_REPLAY_BIN" version | awk '{print $2}')"
export AGENT_REPLAY_VERSION
export GORACE="${GORACE:-} atexit_sleep_ms=0"
printf 'CLI: %s (race=%s)\n' "$AGENT_REPLAY_BIN" "$race" >&2
"$@"
