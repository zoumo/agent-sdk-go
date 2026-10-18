# Mac live ticket 02: interactions and interrupt observation

On 2026-10-18 UTC, one real CLI run completed in 63.70 seconds (`go test`
package time 64.160 s, exit 0). This is **partial live coverage**: user input and
a synthetic command effect passed; three approval kinds and interruption did
not trigger. The Go test passing means the bounded observation completed
without detected transport/decode/stream errors, not that every capability passed.

## Environment and implementation

- Base/main SDK SHA: `cd5ed954e880ae468f56cdbe1b42122f216a1127`.
  Local and authoritative remote main matched before editing; worktree was clean.
- Prerequisite CI independently read back: `39085256828`, that exact SHA,
  completed/success.
- Darwin 25.2.0 arm64; CLI `/path/to/codex`, `codex-cli 0.162.1`.
- mise-selected `go1.26.9 darwin/arm64`, explicitly asserted before live dispatch.
  Ticket 01 records official archive verification; no reinstall here.
- Protocol target remains 0.160.1 at
  `d27764b82f7118f674371e6d6e76271d9d606edb`; CLI 0.160.1 was not run.
- Executed harness: `tests/live/interactions_interrupt_test.go`, SHA-256
  `d0f52167e806ad3e50b435e8ea89caa121c591bb6bdebc8adaad688052ceb4d2`.
  No SDK implementation, generated protocol or compatibility matrix changed.
- No repository/ancestor AGENTS.md or repository `.agents/skills` was found.
  Global Codex AGENTS.md and RTK.md were read; global `.agents/skills` had no
  relevant additional workflow. Ticket 03/04 and Claude were not executed.

A transient execution-service disconnect occurred before this harness was
written or any ticket 02 model request was dispatched. It recovered without
an app-server retry or model budget charge; it was not an SDK failure.

## Isolation and command

Existing CLI login was inherited. No credentials, private history or business
files were opened, copied or displayed. Child and five new ephemeral threads
used one fresh empty `t.TempDir()` cwd and an explicit **read-only** sandbox.
Every thread/start returned inherited approval policy **never**. No safety
settings or approval policies were weakened to induce events. Only synthetic
local tasks were requested. Native thread/turn/question/tagged request IDs were
compared in memory and excluded from public output.

The separate opt-in avoids repeating ticket 01:

```sh
rtk proxy sh -ec 'test "$(mise exec go@1.26.9 -- go env GOVERSION)" = go1.26.9; RUN_MAC_INTERACTIONS_LIVE=1 MAC_LIVE_CODEX=/path/to/codex mise exec go@1.26.9 -- go test -v -count=1 -run "^TestStdioInteractionsInterrupt$" -timeout=10m ./tests/live'
```

Do not rerun within the completed budget. The actual command redirected safe
output to `/tmp/ticket02-interactions-sanitized.txt` and displayed it on completion.
No raw wire or child stderr transcript was captured. Initialization had a 60 s
bound, ordinary RPCs the SDK 30 s default, each turn observation 120 s, ticket
10 minutes, and Close a 5 s observation window over the SDK shutdown default.
Subscriptions preceded dispatch. StartTurn ran asynchronously while events and
reverse requests were consumed. There was no write replay or extra prompt loop.

## Capabilities versus live behavior

The installed CLI exported experimental schemas into a temporary directory via
`codex app-server generate-json-schema --experimental --out DIR`. All four modern
reverse-request schemas were present. SDK Data handlers and response types cover
all four; raw Params and exact tagged ID access also exist. This is representation
evidence, not a claim of available model tools or successful runtime approval.

Real initialize declared `experimentalApi=true`. The read-only
`collaborationMode/list` RPC responded and advertised `plan`. Only the user-input
turn selected that supported per-turn mode, using the returned thread model and
built-in instructions. No global mode or model setting changed. Runtime
availability of the three approval tools was not independently established.

