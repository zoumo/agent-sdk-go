#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test "$(find . -name go.mod -not -path './.git/*' | wc -l | tr -d ' ')" = 1
test -z "$(find . \( -name go.work -o -name go.work.sum \) -not -path './.git/*' -print)"
test -z "$(find . -maxdepth 1 -name '*.go' -print)"
for shared in lineage queue mocktest; do
  imports=$(go list -deps -f '{{.ImportPath}}' "./internal/$shared")
  if [[ "$imports" == *github.com/zoumo/agent-sdk-go/codex* || "$imports" == *github.com/zoumo/agent-sdk-go/claude* ]]; then
    echo "internal/$shared must not import native SDK packages" >&2
    exit 1
  fi
done
for native in codex claude; do
  # Deliberately omit -test: native data packages have standard-library-only
  # production implementation plus the shared mcp contract; assertion/suite
  # libraries belong to tests.
  deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$native/types" | sed '/^$/d')
  case "$native" in
    codex)
      test "$deps" = "github.com/zoumo/agent-sdk-go/codex/types" ;;
    claude)
      test "$deps" = $'github.com/zoumo/agent-sdk-go/mcp\ngithub.com/zoumo/agent-sdk-go/claude/types' ;;
  esac
done
# The shared MCP value and validation package uses only the standard library.
mcp_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./mcp | sed '/^$/d')
test "$mcp_deps" = "github.com/zoumo/agent-sdk-go/mcp"
# The optional unified contract is above both native SDKs and must not acquire
# either native package, MASS, or a third-party production dependency.
agent_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent | sed '/^$/d')
test "$agent_deps" = $'github.com/zoumo/agent-sdk-go/internal/tracecontext\ngithub.com/zoumo/agent-sdk-go/mcp\ngithub.com/zoumo/agent-sdk-go/agent'
# Core admission and leases may use only the public agent contract in
# addition to the standard library. It cannot reach either native SDK or types.
corelease_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/corelease | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/tracecontext|\
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/mcp|\
    github.com/zoumo/agent-sdk-go/agent/internal/corelease) ;;
    *)
      echo "unexpected agent/internal/corelease dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$corelease_deps"
# Session event delivery adds only the reviewed FIFO and its ChannX dependency.
eventstream_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/eventstream | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/tracecontext|\
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/mcp|\
    github.com/zoumo/agent-sdk-go/agent/internal/eventstream|\
    github.com/zoumo/agent-sdk-go/internal/queue|\
    github.com/zoumo/golib/channx) ;;
    *)
      echo "unexpected agent/internal/eventstream dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$eventstream_deps"
# Transitive allowances do not authorize direct imports of carrier or FIFO internals.
for shared in corelease eventstream; do
  shared_imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' "./agent/internal/$shared")
  while IFS= read -r dependency; do
    case "$dependency" in
      github.com/zoumo/agent-sdk-go/agent) ;;
      github.com/zoumo/agent-sdk-go/internal/queue)
        test "$shared" = eventstream ;;
      *.*)
        echo "unexpected direct agent/internal/$shared dependency: $dependency" >&2
        exit 1
        ;;
    esac
  done <<< "$shared_imports"
done
# Submission tracing shares only the public contract and standard library.
submissiontrace_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/submissiontrace | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/tracecontext|\
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/mcp|\
    github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace) ;;
    *)
      echo "unexpected submissiontrace dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$submissiontrace_deps"
# The shared interaction ledger may use only the public agent contract and the
# reviewed UUID primitive. It never reaches an adapter, native SDK, or registry.
interactionledger_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/interactionledger | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/tracecontext|\
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/mcp|\
    github.com/zoumo/agent-sdk-go/agent/internal/interactionledger|\
    github.com/zoumo/agent-sdk-go/internal/uuidv4) ;;
    *)
      echo "unexpected agent/internal/interactionledger dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$interactionledger_deps"
# Each adapter may use standard library packages, its own native SDK, the
# adapter-neutral core, and reviewed SDK-internal mechanics. It must not reach
# the other adapter or the other native SDK.
for adapter in codex claude; do
  adapter_imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' "./agent/$adapter")
  while IFS= read -r dependency; do
    case "$dependency" in
      *.*)
        case "$dependency" in
          github.com/zoumo/agent-sdk-go/agent|\
          github.com/zoumo/agent-sdk-go/mcp|\
          github.com/zoumo/agent-sdk-go/agent/"$adapter"|\
          github.com/zoumo/agent-sdk-go/agent/internal/corelease|\
          github.com/zoumo/agent-sdk-go/agent/internal/eventstream|\
          github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace|\
          github.com/zoumo/agent-sdk-go/agent/internal/interactionledger|\
          github.com/zoumo/agent-sdk-go/"$adapter"|\
          github.com/zoumo/agent-sdk-go/"$adapter"/types|\
          github.com/zoumo/agent-sdk-go/internal/queue|\
          github.com/zoumo/agent-sdk-go/agent/internal/eventenvelope|\
          github.com/zoumo/agent-sdk-go/internal/uuidv4) ;;
          *)
            echo "unexpected agent/$adapter dependency: $dependency" >&2
            exit 1
            ;;
        esac
        ;;
    esac
  done <<< "$adapter_imports"
