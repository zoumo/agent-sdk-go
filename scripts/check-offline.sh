#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -z "${AGENT_SDK_TESTIFY_BIN:-}" ]]; then
  exec bash scripts/with-agent-sdk-testify.sh bash scripts/check-offline.sh "$@"
fi
export GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off
export CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0
export CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0
export GORACE=atexit_sleep_ms=0
export GOMAXPROCS=2
python3 -m unittest discover -s scripts -p test_story_evidence.py -v
PYTHONPATH=tools/official-sdk-drivers/common:tools/official-sdk-drivers/codex:tools/official-sdk-drivers/claude python3 -m unittest discover -s tools/official-sdk-drivers/tests -p 'test_*.py' -v
python3 -m unittest discover -s tools/official-sdk-drivers/tests/scenarios -p 'test_*.py' -v
python3 scripts/check-story-evidence.py
bash scripts/check-boundaries.sh
go mod verify
python3 -m unittest discover -s codex/scripts -p 'test_ci_checks.py' -v
python3 codex/docs/protocol/verify_inventory.py
(cd codex && bash scripts/check_generated.sh)
# ./... includes both native tests/e2e packages; real CLI/agent tests skip.
go test -p=1 -count=1 -timeout 180s ./...
for example in codex/examples/history-query-offline codex/examples/observe-subagents-offline claude/examples/subagents-offline claude/examples/session-messages-offline claude/examples/session-metadata-offline; do
  go run "./$example"
done
