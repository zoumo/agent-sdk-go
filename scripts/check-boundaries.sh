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
  deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$native/types" | sed '/^$/d')
  test "$deps" = "github.com/zoumo/agent-sdk-go/$native/types"
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
