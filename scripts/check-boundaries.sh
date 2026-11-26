#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test "$(find . -name go.mod -not -path './.git/*' | wc -l | tr -d ' ')" = 1
test -z "$(find . \( -name go.work -o -name go.work.sum \) -not -path './.git/*' -print)"
test -z "$(find . -maxdepth 1 -name '*.go' -print)"
module=github.com/zoumo/agent-sdk-go
for shared in client/internal/lineage internal/queue internal/testsupport/mocktest; do
  imports=$(go list -deps -f '{{.ImportPath}}' "./$shared")
  while IFS= read -r dependency; do
    case "$dependency" in
      "$module"/client/codex|"$module"/client/codex/*|\
      "$module"/client/claude|"$module"/client/claude/*|\
      "$module"/client/pi|"$module"/client/pi/*)
        echo "$shared must not import native SDK packages: $dependency" >&2
        exit 1 ;;
    esac
  done <<< "$imports"
done
for native in codex claude; do
  # Deliberately omit -test: native data packages have standard-library-only
  # production implementation plus the shared mcp contract; assertion/suite
  # libraries belong to tests.
  deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./client/$native/types" | sed '/^$/d')
  case "$native" in
    codex)
      test "$deps" = "github.com/zoumo/agent-sdk-go/client/codex/types" ;;
    claude)
      test "$deps" = $'github.com/zoumo/agent-sdk-go/mcp\ngithub.com/zoumo/agent-sdk-go/client/claude/types' ;;
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
for adapter in codex claude pi; do
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
          github.com/zoumo/agent-sdk-go/agent/internal/telemetry|\
          github.com/zoumo/agent-sdk-go/agent/internal/interactionledger|\
          github.com/zoumo/agent-sdk-go/client/"$adapter"|\
          github.com/zoumo/agent-sdk-go/client/"$adapter"/types|\
          github.com/zoumo/agent-sdk-go/agent/"$adapter"/internal/*|\
          github.com/zoumo/agent-sdk-go/internal/queue|\
          github.com/zoumo/agent-sdk-go/agent/internal/eventenvelope|\
          github.com/zoumo/agent-sdk-go/internal/uuidv4|\
          go.opentelemetry.io/otel/trace|go.opentelemetry.io/otel/metric) ;;
          *)
            echo "unexpected agent/$adapter dependency: $dependency" >&2
            exit 1
            ;;
        esac
        ;;
    esac
  done <<< "$adapter_imports"
done
# Only the shared adapter instrumentation may use the rest of the OTel API.
telemetry_imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' ./agent/internal/telemetry)
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/agent/internal/submissiontrace|\
    go.opentelemetry.io/otel|\
    go.opentelemetry.io/otel/attribute|go.opentelemetry.io/otel/codes|\
    go.opentelemetry.io/otel/metric|go.opentelemetry.io/otel/propagation|\
    go.opentelemetry.io/otel/trace) ;;
    *.*)
      echo "unexpected agent/internal/telemetry dependency: $dependency" >&2
      exit 1 ;;
  esac
done <<< "$telemetry_imports"
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
# Select SDK production packages. Scenario, example and E2E packages are tooling.
sdk_packages=()
sdk_paths=$(go list -f '{{.ImportPath}}' ./agent/... ./client/... ./mcp)
while IFS= read -r package; do
  case "$package" in
    */tests|*/tests/*|*/examples|*/examples/*) ;;
    *) sdk_packages+=("$package") ;;
  esac
done <<< "$sdk_paths"
sdk_dependencies=$(go list -deps -f '{{.ImportPath}}' "${sdk_packages[@]}")
while IFS= read -r dependency; do
  case "$dependency" in
    "$module"/internal/testsupport|"$module"/internal/testsupport/*|\
      "$module"/tests|"$module"/tests/*|*/tests/simulation|*/tests/simulation/*|\
      "$module"/replay|"$module"/replay/*|"$module"/internal/replay/*|"$module"/cmd/*|"$module"/tools/*|\
      github.com/spf13/*|github.com/inconshreveable/mousetrap|\
      github.com/zoumo/agent-mock*|github.com/zoumo/agent-sdk-testify*|\
      github.com/stretchr/*|go.opentelemetry.io/otel/sdk*|go.opentelemetry.io/otel/exporters*)
      echo "SDK production packages must not depend on test or replay tooling: $dependency" >&2
      exit 1 ;;
    github.com/zoumo/golib/channx) ;;
    github.com/zoumo/golib/*)
      echo "SDK runtime may use only reviewed golib/channx: $dependency" >&2
      exit 1 ;;
  esac
done <<< "$sdk_dependencies"

for native in codex claude pi; do
  for package in "${sdk_packages[@]}"; do
    case "$package" in
      "$module"/client/"$native"|"$module"/client/"$native"/*)
        dependencies=$(go list -deps -f '{{.ImportPath}}' "$package")
        while IFS= read -r dependency; do
          case "$dependency" in
            "$module"/client/"$native"|"$module"/client/"$native"/*|"$module"/client/internal/*) ;;
            "$module"/client/*|"$module"/agent|"$module"/agent/*|go.opentelemetry.io/*)
              echo "$package must stay independent of $dependency" >&2
              exit 1 ;;
          esac
        done <<< "$dependencies" ;;
    esac
  done
done

# Both the SDK runner and its test bridge must use Replay's public Go package.
for host in ./internal/testsupport/mocktest ./tests/simulation; do
  imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' "$host")
  if grep -Eq '^github[.]com/zoumo/(agent-sdk-[t]estify|agent-sdk-go/(replay/|internal/replay/))' <<< "$imports"; then
    echo "$host imports private Replay tooling or the retired external module" >&2
    exit 1
  fi
  if ! grep -Fxq "$module/replay" <<< "$imports"; then
    echo "$host must import the public Replay library" >&2
    exit 1
  fi
done

python3 - <<'PYTHON'
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

rules = [
    (('agent', 'client'), True, r'github\.com/[^"]*/(mass|mass-go)(/|")',
     'SDK production code must not import MASS'),
    (('agent', 'client'), True, r'MASS_[A-Z0-9_]+',
     'SDK production code must not depend on MASS environment conventions'),
    (('internal', 'tests/simulation', 'client', 'agent'), False,
     r'protocol mock|"protocol"\s*,\s*"mock"|--scenario|--sdk-test-handshakes',
     'legacy mock CLI dependency in SDK process adapter'),
]

