# Default call timeout test isolation

Date: 2026-10-18 UTC. Base: `7d7e78f502d826d8ba844daa04fd3b0a2afd8129`.
Toolchain: mise Go 1.26.9, Darwin/arm64, `GOTOOLCHAIN=local`.

## Failure and reproduction

GitHub run [39087884345](https://github.com/zoumo/codex-sdk-go/actions/runs/39087884345)
at that exact base failed in both attempts. The authoritative CLI run metadata
and both failed-step logs were read. Both report:

```text
client_contract_test.go:508: codex: initialize (outcome-unknown): context deadline exceeded
```

Attempt 1: test duration 0.13s, package 20.487s. Attempt 2: 0.09s,
package 20.514s. Race/vet/inventory steps were skipped after the failure.
The failing line constructs the fixture, before either intended assertion.

The unmodified focused test passed 100 local repetitions (9.170s); that does not
invalidate the two hosted failures. A temporary fixture probe inserted
`time.Sleep(100 * time.Millisecond)` before the fake peer answers `initialize`.
With that single delay, the original test failed all three repetitions at the
same initialization call, with the same OutcomeUnknown/deadline error. The
repaired frame test passed all three repetitions with the identical probe
(1.631s). The delay was then removed; it is not part of the committed fixture.

## Cause and repair

The test supplied a deliberately short 50ms `CallTimeout` to the constructor.
The real SDK correctly applies that limit to `initialize` too; the outer 3s
fixture context cannot extend it. Real process scheduling and transcript fsync,
plus socket/TLS setup in the shared remote cases, can consume that short budget.
This is test setup coupling, with no demonstrated production defect.

`TestOutgoingFrameLimit` now uses the normal startup budget and still asserts
`NotSent` plus `ErrFrameTooLarge` for an 8192-byte payload against a 4096-byte
limit. It additionally checks that the oversized call never reached the peer
and a subsequent valid echo succeeds. The same body runs for stdio, WS and WSS.

`TestDefaultCallTimeoutWithControlledTime` exercises the real `Client.Call`,
writer, pipe dispatch, pending state and response routing in a `synctest` bubble.
Its client starts ready; real initialization remains covered by the process and
socket contract tests. Reading an actual request frame then `synctest.Wait`
establishes that the write settled before advancing time. No wall-clock sleep
competes with initialization or an active write.

The three cases check a background context, a later caller deadline (1s), and an
earlier caller deadline (20ms). At 1ns before the effective deadline the call must
still be pending; exactly at 50ms/20ms it must return `DeadlineExceeded`,
`OutcomeUnknown`, and the original method/ID. They check pending/queue cleanup,
no connection termination, rejection of a stale response, fresh ID allocation,
and success of a subsequent call. Shared call machinery supplies this deadline
behavior for both transports; this controlled-time test is not a socket test.

A separate temporary production mutation doubled the real call timeout.
The background and later-caller cases both failed with
`call survived its exact deadline`; the earlier caller deadline remained bounded.
The production file was immediately restored byte-for-byte, verified with
`git diff --exit-code -- client.go`. No production source changes are delivered.
The previous lifecycle and directory-identity fixes remain intact. This was the
only `CallTimeout` configuration in `client_contract_test.go`.

The historical names/results in `docs/protocol/evidence.json` are retained at
their original revisions. The old combined test maps to the renamed shared frame
test and the new controlled-time test described here; no historical remote
measurement is relabeled as controlled-time evidence.

## Verification

All Go checks retain the inherited macOS TMPDIR (the default `/var/folders/.../T/`
path, without normalization or override). Every check explicitly sets
`RUN_REAL_CODEX_TESTS`, `RUN_MAC_STDIO_LIVE`, `RUN_MAC_INTERACTIONS_LIVE` and
`RUN_MAC_WEBSOCKET_LIVE` to `0`.

Focused final-tree checks:

- `go test -race -count=1000 -run '^TestDefaultCallTimeoutWithControlledTime$' .`
  passed (2.558s); each repetition includes all three deadline cases.
- `go test -v -count=100 -run '^(TestOutgoingFrameLimit|TestRemotePublicContractParity)$/^(ws|wss)$/^TestOutgoingFrameLimit$' .`
  passed (6.336s), with 100 executions each of stdio, WS and WSS.
- `go test -count=1 ./...` passed (root package 24.376s).

Remaining full-tree gates passed:

- `go test -race -count=1 ./...` (root package 134.255s).
- `go vet ./...`.
- `go mod download` and `go mod verify` (all modules verified).
- `python3 docs/protocol/verify_inventory.py` (170 client requests, 11 server
  requests, 85 server notifications, 1 client notification; 23 controls).
- `go test -v -count=1 ./tests/live`: all three tests explicitly SKIP.
- `git diff --check`.
No real Codex binary or model is called. No Claude, credential or global setting
is changed. This ticket adds no live compatibility claim. The exact delivery
commit and hosted CI result are recorded in the handoff and final report.
