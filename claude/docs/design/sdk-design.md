# Claude native Session implementation

Normative design: [Session module](../../../harness/modules/native-session/design.md),
[interfaces](../../../harness/modules/native-session/interfaces.md) and
[delivery](../../../harness/contracts/event-delivery.md).

The independent claude package owns native NDJSON parsing, one subprocess,
serialized writes, control correlation and original permission state. Session
owns one fixed observed main identity, one FIFO, bounded membership/parent
knowledge and bounded diagnostic quarantine. The process interaction is private;
there is no public Client, callback worker pool, managedTree or second queue.

Open starts readers before matched initialize and requests native forwarding.
Failed Open returns no consumer and aborts its unreachable FIFO. Load returns a
consumable pending Session, so failure preserves accepted startup facts and the
failed load boundary for drain. Ordinary Open with ResumeID starts a fresh process
and passes the literal native resume ID without replay. It never claims successful
Load. Explicit Load requires separate native confirmation and source integrity;
the pinned baseline reports ErrResumeUnconfirmed without a prompt or fake success.

Submit preserves native text/structured input in the fixed main envelope.
Written means a complete local write. SetModel/SetThinking keep requested and
observed configuration distinct. Native results remain events and do not close
Session. ListSubagents inspects explicit native record scope without execution
or membership side effects.

Receive delivers native facts in receive order, without UUID/entity suppression,
ancestry waiting or invented run state. Parent evidence is optional. Unknown
or foreign envelopes stay diagnostic with explicit retention and coverage gaps.
Native Event values keep Raw, unknown JSON and field presence; SDK observations
have separate Received discriminators.

Actual can_use_tool controls enter the same FIFO with their original opaque
handle. The bounded ID registry prevents duplicate/retired IDs from granting a
second response. Native cancellation and termination invalidate authority even
if Receive is idle. Local invalid input and cancellation before authorization are
NotSent and preserve live authority. Started ambiguous writes are OutcomeUnknown
and never retried. Only unsupported native controls have an independent bounded
protocol-error responder; it never chooses a permission decision.

Close stops admission, invalidates owned authority and performs bounded process
cleanup without waiting for consumers or caller UI code. Accepted facts drain
to EOF or the first terminal cause; Abort additionally discards backlog. Native
stdout/stderr draining, complete frame writes, exit diagnostics, cancellation and
POSIX process-group cleanup remain tested against actual isolated OS pipes.
No real CLI/model execution or runtime compatibility certification is implied.
See [compatibility](../compatibility.md), [source pins](../type-sources.md) and
[Session migration](../session-migration.md).