def scan_error(error):
    raise error

for roots, production_only, pattern, description in rules:
    expression = re.compile(pattern)
    for root in roots:
        for directory, _, names in os.walk(root, onerror=scan_error):
            if production_only and {'tests', 'examples'}.intersection(Path(directory).parts):
                continue
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

module = 'github.com/zoumo/agent-sdk-go/'
rows = subprocess.check_output(
    ['go', 'list', '-json', './agent/...', './client/...', './tests/simulation/...', './internal/testsupport/mocktest'],
    text=True,
)
decoder = json.JSONDecoder()
while rows.strip():
    item, end = decoder.raw_decode(rows.lstrip())
    rows = rows.lstrip()[end:]
    path = item['ImportPath']
    relative_path = path.removeprefix(module)
    parts = relative_path.split('/')
    provider = parts[1] if len(parts) > 1 and parts[1] in {'codex', 'claude', 'pi'} else None
    dependencies = item.get('Imports', []) + item.get('TestImports', []) + item.get('XTestImports', [])
    for dependency in dependencies:
        assert not dependency.startswith(('github.com/zoumo/codex-sdk-go', 'github.com/zoumo/claude-agent-sdk-go')), (path, dependency)
        if provider:
            for other in {'codex', 'claude', 'pi'} - {provider}:
                for domain in ('agent', 'client'):
                    prefix = module + domain + '/' + other
                    assert dependency != prefix and not dependency.startswith(prefix + '/'), (path, dependency)
        if dependency.startswith((module + 'replay/', module + 'internal/replay/', 'github.com/zoumo/agent-sdk-testify')):
            sys.exit(f'{path} must use the public Replay entry point: {dependency}')
PYTHON
