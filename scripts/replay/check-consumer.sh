#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
tag=${1:?usage: check-consumer.sh SDK_TAG SDK_COMMIT_SHA}
revision=${2:?usage: check-consumer.sh SDK_TAG SDK_COMMIT_SHA}
go=${GO:-go}
module=github.com/zoumo/agent-sdk-go
work=$(mktemp -d "${TMPDIR:-/tmp}/agent-replay-consumer.XXXXXXXX")
work=$(cd "$work" && pwd -P)
cleanup() {
  chmod -R u+w "$work"
  rm -rf "$work"
}
trap cleanup EXIT

export GOWORK=off GOTOOLCHAIN=local GOENV=off GOFLAGS=
export GOPATH="$work/gopath" GOMODCACHE="$work/module-cache" GOBIN="$work/bin"
mkdir "$work/consumer" "$work/home"
cp "$root/tests/replay/release/testdata/consumer/main.go" "$work/consumer/"
cd "$work/consumer"
"$go" mod init consumer.invalid/agent-replay-check
"$go" mod download -json "$module@$tag" > "$work/module.json"
sum=$("${PYTHON:-python3}" - "$work/module.json" "$module" "$tag" "$revision" <<'PY'
import json, sys
path, module, tag, revision = sys.argv[1:]
result = json.load(open(path))
assert (result['Path'], result['Version']) == (module, tag), result
assert result.get('Origin', {}).get('Hash') == revision, result
print(result['Sum'])
PY
)
"$go" get "$module@$tag"
"$go" mod tidy
"$go" mod verify

# Run the consumer outside the checkout, with only its downloaded module and dependencies.
env GOPROXY=off GOSUMDB=off "$go" build -o "$work/library-check" .
env HOME="$work/home" "$work/library-check" "$tag" "$sum"
cd "$work"
"$go" install "$module/cmd/agent-replay@$tag"
[[ "$("$GOBIN/agent-replay" version)" == "agent-replay $tag" ]]
"$go" version -m "$GOBIN/agent-replay" | awk -v path="$module" -v tag="$tag" -v sum="$sum" '
  $1 == "path" && $2 == path "/cmd/agent-replay" { command = 1 }
  $1 == "mod" && $2 == path && $3 == tag && $4 == sum { module = 1 }
  END { exit !(command && module) }
'
env HOME="$work/home" "$GOBIN/agent-replay" prepare --agent codex --case SDK-CASE-001 --fixture-dir "$work/installed-fixture"
"${PYTHON:-python3}" -c 'import json, sys; assert json.load(open(sys.argv[1]))["materialId"]' \
  "$work/installed-fixture/current.json"
printf 'External Replay library and go install verified %s at %s (%s)\n' "$tag" "$revision" "$sum"
