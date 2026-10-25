# Zero-model fault and lifecycle companions

Issue [#13](https://github.com/zoumo/agent-sdk-go/issues/13), parent
[#6](https://github.com/zoumo/agent-sdk-go/issues/6). Execution base:
`52b329a77333e55063a97eaf1304c82b30a99a59` (#8 crosswalk, PR #24).
The [current Story matrix](story-coverage.md) defines the SDK-US namespace;
these companions exercise existing native public APIs, without new runtime or
recorder features. Follow the [testing](../../harness/testing.md) and
[layout](../../harness/code-layout.md) standards.

## Run each Story

From the repository root, use Go 1.26.9 via mise. No CLI, account, model, shell
execution by a peer, or live fallback is involved. The peers are the test binary,
with explicit executable paths and isolated temporary directories. Run one row
by replacing `NNN` with its three digits:

```sh
mise exec -- go test -count=1 -timeout 60s ./tests/stories -run '^TestCommonStories$/^TestSessionTargets$/^SDK-US-NNN$'
```

Every row runs both `codex` and `claude`; append `/^codex$` or `/^claude$`
to select one. Assertions live in [acceptance_test.go](../../tests/stories/acceptance_test.go),
native synthetic frames in [session_peer_test.go](../../tests/stories/session_peer_test.go).

| SDK-US | Runnable NNN | Deterministic application-visible boundary |
|---|---|---|
| SDK-US-005 | 005 | Duplicate native facts remain duplicated; original Raw, large integers, nullable/unknown fields and unknown-envelope quarantine survive. Unknown data grants no answer authority. |
| SDK-US-009 | 009 | Original native approval deny/allow with exact native extensions, two original IDs, one answer each. Full peer-observed input/control/answer order excludes automatic answers and retries. Codex dynamic tools are not native approvals. |
| SDK-US-010 | 010 | Continue Receive/control while two requests are pending; native resolution/cancel invalidates first; canceled local answer wait leaves second usable once. Late/duplicate answers send no extra bytes. Exact control/answer order is checked. |
| SDK-US-014 | 014 | Leave 256 synthetic native facts unread while control completes; explicit Close and Done precede complete FIFO drain to EOF, with every sequence checked. |
| SDK-US-015 | 015 | Pre-canceled Submit is NotSent; canceled Receive leaves Session usable. Codex settled dispatch without reply is OutcomeUnknown, with no resend. Claude active large write is canceled only after the peer reads its prefix; OutcomeUnknown/ErrUnknownOutcome and no retry remain required. |
| SDK-US-016 | 016 | Interrupt is a native control and leaves Session usable. Close ends ownership, is idempotent, rejects late controls, and Codex shared-client ownership stays separate. |
| SDK-US-017 | 017 | Native task failure does not set transport Err. Injected transport loss invalidates pending handles; all 32 accepted facts drain in exact order before the original terminal cause. Close does not overwrite that cause. |
| SDK-US-018 | 018 | Loss is reported without reconnect/resubmit. Further input fails; peer input proves one launch/initialization and one submission. Fresh connections do not restore old handles (below). |

Additional public-seam companions:

```sh
mise exec -- go test -count=1 -timeout 60s ./tests/stories -run '^TestCommonStories$/^TestFaultClosePendingRequest$'
mise exec -- go test -count=1 -timeout 60s ./tests/stories -run '^TestCommonStories$/^TestRecorded(Question)?FaultCompanion$'
mise exec -- go test -count=1 -timeout 60s ./tests/stories -run '^TestCommonStories$/^TestReplayFinalResult$'
```

`TestFaultClosePendingRequest/{codex,claude}` closes unanswered original handles,
then opens a fresh connection whose synthetic peer deliberately reuses the same
native request ID. The old handle still cannot answer; only the fresh original
handle can. Two late answer attempts after Close produce zero additional answers.
No automatic recovery or old-handle transfer is implemented.

## Real bytes versus application timing

The recorded companions use the single embedded public fixture copies:

- Codex S2 [wire](../../codex/tests/testdata/recordings/real-sanitized/s2/protocol.jsonl)
  and [manifest](../../codex/tests/testdata/recordings/real-sanitized/s2/manifest.json):
  original requestUserInput ordinal 24, Amber answer 25, resolution 26 and terminal
  45. The application answers via the original handle once, checks a duplicate
  attempt fails, then withholds Receive until strict replay completion. Every
  captured native notification is consumed in order with exact JSON values and
  original unknown payload. Native control IDs retain the existing typed mapping.
- Claude [wire](../../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/wire.jsonl)
  and [manifest](../../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/manifest.json):
  withhold Receive until the peer has emitted all captured frames; consume every
  protocol frame byte-for-byte, observe `subtype=success` plus `is_error=true`,
  then idle, explicit Close and strict EOF. This is authentication failure only.

The delayed consumers, private replay handshakes and bounded loopback completion
barrier are **synthetic application-driver evidence**, not native captured timing.
They insert/edit no native frame. The separate 256-frame bursts, cancellations,
reused request IDs, transport loss and blocked-write prefix barrier are entirely
**synthetic**, not recordings. No copy or mutation of a real capture is published.
No real slow-consumer, approval, interrupt, successful Claude generation or
application-recovery run is established by passing these tests.

Close preserves accepted queue facts; a peer's successful stdout write alone is
not proof the SDK accepted them. Recorded companions consume the native terminal
boundary before Close, then require EOF. The synthetic slow-consumer companion
uses its control reply as a protocol ingestion barrier before Close and drain.
Both recorded peers report their final matcher return value over a bounded
application result channel **after** the strict client-EOF check. Tests require
that report after Close: a local SDK EOF or process shutdown alone is insufficient.
`TestReplayFinalResult/{codex,claude}` feeds each original capture's client frames
with normal EOF and with an extra frame after the completion handshake; the
latter must report a final matching error. A mutation discarding that error must
fail the negative test.

## Timing, matching and unsupported scope

Claude's blocked-write peer reads exactly one prefix byte, signals over a bounded
loopback channel and waits for explicit release. There is no filesystem polling,
sleep-to-pass or partial-frame retry. Codex cancellation uses the public native
submitted marker before canceling the local wait. Active-write ambiguity and
settled-write/wait cancellation remain different native cases.

Existing finer-grained timing gates remain authoritative: Codex
[TestDefaultCallTimeoutWithControlledTime](../../codex/client_timeout_test.go)
checks exact default/earlier/later caller budgets using synctest; Claude
[TestSessionPermissionStartedWriteOutcomeUnknownCannotRetry](../../claude/session_requests_unix_test.go)
and its queued-cancel companion retain their Unix platform scope. These checks
are not reclassified as recorded evidence or a universal native time budget.

The [native supplements](../../harness/sdk/native-stories.md) still apply.
Unsupported input/configuration operations retain native errors (T/002 and
T/012), not invented shared wire values or model fallback. Real approval/interrupt
capture gaps remain in the matrix. No new shared pagination, control schema,
reconnection, independent child history or Phase 2 feature is introduced.

Strict negative matching, ID correlation, unknown payload equality, unfinished
requests, early/extra EOF, full-consumption and bounded-stream checks remain in
both public [Codex mock/replay](../../codex/tools/mock) and
[Claude mock/replay](../../claude/tools/mock) suites:

```sh
mise exec -- go test -count=1 ./codex/tools/mock ./codex/tools/replay ./claude/tools/mock ./claude/tools/replay
mise exec -- bash scripts/check-offline.sh
GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...
mise exec -- go vet ./...
```

The offline gate also checks generation, inventory, hashes, quarantine/sanitizer
canaries, dependency boundaries and runnable examples. Fetched consumers must
validate the precise published SHA via the [consumer gate](../testing.md#external-consumers).
No passing synthetic test or skipped live test establishes genuine live success.
