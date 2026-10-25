# Ticket 02: Claude bounded local messages

Base: bd557d91b98ab4db5e2b968b125fba6d583f791b (`zoumo/agent-sdk-go`).
Scope: only ticket 02. No metadata/list API, Codex change, Load lifecycle or
Session authority change. Existing old codex-sdk-go checkout was read-only.

API/selection/coverage contract:
[Claude messages](../../harness/contracts/claude-session-messages.md).
Native evidence and strict differences:
[local query guide](../../claude/docs/session-messages.md).
Shared public seam: CommonStoriesSuite's Claude query adapter calls
GetSessionMessages, with temporary ConfigDir/project and synthetic JSONL.
Focused integrity test only injects between-read source changes.

TDD evidence: initial public story failed to compile because the query API did
not exist; minimal implementation made it pass. Additional failing metadata
identity and side-terminal/main-ancestor cases drove identity validation and
native fallback selection corrections. No assertion was weakened or skipped.
The environment needed an isolated mise installation; initial generic mise URLs
returned HTTP 403/404, then pinned mise 2026.10.9 installed successfully. All Go
commands use mise-managed Go 1.26.9 with caches under /tmp/history-tools.

## Checks

Reviewed code revision: 7ebb2e6d45a8bd6218eae7dacea07a7cba992a2f.
All checks below used mise-managed Go 1.26.9 on Linux.

| Check | Result |
|---|---|
| Public focused query/integrity tests | Passed |
| `mise exec -- bash scripts/check-offline.sh` | Passed after review fixes; full offline Go tests, module verification, dependencies/boundaries, 417 generated types/783 synthetic cases, protocol inventory, sanitization/recording hashes/transcript checks, checkout consumer and all five offline examples |
| `GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout=120s ./claude ./tests/stories` | Passed after review fixes |
| `mise exec -- go vet ./...` | Passed after review fixes |
| `GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout=300s ./...` | Passed against reviewed code, all packages; real E2E gated/skipped |
| `git diff --check` | Passed |
| Real Agent/model E2E | Explicitly disabled/skipped |
| New-SHA external fetched-module consumer | Not executed: unpublished local SHA |
| macOS execution | Not executed in this Linux container |

The first full-race invocation overlapped the deliberate red regression fixture
for null tool IDs and failed that then-unfixed public case. It is retained as a
failed development check, not classified as a baseline failure or a final pass.
The final full-race run against the fixed revision passed all packages.
No failing assertion was weakened or skipped.

Review against base bd557d9 ran the implement skill's code-review Standards and
Spec agents independently. Initial findings: misplaced private acceptance helpers,
redundant identity validation, unnecessary repeated answered-set copying, and null
tool IDs incorrectly coerced to empty identities. All were corrected; public
regressions cover null identity and recovered timestamp ordering. Both axes
rechecked 7ebb2e6 and reported zero outstanding findings.

Fixed Python sessions.py SHA-256:
`5faa6011b0559bb2ccbe0e54ec9dea029418001da04013a67c6d107ebd192419`.
No official SDK implementation is a production dependency; the adapted algorithm
retains upstream MIT attribution in claude/NOTICE.

No real Agent/model E2E, private history access, new recording, push, PR or
external issue was performed. New-SHA fetched-module consumer is unverified:
the local change is intentionally unpublished; the checkout consumer is a
different check. Linux validation cannot certify macOS execution.

Consistency remains best effort: complete means validated declared source,
not all native history or a filesystem transaction; no cross-page snapshot.
Only immediate main transcript project layout is supported, without worktree
expansion or a running-state claim. Native corrupt/absent behaviors are
intentionally stricter, as recorded in the guide and synthetic fixtures.
