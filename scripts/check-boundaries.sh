#!/usr/bin/env bash
set -euo pipefail
if ! command -v rg >/dev/null 2>&1; then
  echo "check-boundaries.sh requires ripgrep (rg)" >&2
  exit 1
fi
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
  # production dependencies; assertion/suite libraries belong to tests.
  deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$native/types" | sed '/^$/d')
  test "$deps" = "github.com/zoumo/agent-sdk-go/$native/types"
done
# The optional unified contract is above both native SDKs and must not acquire
# either native package, MASS, or a third-party production dependency.
agent_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent | sed '/^$/d')
test "$agent_deps" = "github.com/zoumo/agent-sdk-go/agent"
# The shared Provider lifecycle may use only the public agent contract in
# addition to the standard library. It cannot reach either native SDK or types.
lifecycle_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/lifecycle | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/agent/internal/lifecycle) ;;
    *)
      echo "unexpected agent/internal/lifecycle dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$lifecycle_deps"
# The shared interaction ledger may use only the public agent contract and the
# reviewed UUID primitive. It never reaches an adapter, native SDK, or registry.
interactionledger_deps=$(GOPROXY=off GOSUMDB=off go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./agent/internal/interactionledger | sed '/^$/d')
while IFS= read -r dependency; do
  case "$dependency" in
    github.com/zoumo/agent-sdk-go/agent|\
    github.com/zoumo/agent-sdk-go/agent/internal/interactionledger|\
    github.com/zoumo/agent-sdk-go/internal/uuidv4) ;;
    *)
      echo "unexpected agent/internal/interactionledger dependency: $dependency" >&2
      exit 1
      ;;
  esac
done <<< "$interactionledger_deps"
# Each adapter may use standard library packages, its own native SDK, the
# provider-neutral core, and reviewed SDK-internal mechanics. It must not reach
# the other adapter or the other native SDK.
for adapter in codex claude; do
  adapter_imports=$(go list -f '{{range .Imports}}{{println .}}{{end}}' "./agent/$adapter")
  while IFS= read -r dependency; do
    case "$dependency" in
      *.*)
        case "$dependency" in
          github.com/zoumo/agent-sdk-go/agent|\
          github.com/zoumo/agent-sdk-go/agent/"$adapter"|\
          github.com/zoumo/agent-sdk-go/agent/internal/lifecycle|\
          github.com/zoumo/agent-sdk-go/agent/internal/interactionledger|\
          github.com/zoumo/agent-sdk-go/"$adapter"|\
          github.com/zoumo/agent-sdk-go/"$adapter"/types|\
          github.com/zoumo/agent-sdk-go/internal/queue|\
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
if rg -n --glob '*.go' 'github.com/zoumo/agent-sdk-go/agent/claude' agent/codex; then
  echo "agent/codex must not import agent/claude" >&2
  exit 1
fi
if rg -n --glob '*.go' 'github.com/zoumo/agent-sdk-go/agent/codex' agent/claude; then
  echo "agent/claude must not import agent/codex" >&2
  exit 1
fi
# Native production code stays below the optional unified layer.
if rg -n --glob '*.go' --glob '!**/*_test.go' 'github.com/zoumo/agent-sdk-go/agent' codex claude; then
  echo "native SDK production code must not import the agent layer" >&2
  exit 1
fi
# The SDK never imports MASS implementation code or reads MASS environment
# conventions. MASS owns configuration, identity, persistence, and aggregation.
if rg -n --glob '*.go' --glob '!**/*_test.go' 'github\.com/[^"]*/(mass|mass-go)(/|")' agent codex claude; then
  echo "SDK production code must not import MASS" >&2
  exit 1
fi
if rg -n --glob '*.go' --glob '!**/*_test.go' 'MASS_[A-Z0-9_]+' agent codex claude; then
  echo "SDK production code must not depend on MASS environment conventions" >&2
  exit 1
fi
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
import json, subprocess
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

# SDK process adapters must target the external record/verify/mock interface,
# never the removed protocol subcommands or scenario flag.
if rg -n --glob '*.go' 'protocol mock|"protocol"[[:space:]]*,[[:space:]]*"mock"|--scenario|--sdk-test-handshakes' internal tests codex claude; then
 echo "legacy mock CLI dependency in SDK process adapter" >&2
 exit 1
fi