done
# Unified event-envelope construction is adapter-only shared mechanics. It must
# not acquire native SDKs or adapter-specific conversion dependencies.
eventenvelope_dependencies=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/eventenvelope | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/tracecontext|\
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/mcp|\
    github.com/zoumo/agent-sdk-go/agent/internal/eventenvelope|\
    github.com/zoumo/agent-sdk-go/internal/uuidv4) ;;
    *)
      echo "unexpected eventenvelope dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$eventenvelope_dependencies"
# The generic FIFO must directly reuse the reviewed ChannX package.
queue_imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/queue)
if ! [[ "$queue_imports" == *github.com/zoumo/golib/channx* ]]; then
  echo "internal/queue must directly import golib/channx" >&2
  exit 1
fi
# No additional non-standard queue dependency may enter through it.
queue_dependencies=$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/queue | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/internal/queue|github.com/zoumo/golib/channx) ;;
    *) echo "unexpected shared queue dependency: $dependency" >&2; exit 1 ;;
  esac
done <<< "$queue_dependencies"
# Test libraries must not leak into any native SDK's production dependency tree.
production_deps=$(go list -deps -f '{{.ImportPath}}' ./codex/... ./claude/... ./agent/...)
if [[ "$production_deps" == *github.com/stretchr/* ]]; then
  echo "testify dependencies must remain test-only" >&2
  exit 1
fi
# Recording/replay utilities and embedded fixtures are tooling, never SDK
# runtime dependencies. Inspect only the four supported SDK/data entry points.
sdk_dependencies=$(go list -deps -f '{{.ImportPath}}' ./agent ./agent/codex ./agent/claude ./codex ./codex/types ./claude ./claude/types)
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/golib/*)
      test "$dependency" = github.com/zoumo/golib/channx || {
        echo "SDK runtime may use only reviewed golib/channx: $dependency" >&2
        exit 1
      } ;;
  esac
done <<< "$sdk_dependencies"
if [[ "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/tools/* || "$sdk_dependencies" == *github.com/spf13/* || "$sdk_dependencies" == *github.com/inconshreveable/mousetrap* || "$sdk_dependencies" == *github.com/zoumo/agent-mock* || "$sdk_dependencies" == *github.com/zoumo/agent-sdk-testify* || "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/tests/* || "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/internal/mocktest* ]]; then
  echo "SDK production packages must not depend on agent-sdk-testify or its test bridge" >&2
  exit 1
fi
for native in codex claude; do
  if [[ "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/$native/tools* || "$sdk_dependencies" == *github.com/zoumo/agent-sdk-go/$native/tests/* ]]; then
    echo "SDK production packages must not import recording tools or fixtures" >&2
    exit 1
  fi
done
python3 - <<'PY'
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

rules = [
    (('agent/codex',), False, r'github\.com/zoumo/agent-sdk-go/agent/claude',
     'agent/codex must not import agent/claude'),
    (('agent/claude',), False, r'github\.com/zoumo/agent-sdk-go/agent/codex',
     'agent/claude must not import agent/codex'),
    (('codex', 'claude'), True, r'github\.com/zoumo/agent-sdk-go/agent',
     'native SDK production code must not import the agent layer'),
    (('agent', 'codex', 'claude'), True, r'github\.com/[^"]*/(mass|mass-go)(/|")',
     'SDK production code must not import MASS'),
    (('agent', 'codex', 'claude'), True, r'MASS_[A-Z0-9_]+',
     'SDK production code must not depend on MASS environment conventions'),
    (('internal', 'tests', 'codex', 'claude'), False,
     r'protocol mock|"protocol"\s*,\s*"mock"|--scenario|--sdk-test-handshakes',
     'legacy mock CLI dependency in SDK process adapter'),
]

def scan_error(error):
    raise error

for roots, production_only, pattern, description in rules:
    expression = re.compile(pattern)
    for root in roots:
        for directory, _, names in os.walk(root, onerror=scan_error):
            for name in sorted(names):
                path = Path(directory) / name
                if not name.endswith('.go') or path.is_symlink():
                    continue
                if not stat.S_ISREG(path.stat().st_mode):
                    continue
                if production_only and name.endswith('_test.go'):
                    continue
                for number, line in enumerate(path.read_text(encoding='utf-8').split('\n'), 1):
                    if expression.search(line):
                        print(f'{path}:{number}:{line}', file=sys.stderr)
                        sys.exit(description)

rows = subprocess.check_output(['go', 'list', '-json', './codex/...', './claude/...', './tests/simulation/drivers/codex/go/...', './tests/simulation/drivers/claude/go/...'], text=True)
decoder = json.JSONDecoder()
while rows.strip():
    item, end = decoder.raw_decode(rows.lstrip())
    rows = rows.lstrip()[end:]
    path = item['ImportPath']
    own = 'codex' if '/codex' in path else 'claude'
    other = 'claude' if own == 'codex' else 'codex'
    for dep in item.get('Imports', []) + item.get('TestImports', []) + item.get('XTestImports', []):
        assert not dep.startswith('github.com/zoumo/agent-sdk-go/' + other), (path, dep)
        assert not dep.startswith('github.com/zoumo/agent-sdk-go/tests/simulation/drivers/' + other), (path, dep)
        assert not dep.startswith(('github.com/zoumo/codex-sdk-go', 'github.com/zoumo/claude-agent-sdk-go')), (path, dep)
PY
