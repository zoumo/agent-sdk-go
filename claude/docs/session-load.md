# Claude Session Load (T11)

Implements the reachable failure path of the [Session Interface](../../harness/modules/native-session/interfaces.md)
and [async-history contract](../../harness/contracts/async-history.md). Source
limits are fixed in [native evidence](../../docs/research/claude-session-native-evidence.md),
sections 2 and 6: TS SDK 0.3.258 / CLI 2.1.258.

`Load` now returns `*Session`, without an independent replay channel or a later
Client handoff. The Session owns its single FIFO, startup cancellation, load
outcome, native interaction and cleanup. Before successful load, ordinary Submit
and configuration are rejected locally with NotSent; real IncomingRequest
handles still use the original permission registry. Interrupt does not wait for
initialize or Receive once the process is published. Close can cancel even
before publication and waits for owned cleanup, never consumption.

This fixed baseline has **no established no-input local resume confirmation**.
Initialize does not prove recovery. Load validates/indexes the bounded transcript,
starts the exact requested resume/data-home process, and fails after initialize
with `ErrResumeUnconfirmed`. It sends no user input, shouldQuery probe, guessed
control, or version upgrade. Even an unsolicited matching system/init frame is
an observed identity, not proof of restored retained history. Actual wrong root
identity instead preserves `IdentityMismatchError`, its native Raw, requested and
observed IDs. Native control refusal, startup cancellation/timeout, history errors
and transport failure retain their separate causes. No history is admitted and
normal input never becomes available on this baseline.

Failure/Close settles LoadState independently of drain and accepts exactly one
ReceivedLoad failed boundary before closing the FIFO. Open reports not-requested
and emits no load boundary. Native events and requests accepted before failure
remain ahead of the boundary. Session.Err keeps the first interaction cause;
Close cleanup cannot replace it. Explicit early Close may have nil Session.Err
and EOF after drain while LoadState.Err explains that loading was closed.
Boundary and query snapshots detach mutable native error/Raw diagnostics.

The offline history reader/projection remains intact and independently tested
through a private test harness. That harness neither starts a process nor makes
public Load succeed. It preserves main-chain/branch selection, compaction,
tool-use/result and unknown blocks, exact source Raw, SHA256 prefix/record checks,
mutation/append detection, resource limits, cancellation and Unix FIFO safety.
Known metadata cannot bypass supplied cwd/session identity checks in the frozen
prefix or appended tail. The four extra non-chain names permission-mode,
ai-title, file-history-delta and cost-state remain rejected: chain exclusion
alone proves neither full schema validity nor append safety.

Offline peers establish only Go lifecycle/control behavior, never native recovery
compatibility. Success-dependent history/live interleaving, slow replay controls,
partial replay through public Load, successful completion boundaries and
post-success context lifetime cannot be validated until the fixed native
confirmation gap is resolved. No synthetic success flag or public bypass exists.
No real CLI/Agent/model execution, recording, push or release was performed.
