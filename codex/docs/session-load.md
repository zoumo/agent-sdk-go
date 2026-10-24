# Explicit Load into a Session

`codex.Load(ctx, codex.LoadOptions{Client: client, HistoryOptions: options})`
returns a consumable Session after reserving the known main identity, before
source reads, resume confirmation and replay finish. Alternatively supply Stdio
instead of Client for an owned process. An ID, absolute cwd and CodexHome are
required. The connection's CodexHome and stored identity must match.

Use Session.Receive for historical Events, live notifications, actual native
IncomingRequests and SDK Load boundaries. History keeps Event.History.Raw and
MissingFields; Received.Raw stays empty for projections. Unknown statuses stay
native Turn values without an invented notification method. This path uses one
Session FIFO, with one page/record at a time in the history producer. The FIFO
can grow to available memory when consumption is slow.

LoadState reports not-requested for Open, or pending/succeeded/failed for Load.
Success requires matching Thread.ID, Thread.Cwd and response Cwd from the actual
thread/resume response, full retained main-history admission and final integrity
checks. History is not delivered before that response is validated. A later
thread/read cannot replace resume confirmation. LoadState.ResumeRaw and
ResumeError.Raw preserve actual result evidence, including malformed results.
Errors classify with ErrIdentityMismatch or ErrResumeUnconfirmed; an observed
wrong identity is retained without setting successful Confirmation. Completion does not terminate the
Session or mean the application has drained the FIFO. Failure has a boundary,
terminates interaction and leaves accepted facts drainable. Close reports cleanup
separately; failed or unknown cleanup never reopens an uncertain identity.

Submit, SetModel and SetThinking reject pending/failed loads without sending.
Real request answers, explicit-turn Interrupt and Close remain usable during
replay. Load's context owns startup/replay only; cancellation after success does
not stop the Session. Pending cancellation/Close ends owner waits without
canceling an already started write on a shared connection. No automatic retry,
new-thread fallback, extra prompt or tool execution is used.

Stable persisted main history only: active/ephemeral sources, repeated IDs,
summary items, cyclic/nonadvancing cursors, resource exhaustion and observed
source mutation fail explicitly. Metadata is checked across resume and replay.
Pagination is non-atomic and requires external writers to be quiescent. Allowing
live interleaving is not a promise of lossless active-execution recovery, original
cross-source timing, missing deltas, historical approval authority or child-history
replay. Existing limits and native raw content remain authoritative.

## Shared native attachments and request replay

The fixed native baseline is Codex 0.162.1, commit
`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`. Owning-source inspection used the cached
files already fetched for the repository's fixed-baseline research; no native
process, model, recording or upstream test was executed:

- [thread_state.rs, lines 528–560 and 572–619](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/thread_state.rs#L528-L619): add inserts the connection ID in sets; repeated attachment is not an independent native subscription. Unsubscribe removes that connection from the thread.
- [thread_lifecycle.rs, lines 715–836](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/request_processors/thread_lifecycle.rs#L715-L836): warm resume adds the connection before the response, then replays pending server requests. Some errors can occur after adding the connection.
- [outgoing_message.rs, lines 446–465 and 580–600](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server/src/outgoing_message.rs#L446-L465): pending replay resends original request IDs; user-verification requests have separate connection ownership and are excluded from ordinary pending replay.

A main identity still has one Session owner. An explicitly loaded child can also
remain in its ancestor's already confirmed observation scope. Session attachment
references track both observers; closing the child cannot unsubscribe that
ancestor's coverage. The last observer releases physical attachments acquired by
Open/Load. No automatic resume is used to compensate for missing observation.
Membership alone does not claim that native forwarding covers all child traffic.

Real request authority is assigned once at first reception. A new child request
prefers its direct owner; replay of an existing pending request preserves its
original authority. Duplicate delivery does not duplicate effective answers or
transfer a canceled handle. Typed ResumeThread, StartTurn and UnsubscribeThread
cannot bypass coordinated Session ownership or uncertain/releasing attachment
fences. Generic raw Call remains an explicit uncoordinated extension.

The public synthetic regressions in session_load_test.go cover confirmation,
provenance, independent lifetime/drain, interleaving/questions/controls, late
failure, cancellation, unknown dispatch fences, and overlapping observers with
unique answers and close isolation. Existing history/protocol/permission tests
remain. These are offline protocol checks, not real resume or complete native
observation certification. The independent Client.Load/LoadedThread lifecycle has been removed. Package
Load and Session.Receive own admission, replay, continued interaction and
attachment cleanup. Both admission orders for overlapping child/ancestor ranges
share attachment references; final release does not depend on discovery order.


Contract: [async history](../../harness/contracts/async-history.md),
[event delivery](../../harness/contracts/event-delivery.md), and
[Session interface](../../harness/modules/native-session/interfaces.md).
