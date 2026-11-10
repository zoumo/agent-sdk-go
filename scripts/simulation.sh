#!/usr/bin/env bash
# SDK-owned build and simulation. No wheel, native Agent, or tool-source fallback.
set -euo pipefail
simulation_source=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
simulation_build=${AGENT_SDK_TESTIFY_BUILD_DIR:-"$simulation_source/.artifacts/simulation"}
case "${1:---help}" in
 build)
  mkdir -p "$simulation_build"
  go -C "$simulation_source" build -o "$simulation_build/sdk-driver" ./tests/simulation/drivers
  go -C "$simulation_source" build -o "$simulation_build/sdk-simulate" ./tests/simulation/simulate
  ;;
 simulate|gate)
  simulation_mode=$1; shift
  if [[ "$simulation_mode" == gate ]]; then
   bash "$simulation_source/scripts/simulation.sh" build
   mkdir -p "$simulation_source/.artifacts/simulation/gates"
   simulation_output=$(mktemp -d "$simulation_source/.artifacts/simulation/gates/gate.XXXXXXXX")
   rmdir "$simulation_output"
   set -- --gate --output "$simulation_output" "$@"
  fi
  exec "$simulation_build/sdk-simulate" --driver "$simulation_build/sdk-driver" "$@"
  ;;
 --help|-h)
  echo "Usage: scripts/simulation.sh build | simulate --provider codex|claude --case ID --output ABS | gate [--jobs N]"
  ;;
 *) echo "record/verify/material management belongs to agent-sdk-testify; SDK supports build, simulate, gate" >&2;exit 2 ;;
esac
