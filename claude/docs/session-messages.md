# Independent local saved-message queries

Contract: [Claude local messages](../../harness/contracts/claude-session-messages.md).
Public acceptance: [message-query scenarios](../../harness/contracts/stories/claude-session-messages.md).

```go
page, err := claude.GetSessionMessages(ctx, sessionID, claude.SessionMessagesOptions{
    Scope: claude.SessionQueryScope{
        ConfigDir: "/explicit/config", // required; no implicit HOME lookup
        ProjectDir: "/absolute/project", // optional, existing project directory
    },
    Offset: 0,
    Limit: 100,
})
// Check err before using messages; inspect page.Coverage for the actual scope.
```

No CLI, Client, Open, resume or Session is required. The independent read neither
changes active Sessions nor creates requests that can be answered. It returns
saved user/assistant content, including tool blocks and compact summaries. Message
and Raw are caller-owned JSON bytes; Raw preserves source fields, unknown data,
large numbers and optional-field distinctions. This is not complete event replay.

The backend is explicitly local ConfigDir/projects/<project-key>/<UUID>.jsonl.
ProjectDir is canonicalized through the existing filesystem, then encoded using
the native non-alphanumeric replacement rule. Truncated (>200 character) project
keys are unsupported. Without ProjectDir, the immediate project directories are
searched completely under a discovery-entry limit. Duplicate locations report
ambiguous rather than picking directory iteration order. No worktree expansion,
git process, programmatic-store adapter, old layout, subagent message query,
SessionStore, system inclusion or Managed Agents backend is implemented.
Programmatic transcripts in this same main-session layout are readable.
Official list default worktree discovery is broader than this scope.

Zero Limit means **1000 messages**, after visible-chain selection. MaxMessages
(default 10000) caps a requested page; raising it is explicit. Other zero defaults:
64 MiB source, 100000 lines, 100000 indexed records, 8 MiB scanner line buffer,
10000 discovery entries. Metadata/blank lines count toward lines; hidden branches
count toward indexed records. All bounds and offset/limit must be nonnegative.
There is no native total/cursor/nextCursor. Every call reads and validates the
entire bounded source; offset/limit are content slicing, not I/O pagination.
A valid empty source and an offset beyond the selected visible messages succeed
with an empty slice. Return-limit exhaustion is an error, not silent truncation
beyond the explicitly requested slice.

Use `errors.As(err, &queryError)` for `*claude.SessionQueryError`, with Kind:
invalid-input, missing, inaccessible, corrupt, unsupported, ambiguous,
resource-limit or changed. OS causes are retained for errors.Is, while cancellation
returns the context error. No messages are returned on any error. Coverage is
unknown before source selection, partial after source selection if reading fails,
and complete only for a fully checked declared source. It is never a statement
about every native layout or an atomic snapshot. Raw error material may be private.

On validated Unix platforms, directory handles are pinned beneath os.Root and
in-tree symlinks are rejected; files must be regular, opened nonblocking/no-follow.
Other platforms return unsupported for the existing local-file opening primitive.
At completion the full source hash/length/mtime and root-relative identity are
rechecked, detecting observed append/rewrite/replacement, including parent directory
replacement. No transaction lock is acquired: writes after checks, changes fully
restored between checks, and rename of the explicit config root itself can evade
checks. Separate pages can reflect different versions. Retry is caller policy.

## Native evidence and deliberate strict differences

The implementation was checked against official Python SDK **0.2.174**, commit
[`0e89b298e771259378a6cd3734e00d323dff5703`, sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py),
particularly `_build_conversation_chain`, `_recover_orphaned_parallel_tool_results`,
`_is_visible_message` and `get_session_messages`. The source was obtained by git
at that exact commit. The parallel reconstruction algorithm is adapted in Go;
its MIT notice is retained in [NOTICE](../NOTICE).

The algorithm selects the last main leaf by file position, follows parentUuid,
ignores logicalParentUuid, keeps compact summaries and recovers response siblings
and their results. Strays need still-unanswered unambiguous same-writer calls;
unknown/missing agent identity is distinct from the empty string. Native stable
sorting applies only to recovered siblings/results, not the selected chain.
Filtering sidechain/team/meta happens before offset/limit. The upstream fallback
when there are no main leaves is retained, followed by visibility filtering.

Python may return [] for invalid UUID, missing sources and no visible content,
skip malformed lines, let duplicate UUID overwrite earlier entries, or stop at
missing parents/cycles. Go distinguishes invalid/missing/corrupt, validates the
entire indexed graph (even abandoned branches), rejects duplicate keys/UUIDs,
invalid Unicode, incomplete final lines and typed identity/flag/timestamp errors,
and rejects unknown entry types. This also rejects an orphan tool result whose
parent UUID is absent, even when the native recovery algorithm could associate
its tool_use_id; an existing unrelated parent is supported for stray recovery.
These are intentional strict coverage differences, covered by synthetic fixtures.
Known transcript metadata is ignored for content after identity validation.
Independent queries also recognize api-request-shape, api-request-blob, api-request
and cost-state as non-message bookkeeping. They need no uuid/parentUuid and do
not enter the conversation graph, but still undergo strict JSON and optional
sessionId/session_id validation and count toward source byte/line/line-size bounds.
Unknown types remain unsupported. This query-only classification does not expand
the older replay parser or its frozen-tail append allowlist. The exact official
filter evidence and synthetic regression are recorded in the
[compatibility follow-up](../../docs/validation/history-query-integration.md#compatibility-follow-up-non-message-request-records).

Tests are synthetic public API acceptance in `tests/stories/history_query_test.go`
inside CommonStoriesSuite (private adapters/assertions in acceptance_test.go),
with isolated temporary config, read-only files,
hash/permission assertions, missing runtime PATH, chain/parallel/compaction faults,
limits, cancellation, scope isolation and coexistence with original live approvals.
Focused `claude/session_query_integrity_test.go` injects changes at the final
verification seam that cannot be timed reliably through filesystem public calls.
No real private transcript, CLI, model or new recording is used. These tests do
not certify a future CLI layout or successful live model behavior.

Run the [self-contained offline example](../examples/session-messages-offline/main.go):

```sh
mise exec -- go run ./claude/examples/session-messages-offline
```

Actual checks and remaining limits are recorded in
[the ticket validation report](../../docs/validation/claude-session-messages.md).
