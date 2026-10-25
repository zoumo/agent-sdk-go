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

Reviewed implementation revision: `f222ab3f85fc54c286cb3a696cdb02183b331355`.
Thread/source delegation: `01a15713-e031-76d5-abd3-ebec1796c0c6`, independent ticket03.

| Check (mise-managed Go 1.26.9, Linux) | Final result |
|---|---|
| Public metadata/messages/live-coexistence and focused source-integrity checks | Passed |
| `mise exec -- bash scripts/check-offline.sh` | Passed after review fixes; full offline suite, module verification, dependency boundaries, generated bindings/tests, protocol inventory, sanitizer/hash checks, checkout consumer and six offline examples |
| `GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout=300s ./...` | Passed after review fixes, every package; real E2E gated/skipped |
| Focused query/message/source-integrity race | Passed after review fixes |
| `mise exec -- go vet ./...` | Passed after review fixes |
| `git diff --check` | Passed |
| New-SHA fetched-module consumer | Run after authorized branch publication; exact revision/result supplied in handoff |
| Real Agent/model and macOS execution | Not executed |

The first full offline run failed in unchanged Codex
`TestServerRequestLifecycleSuite/TestConcurrentAnswerAndNativeResolutionHaveOneOutcome`:
not-sent/0 responses expected, outcome-unknown/1 response observed. The first full
race run also failed the same shared Codex assertion through wss, plus Claude
`TestSessionPermissionConcurrentAnswersAndClose/close` with a client-closed
outcome-unknown cleanup error. These are retained failed checks, not counted as
passes. On an isolated worktree at the precise base `bfce634d…`, the Codex direct
case (`go test ./codex -run TestServerRequestLifecycleSuite/TestConcurrentAnswerAndNativeResolutionHaveOneOutcome -count=100`)
and Claude case (`go test -race ./claude -run TestSessionPermissionConcurrentAnswersAndClose -count=100`)
both reproduced the same respective failures. No approval implementation/test
assertion was modified. The final full offline and full race runs passed; the
baseline intermittent approval races remain a separate known limitation.

The implement skill's independent Standards/Spec agents reviewed against the
actual base. Initial findings: metadata provenance inferred from matching content
instead of source position (both axes), and duplicated bounded range reader
(Standards judgement). A new public nested-object fixture reproduced wrong head
facts and sidechain exclusion. The parser now carries head-record provenance
from byte offsets; message and metadata reads share one bounded range reader.
Both axes re-reviewed `f222ab3…`: **Standards 0, Spec 0 outstanding findings**.
No findings were dismissed by weakening assertions.

Fixed Python sessions.py SHA-256 recomputed locally:
`5faa6011b0559bb2ccbe0e54ec9dea029418001da04013a67c6d107ebd192419`.
Official npm tarball SHA-512 matched the guide's fixed integrity. Static source
inspection is distinct from synthetic public acceptance or real CLI evidence.
The new fetched-module check also compiles and runs the metadata example, using
only temporary synthetic storage and the exact published revision.

Publication is restricted to `phase2/claude-metadata`, with the integrated 02
commit as ancestor. No main merge, force push, PR or external issue is authorized
by this ticket. The parent owns integration, ticket04 and real-model acceptance.
