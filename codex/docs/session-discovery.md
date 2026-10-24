# Session descendant discovery

`Session.ListSubagents(ctx, SubagentQuery)` uses `thread/list` with the Session's
fixed `ancestorThreadId`. There is no caller-selectable ancestor or parent field.
It reads stored descendants at any depth, including records that predate Open.
It never starts, resumes, subscribes, replays, answers a request, or adds a record
to observation membership. Listed status and timestamps remain native metadata;
a record is not proof of current execution, subscription, or SDK-observed creation.
The contract is [Session Interface](../../harness/modules/native-session/interfaces.md).

Initialization must opt into `experimentalApi`; known disabled capability returns
`ErrUnsupportedDiscovery` in a `CallError` with `NotSent`. Native rejection and
storage/access restrictions remain native `RPCError`/dispatch evidence. The SDK
does not retry, downgrade to parent-only discovery, or fall back to cached seeds.

## Filters and native limits

The default explicitly sends `subAgent`, `subAgentReview`, `subAgentCompact`,
`subAgentThreadSpawn`, and `subAgentOther`. Coverage.Requested retains exact
filters, including cursor/limit, providers, sorting, cwd, section/project/search,
originators, and state-DB-only selection. Unknown native values go to the server
for validation. Unsupported hosted/local filters can therefore be rejected.

The fixed owning source has a special relation-filter branch: with an ancestor,
null sourceKinds produces unrestricted sources; an explicit empty array instead
uses interactive defaults. Coverage.SourceSelection distinguishes these from an
explicit list. Requests are frozen through exact native JSON encoding, so
`Some(nil slice)` is correctly treated as wire null; a non-nil empty slice stays
`[]`. Returned Requested filters do not alias caller-owned query slices.
Omitted SDK sources use the five-kind default, not native omission.
`archived:true` selects only archived records; false, null, and omission select
only non-archived. Query both states separately for both scopes. No successful
single-state enumeration claims all Session records or future source kinds.

## Pagination and partial results

One foreground call walks native pages, including empty pages with a continuation.
`Pages` retains each whole native response, its unknown fields and backwards cursor.
No identities, parents, source variants or statuses are rewritten or inferred.
Defaults bound the walk to 128 pages, 10000 records and 16 MiB of encoded native
response JSON. MaxPages/MaxRecords/MaxBytes override these bounds; negative values
are invalid before dispatch. Native page size remains native `limit` (the fixed
local implementation clamps it to 1–100, default 25). The existing connection
frame limit also bounds a single response; decoding can transiently hold that
response in addition to the retained result budget.

- `PagesCompleted` counts decoded native RPC responses, including a page rejected
  by retention limits. `len(Pages)` counts actually retained pages.
- `EndReached` requires a retained terminal page with explicit native null cursor.
  `EnumerationComplete` additionally requires starting without a non-null cursor.
  Both refer only to the requested storage/filter scope, not an atomic snapshot,
  complete runtime observation, missing/unindexed storage, or all archive states.
- Failure returns retained pages plus an error and IncompleteReason. A resource
  error never truncates a native page into success. NextCursor remains the input
  cursor of an unretained or failed page, so callers can query that page with a
  different budget. An unset cursor means the beginning, not successful exhaustion.
- Repeated/non-advancing or empty continuation cursors return ErrDiscoveryCursor.
  Native problematic cursors remain visible; they are not safe continuation advice.
  Missing terminal-cursor evidence returns ErrProtocol with the retained response.
- Native rejection, cancellation, owner Close and connection faults retain the
  existing owner-aware RPC classifications. Canceling after dispatch can be
  OutcomeUnknown even though no execution was requested. No retry is automatic.

## Fixed evidence

Pin: Codex 0.162.1, commit `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`.
Sources and exact hashes remain in [sources.json](protocol/sources.json):

- `v2/thread.rs:1394–1465`: list filters, experimental ancestor/parent exclusivity;
  `1506–1519`: ten native source kinds; `1551–1563`: native response/cursors.
- `thread_processor.rs:2571–2596`: DescendantsOf versus DirectChildrenOf;
  `2591–2642`: native limit/storage query; `5504–5509`: relation-filter source
  special case. Inspected bytes match C1/C4 SHA256 in the
  [fixed checklist](../../docs/research/session-native-integration-checklist.md).
- Generated params/response use the same original experimental schema archive
  blob `eef430182091bfc6f120c98dda3a3246771de8d1`; the existing generator verifies
  the blob before selecting 42 roots/170 definitions. No handwritten schema or pin
  upgrade. Outputs contain 417 types and 783 generated synthetic cases.

`session_discovery_test.go` uses the public Session seam and an independently
authored synthetic native peer for nested old records, unknown metadata, filters,
empty/multiple pages, cursor faults, partial errors, limits and zero side effects.
Configuration and request regressions retain their separate contracts. This is
offline synthetic/source evidence, not real CLI querying, live storage coverage,
native runtime completeness certification, or a new recording.
