# Independent child saved-content queries

`claude.ListSubagents(ctx, parentSessionID, ListSubagentsOptions)` and
`claude.GetSubagentMessages(ctx, parentSessionID, agentID, SubagentMessagesOptions)`
are independent package functions. They read only an explicitly supplied absolute
`Scope.ConfigDir`, optionally narrowed by an absolute `Scope.ProjectDir`. They
never create a Client/Session, use HOME/environment defaults, run git/CLI/model,
resume, subscribe, submit, modify live membership or create answer handles.
An existing Session with the same identity need not be idle.

The [native contract](../../harness/contracts/claude-subagent-history.md) fixes all
signatures, defaults, error kinds and consistency limits. The parent must be a
native session UUID; an agent is a case-sensitive UTF-8 path component, **not** a
UUID. Only the unique selected parent's adjacent `<parent>/subagents/` tree is
supported, including ordinary nested workflow directories. Directory nesting does
not prove agent ancestry. No worktree/cloud/alpha-store/legacy layout parity is
claimed. Empty main files are allowed; the selected main source must exist and
pass full bounded syntax/available-identity validation.

Selection follows the pinned official Python SDK
[`_build_subagent_chain`](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1474):
index chain entries, choose the last user/assistant in file order, walk
`parentUuid` to the root, reverse, retain user/assistant, then slice. This does
not reuse main history compaction, logical-parent, sidechain/meta/team filtering,
response deduplication or parallel tool-result recovery. Stored content/Raw is
not the original wire delta/control log.

List validates all candidate contents/sidecars, sorts by path bytes and slices.
Get completes the bounded inventory to prove uniqueness, then reads only the
main source, selected child and optional sidecar. Missing sidecar is successful:
parent facts stay nil and running stays `unknown`. Present sidecar bytes,
unknown content and large JSON numbers remain exact, with detached caller-owned
slices/pointers. Sidecar facts never imply permission, membership or creation.

Compared with the pinned Python API, Go requires explicit scope, refuses
ambiguous parent/agent/file aliases rather than first-match selection, rejects
symlinks and nonregular nodes, and distinguishes missing/inaccessible/corrupt/
unsupported/resource-limit/changed. Duplicate keys/UUIDs, invalid UTF, incomplete
JSONL, invalid optional identity, orphan sidecars, cycles and missing chain
parents fail explicitly. List does not reconstruct child graphs; Get validates
every branch, including abandoned branches. Existing `Session.ListSubagents`
retains its separate frozen-environment and partial-discovery behavior unchanged.

Limit zero means 100 for list or 1000 for get; MaxAgents/MaxMessages zero means
10000. Offset beyond the visible end succeeds empty. These are result slices,
not I/O pagination; there is no total, cursor or snapshot token. Every call
counts initial bytes/physical lines/index records/read files/discovery entries/
directories cumulatively, including main and sidecar reads. See the contract for
all resource defaults. Errors return no records/messages/Info, with actual
unknown/partial coverage; error Raw can be private and is never printed by
`Error()`.

Before success, the implementation verifies directory entry sets/types/identity,
optional absence, and read-file identity/size/mtime/full hash. Verification reads
at most the initial byte count again; Coverage.Bytes counts initial reads only.
Observed append/rewrite/replacement and inventory/sidecar changes return changed,
with no retry. Atomic is always false: restored changes between checks and writes
after final verification cannot be ruled out; pages/calls share no snapshot.

Run the [complete synthetic browser](../examples/subagent-history-offline/main.go):

```sh
mise exec -- go run ./claude/examples/subagent-history-offline
```

Acceptance maps SDK-US-008/019 and US-014 to CLAUDE-US-005/007:

| Contract scene | Executable synthetic evidence |
|---|---|
| H1 independent/no CLI/live coexistence | CommonStoriesSuite.TestIndependentSubagentHistory in [shared suite](../../tests/stories/history_query_test.go), assertions in acceptance_test.go; same native identity/config, peer interrupt barriers, unchanged trace and original approvals |
| H2 direct/nested/identity/sidecar | [public native tests](../subagent_history_test.go): TestChildLayoutAndEmpty, TestChildHistoryFaults, TestChildQueryScopeAndCancellation |
| H3 selection/ownership | TestIndependentSubagentHistory/H1-no-session and TestChildSelectionAndOwnership; sidechain/meta/team preserved, logical parent and stray branches excluded |
| H4 bounds/errors/partial | TestChildHistoryBounds, TestChildListInventoryBoundBeforeReading, TestChildGetReadsOnlySelectedContent, TestChildQueryInaccessibleSidecar |
| H5 containment/changes | public layout tests and [FIFO rejection](../subagent_history_unix_test.go); [deterministic read-set barrier](../subagent_history_integrity_test.go) covers append/rewrite/replacement/inventory/sidecar/container changes |

All material is generated in temporary synthetic ConfigDir trees. No real or
private transcript, model/CLI execution or new recording was used. Successful
native child-file interoperability and background completion remain unverified;
#22 owns new real sanitized child evidence under separate live/publication gates.
