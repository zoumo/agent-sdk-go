#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test "$(find . -name go.mod -not -path './.git/*' | wc -l | tr -d ' ')" = 1
test -z "$(find . \( -name go.work -o -name go.work.sum \) -not -path './.git/*' -print)"
test -z "$(find . -maxdepth 1 -name '*.go' -print)"
if [[ -d internal/lineage ]]; then
  imports=$(go list -deps -f '{{.ImportPath}}' ./internal/lineage)
  if [[ "$imports" == *github.com/zoumo/agent-sdk-go/codex* || "$imports" == *github.com/zoumo/agent-sdk-go/claude* ]]; then
    echo "internal/lineage must not import native SDK packages" >&2
    exit 1
  fi
fi
for native in codex claude; do
  # Deliberately omit -test: native data packages have standard-library-only
  # production dependencies; assertion/suite libraries belong to tests.
  deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$native/types" | sed '/^$/d')
  test "$deps" = "github.com/zoumo/agent-sdk-go/$native/types"
done
# Test libraries must not leak into any native SDK's production dependency tree.
production_deps=$(go list -deps -f '{{.ImportPath}}' ./codex/... ./claude/...)
if [[ "$production_deps" == *github.com/stretchr/* ]]; then
  echo "testify dependencies must remain test-only" >&2
  exit 1
fi
# Recording/replay utilities and embedded fixtures are tooling, never SDK
# runtime dependencies. Inspect only the four supported SDK/data entry points.
sdk_dependencies=$(go list -deps -f '{{.ImportPath}}' ./codex ./codex/types ./claude ./claude/types)
for native in codex claude; do
  if [[ "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/$native/tools* || "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/$native/tests/testdata* ]]; then
    echo "SDK production packages must not import recording tools or fixtures" >&2
    exit 1
  fi
done
python3 - <<'PY'
import json, subprocess
rows = subprocess.check_output(['go', 'list', '-json', './codex/...', './claude/...'], text=True)
decoder = json.JSONDecoder()
while rows.strip():
    item, end = decoder.raw_decode(rows.lstrip())
    rows = rows.lstrip()[end:]
    path = item['ImportPath']
    own = 'codex' if '/codex' in path else 'claude'
    other = 'claude' if own == 'codex' else 'codex'
    for dep in item.get('Imports', []) + item.get('TestImports', []) + item.get('XTestImports', []):
        assert not dep.startswith('github.com/zoumo/agent-sdk-go/' + other), (path, dep)
        assert not dep.startswith(('github.com/zoumo/codex-sdk-go', 'github.com/zoumo/claude-agent-sdk-go')), (path, dep)
PY
