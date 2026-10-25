# Ticket03: Claude independent metadata and discovery

Actual base: `bfce634d75044b07c0cedce030d2862ab25f292d` on
`zoumo/agent-sdk-go`, tree `b9afdf6055ac65c14bd7321fcd3cb4b2c1bafef0`.
The parent verified this is tree-equivalent to original ticket02 result
`0f45a01df10996f0ceb4cdcde304d36603e2ec3b`; fetched baseline was checked locally.
Only ticket03 is implemented; no assumption that ticket01 is present.

The original Library handoff failed twice using the official materialization
helper (no local file existed or hash could be verified). Work resumed only after
explicit parent authorization to use the published, precise ticket02 Git base.
The old `/workspace/codex-sdk-go` checkout remained unmodified.

Contract: [metadata](../../harness/contracts/claude-session-metadata.md).
Scope/native references/differences: [guide](../../claude/docs/session-metadata.md).
Public acceptance: CommonStoriesSuite native metadata adapter in
`tests/stories/history_query_test.go` and private checks in `acceptance_test.go`.
Existing message/live-approval coexistence case now also invokes list and info,
asserting unchanged peer trace, continuing live delivery and original authority.
The source-integrity injection extends the existing focused test only.

## Development evidence

TDD began with the public list → info → messages test failing compilation for
missing APIs. The initial focused run used the wrong suite regexp and reported
no tests; it was corrected to `TestCommonStories/TestClaudeSessionMetadata`, which
actually passed. Later red regressions drove sidechain-file filtering, native
lastPrompt/summary priority and system/command prompt selection, head/tail
provenance, exact discovery-entry boundary, typed visibility flags and incomplete
tail detection. The initial bounded-middle fixture unintentionally put corrupt
complete lines inside both sample windows; it was corrected to place one corrupt
long line across the unread middle, while still asserting message query failure.
No implementation failure was hidden by weakening an assertion or skipping a test.

All Go commands use mise-managed Go 1.26.9, isolated writable tool/cache paths
under `/tmp/ticket03-tools`. Synthetic fixtures stay in temporary directories;
no CLI/model, real private session, Mac or new recording was accessed.

## Validation

Validation and review outcomes are recorded after the final checks below.
Real model execution and macOS execution remain unverified in this Linux slice.
Source/sample stability is best effort and pages are not atomic snapshots.
