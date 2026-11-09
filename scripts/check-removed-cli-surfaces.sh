#!/usr/bin/env bash
# Reject reintroduction of the retired external testify CLI workflow.
set -euo pipefail

root=${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
cd "$root"
go_binary=${GO:-go}
failed=0

report() {
  printf '%s\n' "$1" >&2
  failed=1
}

scan() {
  local description=$1
  shift
  local output status=0
  output=$(rg -n --color never --hidden --no-ignore-vcs "$@" .) || status=$?
  case $status in
    0) printf '%s\n' "$output" >&2; report "$description" ;;
    1) ;;
    *) report "surface scan failed: $description" ;;
  esac
}

# Exclusions must follow positive globs; ripgrep resolves globs in order.
common=(
  --glob '!.artifacts/**'
  --glob '!.git/**'
  --glob '!.scratch/**'
  --glob '!scripts/check-removed-cli-surfaces.sh'
  --glob '!tests/gates/contraction_test.go'
  --glob '!go.mod'
  --glob '!go.sum'
)

scan "external testify executable environment and completion file must remain removed" \
  "${common[@]}" \
  'AGENT_SDK_TESTIFY_BIN|CLAUDE_GO_REPLAY_COMPLETE'

scan "external testify wrapper and version manifest must remain removed" \
  "${common[@]}" \
  'with-agent-sdk-testify|scripts/agent-sdk-testify-version\.json'

scan "retired testify process workflow terms must remain absent from required documentation" \
  -i \
  --glob 'harness/modules/agent-sdk-testify/**' \
  --glob 'harness/architecture.md' \
  --glob 'harness/testing.md' \
  --glob 'docs/testing.md' \
  --glob 'tests/simulation/drivers/README.md' \
  --glob '/README.md' \
  "${common[@]}" \
  'launcher|wrapper|result[[:space:]]+(descriptor|notice)|progress[[:space:]]+descriptor|process[[:space:]]+receipt|receipt\.json|executions/index\.json|schema-[34]|schemaVersion[[:space:]]+[34]'

scan "external testify executable must not be launched" \
  "${common[@]}" \
  "(^[[:space:]]*|[;&|()][[:space:]]*|(exec|command|env|nohup|sudo)[[:space:]]+)([.]?/)?([[:alnum:]_.-]+/)*agent-sdk-testify([[:space:];&|)]|$)|[\"']agent-sdk-testify[\"']"

scan "bridge byte-copy, completion, command, and launcher entry points must remain removed" \
  --glob '*.go' "${common[@]}" \
  'func \([[:alnum:]_]+ \*?Session\) (Run|Command|Launcher)\(|Options\.Complete|\.options\.Complete|options\.Complete|\bComplete[[:space:]]+func|ErrDisconnected'

scan "process-oriented simulation connection contract must remain removed" \
  --glob '*.go' --glob '!**/*_test.go' \
  --glob '!codex/types/**' --glob '!claude/types/**' --glob '!agent/**' \
  "${common[@]}" \
  'json:"(command|launcher|pid|resultDescriptor|progressDescriptor)"'

scan "result notice, progress descriptor, and process receipt parsing must remain removed" \
  --glob '*.go' --glob '!**/*_test.go' "${common[@]}" \
  'resultDescriptor|progressDescriptor|executions/index\.json|receipt\.json|receipts/'

scan "schema-3 and schema-4 comparison readers must remain removed" \
  --glob '*.go' --glob '!**/*_test.go' "${common[@]}" \
  '(schemaVersion|SchemaVersion)[^[:cntrl:]]*(==|!=|<|>|\brange\b)[^[:cntrl:]]*(^|[^0-9])[34]([^0-9]|$)'

scan "schema-3 and schema-4 switch readers must remain removed" \
  -U -P --glob '*.go' --glob '!**/*_test.go' "${common[@]}" \
  '(?is)(schemaVersion|SchemaVersion)[\s\S]{0,300}?case\s+[^:]*\b[34]\b'

for path in scripts/with-agent-sdk-testify.sh scripts/agent-sdk-testify-version.json; do
  if [[ -e $path || -L $path ]]; then
    report "removed testify wrapper path must remain absent: $path"
  fi
done

if ! bridge_imports=$("$go_binary" list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/mocktest); then
  report "unable to inspect internal/mocktest imports"
elif printf '%s\n' "$bridge_imports" | grep -qx 'os/exec'; then
  report "the in-process bridge must not launch an executable"
fi

if [[ $failed -ne 0 ]]; then
  exit 1
fi

printf 'removed testify CLI surfaces are absent\n'
