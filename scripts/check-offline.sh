#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python=${PYTHON:-python3}
task=${1:-all}
if (( $# > 1 )); then
  echo "usage: $0 [all|parallel|quality|test|race|e2e|build]" >&2
  exit 2
fi
export GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off
export CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0
export CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0
export GORACE=atexit_sleep_ms=0
export GOMAXPROCS=2

quality_checks() {
  bash scripts/check-boundaries.sh
  bash scripts/check-removed-cli-surfaces.sh
  go mod verify
  "$python" -m unittest discover -s codex/scripts -p 'test_ci_checks.py' -v
  "$python" codex/docs/protocol/verify_inventory.py
  (cd codex && bash scripts/check_generated.sh)
  go vet ./...
}

test_checks() {
  # ./... includes both native tests/e2e packages; real CLI/agent tests skip.
  go test -p=1 -count=1 -timeout 180s ./...
  for example in codex/examples/history-query-offline codex/examples/observe-subagents-offline claude/examples/subagents-offline claude/examples/session-messages-offline claude/examples/session-metadata-offline; do
    go run "./$example"
  done
}

race_checks() {
  go test -race -p=1 -count=1 -timeout 600s ./...
}

e2e_checks() {
  # Required real-recording regression is independent of the synthetic tests above.
  # Missing trusted inputs fail closed; this command never records or uses accounts.
  bash scripts/simulation.sh gate
}

build_checks() {
  go build ./...
  bash scripts/simulation.sh build
}

run_task_lane() {
  local work=$1
  shift
  local failed=0 task

  for task in "$@"; do
    if AGENT_SDK_TESTIFY_BUILD_DIR="$work/$task/simulation" \
      bash "$0" "$task" >"$work/$task.log" 2>&1; then
      printf 'passed\n' >"$work/$task.status"
    else
      printf 'failed\n' >"$work/$task.status"
      failed=1
    fi
  done
  return "$failed"
}

parallel_checks() {
  local parallel_tasks=(test build)
  local serial_tasks=(race e2e)
  local all_tasks=(test race e2e build)
  local work
  work=$(mktemp -d "${TMPDIR:-/tmp}/agent-sdk-offline.XXXXXXXX")
  local failed=0
  local pids=()
  local task pid

  for task in "${parallel_tasks[@]}"; do
    run_task_lane "$work" "$task" &
    pids+=("$!")
  done

  for pid in "${pids[@]}"; do
    if ! wait "$pid"; then
      failed=1
    fi
  done

  # Race and the recording gate both exercise timing-sensitive subprocess
  # protocols. Keep them isolated from each other and from package tests.
  for task in "${serial_tasks[@]}"; do
    if ! run_task_lane "$work" "$task"; then
      failed=1
    fi
  done

  for task in "${all_tasks[@]}"; do
    printf '\n===== %s (%s) =====\n' "$task" "$(cat "$work/$task.status")"
    cat "$work/$task.log"
  done
  rm -rf "$work"
  return "$failed"
}

case "$task" in
  all)
    quality_checks
    test_checks
    race_checks
    e2e_checks
    build_checks
    ;;
  parallel)
    quality_checks
    parallel_checks
    ;;
  quality)
    quality_checks
    ;;
  test)
    test_checks
    ;;
  race)
    race_checks
    ;;
  e2e)
    e2e_checks
    ;;
  build)
    build_checks
    ;;
  *)
    echo "usage: $0 [all|parallel|quality|test|race|e2e|build]" >&2
    exit 2
    ;;
esac
