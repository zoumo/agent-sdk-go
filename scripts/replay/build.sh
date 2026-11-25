#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
binary=${1:?usage: build.sh OUTPUT_BINARY [SDK_VERSION]}
[[ "$binary" = /* ]] || binary="$PWD/$binary"
version=${2:-$(git -C "$root" describe --tags --exact-match --match 'v*.*.*' 2>/dev/null || printf devel)}
cd "$root"
if [[ -f .git ]]; then
  exec bash scripts/replay/with-build-checkout.sh bash scripts/replay/build.sh "$binary" "$version"
fi
go=${GO:-go}
export GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOENV=off
"$go" run -tags builtin_generate ./cmd/agent-replay --check
mkdir -p "$(dirname "$binary")"
"$go" build -trimpath -buildvcs=true -ldflags="-X=github.com/zoumo/agent-sdk-go/replay/internal/version.buildVersion=$version" \
  -o "$binary" ./cmd/agent-replay