| Scenario | Observed evidence | Result and limit |
| --- | --- | --- |
| Command approval T1 | Prompt requested `printf SDK_T02_COMMAND`. Native turn matched; completed command output was exactly the marker with exit code 0; turn completed. Approval callbacks: 0. | **Approval untriggered**. Harmless command effect **actually passed**, independently of assistant text. Inherited `never` was retained. |
| File approval T2 | Prompt requested apply_patch creating only `ticket02-marker.txt` with `SDK_T02_FILE` plus newline. Turn completed, approval callbacks 0, no completed file-change item observed. | **Approval untriggered**. Read-only sandbox and `never` constrain this scenario. A read-only check of only the test-owned temporary marker while this run still owned its cwd found it absent. No write-effect pass. |
| Permissions approval T3 | Prompt requested request_permissions for already-readable cwd only, or UNAVAILABLE if absent; prohibited network, writes, external paths and extra rights. Turn completed; callbacks 0. | **Untriggered**. Model tool availability was not independently established. No permission expansion was requested or granted. |
| User input T4 | Plan mode; one typed `item/tool/requestUserInput`, matching thread/turn, one non-secret question, exactly BLUE/RED options. One Answer mapped its native question ID to BLUE. Local handle ended ErrRequestAnswered. One serverRequest/resolved matched both thread and exact tagged request ID. Agent result was exactly BLUE; same turn completed. | **Actual pass** for this request/answer workflow. Answer success alone was not treated as server acceptance; resolution, exact subsequent result and terminal event provided further evidence. |
| Interrupt T5 | Prompt requested `sleep 20`. Same native turn started; a command item completed and the turn completed normally. No item/started command satisfied the exact `sleep 20` string guard; no InterruptTurn RPC was dispatched. | **Untriggered**. Ack=false, interrupted terminal=false, matched stopped-item=false. Collected output cannot distinguish a wrapped command string from another cause of the unmet guard. No early natural completion or OS process termination claim. |

The approval-response branches remain **not live covered**: command/file decline,
empty turn-scoped permissions response, and rejection of unmatched/unknown
requests. The harness grants no writes or additional rights. No approval was
manufactured and no raw request payload was published.

Observed user-input order (aliases T4 and R stand for exact native IDs):

```text
turn/start response -> turn/started (same T4)
item/tool/requestUserInput (same thread/T4, tagged R)
Answer once: native question -> BLUE; local ErrRequestAnswered
serverRequest/resolved (same thread, exact tagged R)
item/completed agentMessage: BLUE (checked in memory)
turn/completed: completed (same thread/T4)
```

The agent item was checked in memory rather than printed as a separate log line;
`blue_exact=true` was printed. Unknown notifications were allowed, so this is not
a complete wire transcript. T3 turn/started preceded its StartTurn response and
was correctly correlated. Each subscription Close succeeded with Err=nil; no
decode error, stream loss or overflow was observed. Final client Close=nil,
Done closed, Err=ErrClosed, elapsed 147 ms, confirming owned-child cleanup through
the public Close contract. Arbitrary descendant cleanup is not claimed.

## Budget and follow-up

Exactly **5 turn/start attempts**, all with matching responses, no retries.
Together with ticket 01: **7/9 used; 2/9 remaining**, reserved for ticket 03.
No SDK defect or actual SDK test failure was observed. The unmet interrupt
command guard is a **harness/coverage limitation**, not evidence of an SDK
InterruptTurn defect. A separately authorized follow-up requires better safe
command-item discrimination and its own model budget before interruption can
be certified. This run does not justify weakening settings to induce approvals.
Legacy approvals, pending-approval interruption and OS-level sleep termination
are not covered. No ticket 03/04 or Claude work was performed.

A documentation-write shell quoting error occurred after live execution. It
produced malformed package-argument errors before any Go test binary could run;
it was corrected by a literal heredoc. These were artifact-generation failures,
not SDK failures, and dispatched no additional model turns.

## Offline checks

Both live opt-ins were unset for ordinary suites. The existing macOS `/var`
versus `/private/var` comparison issue was avoided using only a process-local
canonical TMPDIR; no unrelated test repair was made.

```sh
rtk proxy sh -ec 'export TMPDIR="$(cd "$TMPDIR" && pwd -P)"; mise exec go@1.26.9 -- go test -count=1 ./...'
rtk proxy sh -ec 'export TMPDIR="$(cd "$TMPDIR" && pwd -P)"; mise exec go@1.26.9 -- go test -race -count=1 ./...'
rtk proxy mise exec go@1.26.9 -- go vet ./...
rtk proxy mise exec go@1.26.9 -- go mod verify
rtk proxy uv run --no-project docs/protocol/verify_inventory.py
rtk git diff --check
```

Unit/mock suite passed (root package 24.806 s); vet and module verification passed.
Full race suite passed (root package 134.906 s); final whitespace check passed.
Inventory passed: 170 client requests, 11 server requests, 85 server notifications,
1 client notification and 23 controls. Pushed commit and exact hosted CI results
are included in the execution handoff. No protocol
regeneration or live capability-matrix promotion was performed.
