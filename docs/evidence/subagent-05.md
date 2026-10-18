# Ticket 05 — compatibility publication and final offline acceptance

Baseline: clean main `40438a66bc4b6539ac97f993d704dae4684a45f8` (ticket 04).
Before editing, origin/main matched this exact SHA and hosted run
[39100164851](https://github.com/zoumo/codex-sdk-go/actions/runs/39100164851)
was independently read back completed/success on Linux and macOS.

## Delivered contract

The [per-capability matrix](../subagent-compatibility.md) separates native
availability, SDK implementation, pinned E1 source evidence, executed E2
contracts and runtime limits across 17 capabilities. README, canonical
compatibility, protocol provenance and method/control inventory point to the
same boundaries. The [runnable example](../../examples/observe-tree-offline/README.md)
starts only itself as a fake stdio peer with an empty environment. Its
[complete stdout](../../examples/observe-tree-offline/expected.txt) is checked
byte for byte by TestOfflineTranscript through public Client APIs.

The example covers root → child → grandchild, early child routing, explicit
unresolved Native, local Close with a still-usable connection, and bounded
history-only Associated. Private fixture/* controls are not Codex methods;
unexpected RPCs fail the fixture. No production functionality or generated
binding was changed in ticket 05. Method-level inventory descriptions for raw
thread/started, status, reasoning and usage now cite their actual scoped tests;
they remain partial/raw-only rather than claiming new typed roots or workflows.

## E1 provenance

Official checkout HEAD was independently confirmed as
`d27764b82f7118f674371e6d6e76271d9d606edb`, with clean status. The exact source
bytes, Git blob IDs, registry membership and stable/experimental schema
memberships were checked against [sources.json](../protocol/sources.json).
The reviewed auto-attach, per-thread status and Thread lineage sources are now
included there. Generator ROOTS and snapshot roots match exactly (33);
regeneration retains 154 definitions, 397 types and 744 round-trip cases.

The system Python lacks zstandard and an offline uv resolution had no cached
package. The optional upstream verification therefore used the installed zstd
CLI for only that decompression call, without installing or downloading anything:

```sh
python3 - <<'PY'
import io, runpy, subprocess, sys, types
module = types.ModuleType('zstandard')
class Decompressor:
    def stream_reader(self, raw):
        return io.BytesIO(subprocess.run(
            ['zstd', '-d', '-q', '-c'], input=raw,
            stdout=subprocess.PIPE, check=True).stdout)
module.ZstdDecompressor = Decompressor
sys.modules['zstandard'] = module
sys.argv = ['docs/protocol/verify_inventory.py', '/path/to/exact-official-checkout']
runpy.run_path(sys.argv[0], run_name='__main__')
PY
```

This verifies source/schema provenance, not native runtime support. Official tests
were inspected as sources; they were not executed as SDK test evidence.

## E2 commands and exclusions

Environment for all Go verification:

```sh
export RUN_MAC_STDIO_LIVE=0 RUN_MAC_INTERACTIONS_LIVE=0 RUN_MAC_WEBSOCKET_LIVE=0
export GOPROXY=off GOSUMDB=off
mise exec -- go test -v -count=1 ./...
mise exec -- go test -race -v -count=1 ./...
mise exec -- go vet ./...
mise exec -- go mod download
mise exec -- go mod verify
python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v
python3 docs/protocol/verify_inventory.py
mise exec -- bash scripts/check_generated.sh
mise exec -- go run ./examples/observe-tree-offline
git diff --check
```

All final checks passed (exit 0), on Darwin arm64 with mise Go 1.26.9 on
2026-10-18 UTC. The final serial normal SDK suite completed in 46.415 seconds;
the final serial race SDK suite completed in 218.265 seconds. Three
focused WS/WSS flood repetitions passed without changing the 3-second assertion.
Six CI gate self-tests, inventory, generation, vet and cached module validation
passed. The runnable example output matched expected.txt. All three live
tests visibly skipped in normal and race suites.

The initial normal and race attempts were launched concurrently with the CI gate
negative-fixture build. Normal failed one existing WS flood response deadline
(3 seconds); race failed existing blocked-writer prefix waits (5 seconds).
The new example and all tree contracts passed in those attempts, with no race
report. Those failed attempts are retained in local logs; no timeout, assertion
or production behavior was weakened. Final acceptance uses serial reruns and
exact-delivery hosted CI, not those failed attempts.

Exact source/test SHA-256 values, baseline commit, UTC validation time, commands
and outcomes are in evidence.json records `subagent05-contract-mock`,
`subagent05-contract-race` and `subagent05-offline-gates`. These describe the
pre-commit working tree and do not pretend that the baseline contains the new
example. Private execution logs and planning drafts are not published. Hosted
CI verifies the delivered commit separately; its exact SHA/run result is returned
in the ticket handoff to avoid a self-referential evidence-only commit cycle.

All three tests in tests/live must visibly SKIP (the Mac stdio, interactions,
and WebSocket live tests). Only synthetic processes and loopback transports are
used. No real CLI/model, provider mock-backend integration, credential access,
authentication change or budget probe was performed. No subagent E3/E4 pass is
claimed. Cached modules only; no dependency or toolchain version changed.

## Whole-round acceptance

The reviewed implementation chain on main is
[01 bf765403](https://github.com/zoumo/codex-sdk-go/commit/bf7654035f1e073a2374bf2600e667d947684ac7) →
[02 9aca0c39](https://github.com/zoumo/codex-sdk-go/commit/9aca0c39a788b42ef7caa4d877f22fe30fee88d5) →
[03 94b7ba6c](https://github.com/zoumo/codex-sdk-go/commit/94b7ba6cceec55ca8a56493313d9fbae58988fcd) →
[04 40438a66](https://github.com/zoumo/codex-sdk-go/commit/40438a66bc4b6539ac97f993d704dae4684a45f8),
followed by this ticket 05 publication.

| Ticket | Accepted scope | Evidence boundary |
| --- | --- | --- |
| 01 — direct observation | Live successful spawn evidence, derived creation/association, unchanged native stream and zero child RPC | Public API synthetic stdio/WS/WSS contracts; no live attach proof |
| 02 — recursive routing | Ancestor ordering, nested relations, conflicts/cycles, bounded unresolved delivery and narrow terminal identity dedup | E2 fixtures and race; no reliable cursor or cross-epoch exactly-once |
| 03 — lifecycle/history | Independent parent/child states, local observation lifetime, history Associated, disconnect permanent-gap/new epochs | E2 fixtures and race; no cold-thread attachment or seamless recovery |
| 04 — bounds/authority | Atomic bounded admission, independent control routing, original request handles, source-separated answer/thinking/usage | E2 fixtures and race; no auto-approval, result synthesis or cost total |
| 05 — publication | Per-capability matrix, consistent inventory/provenance, reproducible offline example and final full gates | E1/E2 only; exact hosted delivery result reported separately |

Acceptance applies to this scoped offline contract. Native server auto-attach
remains best-effort; older/unknown server versions are unverified. No automatic
resume/read/list/start recovery is performed. Disconnection exposes permanent-gap;
history never invents Created or current work state. Parent idle, parent
turn/completed and child completion are distinct; agent identity survives new
runs. Exact Raw and the original request answer authority remain intact. ACP
subagent proposals remain draft design references, not a stable standard or a
wire protocol implemented by this SDK.
