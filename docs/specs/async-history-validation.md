# History loading validation

2026-10-21, SDK branch `main`, resume-and-replay baseline `7878e36`. See the
[API and acceptance spec](async-history.md). No push, new worktree, dependency
change, personal history access or new recording was performed.

Passed with mise Go 1.26.9:

- Public Load tests for both SDKs: native resume before replay, same-session
  continued input, pending-input rejection, ordinary resume without SDK replay,
  startup/identity/cwd failures, late response and unconfirmed ownership gates,
  close/release errors, native metadata append acceptance and foreign metadata/
  conversation append rejection. Also original full-turn/native JSON,
  ordering, first record, tool relations, chain/branch/compaction, duplicate and
  malformed evidence, missing identity/parent/message, limits, late failure,
  actual page-two blocked RPC cancellation, slow sends, Client/handle shutdown,
  FIFO rejection and two-session isolation.
- Full `scripts/check-offline.sh`: dependency boundaries, module integrity,
  pinned protocol inventory, reproducible generation, 767 generated schema
  round-trip cases, sanitizer/fixture checks, all package tests and offline
  examples/consumer. The default Python 3.9 lacked `tomllib`; rerunning with an
  isolated PATH selecting installed Python 3.13 passed. No repository workaround.
- `GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...`.
- `mise exec -- go vet ./...`.
- Independent code-review Standards and Spec axes; all findings fixed and
  re-reviewed with no remaining findings. Regression tests first reproduced
  wrong Claude terminal selection, missing fields and missing graph-error raw.

Native capability and isolated integration inspection:

- Installed Codex 0.163.0 exported its experimental JSON schemas successfully in
  an owned temporary HOME/CODEX_HOME; both turns/items list parameter and response
  schemas exist. Bindings still use the existing exact 0.162.1 upstream blob.
- Installed Claude reports 2.1.258. The official Python session-reader reference
  at `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab` was inspected for transcript format
  and terminal/parent selection; SHA256 of `_internal/sessions.py` is
  `d5323963daf711c8bcc684057fd48d0cb786ee04d8329be6dec82444ec505662`.
  No implementation or fixture is copied or used as a runtime bridge.
- Installed Claude 2.1.258 plus the current Go SDK passed native resume
  initialization and asynchronous replay of two owned synthetic transcript
  records, then closed/reaped the child. No prompt/model/tools were invoked.
  Missing native resume ID fails initialization. Native resume appended atis-latch
  and mode metadata; the SDK permits only known metadata tails and verifies the
  entire original prefix SHA256 unchanged. This is synthetic-source/native-CLI
  integration evidence, not real model-created history or model continuation.

## Local real-agent Load acceptance

2026-10-21 03:08–03:11 Asia/Shanghai; SDK source
`01878244f930daa6d7397e46f45ad5267076fde0`, mise Go 1.26.9. Explicitly
requested real-agent execution; default/offline tests remain unchanged.
Installed Codex 0.163.0 and Claude 2.1.258 both reported logged in through their
native CLIs. The probe inherited that login without extracting or copying
credentials. It read only sessions newly created for this validation in owned
temporary working directories. Native history stayed in the CLI-selected data
home; this was not an isolated-HOME authentication test. No published wire
recording or dependency/source change was made.

Passed through the public SDKs, using real model responses:

- Codex: created a persistent read-only thread, completed two distinct marker
  turns, closed/reaped the first client, and opened a fresh client. Load rejected
  input before completion, returned exactly those two completed native turn IDs
  in order with raw full-turn data using PageSize=1, and reported the same native
  thread ID. Canceling the successful load context did not stop the connection.
  A third real turn recalled both earlier markers without receiving their values
  again; final native history contained exactly the three expected turn IDs.
  Loaded attachment release and resumed-client Close/Done succeeded.
- Claude: completed two real marker turns, closed/reaped the first client, and
  resumed through Load. Client access was rejected while pending. All five
  retained main-chain JSONL records (including a separate thinking record) were
  replayed byte-for-byte in native order with unique UUIDs and matching session
  identity. After load-context cancellation, a third real model response recalled
  both markers; both the result and observed native session ID matched the
  original. LoadedSession.Close closed/reaped the resumed child; Client access
  after Close was rejected. A separate four-record real session also passed
  continuation, but the five-record source comparison is the stronger evidence.

Probe corrections: the initial Claude assertion incorrectly assumed two turns
always produce four records; the corrected check compares every retained native
source record instead. Initial Codex attempts used macOS `/var` and
`/private/var` aliases inconsistently and were rejected by the existing exact cwd
checks before replay. A fresh complete run with canonical cwd from creation
through Load passed. Callers must supply the same native absolute cwd; Load does
not resolve arbitrary remote path aliases. No SDK implementation fix was needed.
The final local driver is `/tmp/sdk-load-real.go`; it is a temporary validation
artifact, not a committed fixture or default test.

Still unverified: real tool-heavy/compacted/branched/subagent histories,
large-history pagination, cancellation during active real work and MASS's full
pipeline. These successful text-only real runs do not establish those scenarios.
Existing E2E tests retain their explicit opt-in skips; a skipped test is not a
real pass.

External fetched-commit consumer/CI checks were not run because pushing is outside
scope. No full MASS adapter → Service → EventStore → Broadcast execution is claimed.

Limits: quiescent external conversation writers are required; Codex paging is not an atomic
snapshot. Claude reads only a regular Unix local transcript with an exact project
key of at most 200 characters and the selected retained main chain. It rejects
unsupported metadata/layouts instead of claiming completeness and does not load
standalone subagent files. Native compaction, missing token deltas and truncated
outputs cannot be reversed. Explicit size/count/index limits fail rather than
silently truncating. The load context controls startup/replay; canceling it after success leaves the
resumed session usable. Close releases the attachment/owned child. Unknown Codex
resume ownership remains blocked until the original Client is closed.
