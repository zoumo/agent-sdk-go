#!/usr/bin/env bash
# Explicit local integration until an actual external release can be pinned.
set -euo pipefail
if [[ $# == 0 ]]; then echo "usage: $0 COMMAND [ARG ...]" >&2; exit 2; fi
: "${AGENT_SDK_TESTIFY_BIN:?No published agent-sdk-testify release pin. Set an absolute locally built AGENT_SDK_TESTIFY_BIN.}"
[[ "$AGENT_SDK_TESTIFY_BIN" = /* && -f "$AGENT_SDK_TESTIFY_BIN" && -x "$AGENT_SDK_TESTIFY_BIN" ]] || {
 echo "AGENT_SDK_TESTIFY_BIN must be an absolute executable" >&2; exit 2;
}
export AGENT_SDK_TESTIFY_BIN
"$AGENT_SDK_TESTIFY_BIN" version >&2
"$@"
