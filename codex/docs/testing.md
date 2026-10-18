# Testing and CI

Default checks run offline against synthetic stdio children, loopback WS/WSS peers
and embedded real sanitized transcripts. They require no Codex executable, login
or model calls. Go 1.26.9 is pinned in mise; CI pins Python 3.13.13. Local Python
checks require Python 3.11+ (`tomllib`); use `python3.13` below if the system
`python3` is older.

## Run locally

From the native `codex/` directory, keep all live switches disabled:

```sh
export GOTOOLCHAIN=local
export RUN_MAC_STDIO_LIVE=0 RUN_MAC_INTERACTIONS_LIVE=0 RUN_MAC_WEBSOCKET_LIVE=0
export SDK_REAL_AGENT_E2E=0
mise exec -- go mod download
mise exec -- go mod verify
mise exec -- go test -v -count=1 ./...
mise exec -- go test -race -v -count=1 ./...
mise exec -- go vet ./...
python3 docs/protocol/verify_inventory.py
mise exec -- bash scripts/check_generated.sh
python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v
python3 -m unittest discover -s scripts -p 'test_recording_sanitize.py' -v
python3 scripts/check_recordings.py
mise exec -- go run ../tests/consumers/codex-public
mise exec -- go run ./examples/observe-subagents-offline
mise exec -- go run ./examples/replay-recorded
git diff --check
```

Use `GOPROXY=off GOSUMDB=off` when dependencies are already cached to prohibit
module network access. The [pure API/configuration consumer](../../tests/consumers/codex-public/main.go) is
a repository validation check, separate from SDK demonstrations. The API usage
package and documentation examples in Go
test files compile with `go test ./...`; examples that require a real service
are not executed. The synthetic observation example checks its expected
transcript, and replay validates complete fixture consumption with no live fallback.
See [recorded events](recorded-events.md) for manifests, sanitizer checks and the
separate explicit maintenance entry point.

The public contract seam exercises exported APIs against controllable peers:
handshake, exact IDs, notifications before responses, unknown data, strict resume,
reverse-request authority, dispatch uncertainty, bounded queues, observer isolation
and cleanup. Controlled-time tests isolate call deadlines and handle expiry from
process scheduling. Directory checks compare filesystem identity, including macOS
path aliases, while retaining strict argument/environment assertions. These
contracts complement [official testing patterns](protocol/testing-patterns.md);
reading upstream tests is not evidence that those tests ran here.

## Hosted gates

The [workflow](../../.github/workflows/ci.yml) runs on pushes to `main`, pull requests
and manual dispatch, on Ubuntu 24.04 and macOS 15 with matrix fail-fast disabled.
Each job checks dependencies/module cleanliness, committed whitespace, CI negative
fixtures, inventory, reproducible generation, recording validation, uncached normal
and race suites, offline examples and vet. Live switches are explicitly zero;
real scenarios visibly report SKIP. A package-level success is not a live pass.
No Windows support claim is made.

Whitespace checks compare commit trees, independently of working-tree state:

| Event | Comparison |
| --- | --- |
| Push | Event `before` to checked-out HEAD; empty tree for an initial push |
| Pull request | Merge-base of event base/head to PR head; other tests use the checked-out merge revision |
| Manual | First parent to HEAD; empty tree for a root commit |

`python3 scripts/check_whitespace.py` requires a GitHub event JSON file and
`GITHUB_EVENT_NAME`/`GITHUB_EVENT_PATH`. Missing or invalid commits fail closed.
The gate rejects trailing whitespace, blank lines at EOF and spaces before tabs.
Negative fixtures use isolated temporary repositories and assert actual failures
for bad comparisons, whitespace and generated drift; they do not weaken checks.

Generation uses the checked-in `types/schema.json`, formats both generated Go
outputs and requires no diff in schema, bindings or generated tests. It downloads
no upstream source. This proves snapshot/output consistency, not source
revalidation or live compatibility. The inventory verifier can separately check
an official checkout at the exact [protocol pin](protocol/README.md).

CI uses read-only repository permissions, full comparison history, no persisted
checkout credentials, no service secrets and ordinary `pull_request` execution.
Concurrency cancels obsolete runs only within the same workflow/event/ref lane.
Actions are pinned by immutable SHA in the workflow; weekly Dependabot updates
require review of official release/tag correspondence and runner requirements.
Toolchain changes remain explicit maintenance.

## Live boundaries

The three `tests/live` harnesses require their own `RUN_MAC_*_LIVE=1` switch and
an explicit `MAC_LIVE_CODEX` executable. `RUN_REAL_CODEX_TESTS` is not a harness
switch. Their historical 9/9 model-attempt budget is exhausted and grants no
reruns. The independent recorder requires `SDK_REAL_AGENT_E2E=1` and refuses CI.
Neither mechanism is enabled by default checks. Current coverage and untriggered
scenarios are listed in [compatibility](compatibility.md#live-validation).
