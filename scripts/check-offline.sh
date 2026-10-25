#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off
export CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0
export CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0
export GORACE=atexit_sleep_ms=0
export GOMAXPROCS=2
python3 -m unittest discover -s scripts -p test_story_evidence.py -v
python3 scripts/check-story-evidence.py
bash scripts/check-boundaries.sh
go mod verify
python3 -m unittest discover -s codex/scripts -p 'test_ci_checks.py' -v
python3 codex/docs/protocol/verify_inventory.py
(cd codex && bash scripts/check_generated.sh)
python3 -m unittest discover -s codex/scripts -p 'test_recording_sanitize.py' -v
python3 codex/scripts/check_recordings.py
python3 -m unittest discover -s claude/scripts -p 'test_publisher_version.py' -v
python3 claude/scripts/check-transcripts.py
# ./... includes both native tests/e2e packages; real CLI/agent tests skip.
go test -p=1 -count=1 -timeout 180s ./...
# The pure public API/configuration consumer is a validation check, not an SDK demo.
go run ./tests/consumers/codex-public
for example in codex/examples/history-query-offline codex/examples/observe-subagents-offline codex/examples/replay-recorded claude/examples/subagents-offline claude/examples/recorded-replay claude/examples/session-messages-offline claude/examples/session-metadata-offline; do
  go run "./$example"
done
