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

Unverified: real model-created complex histories, native pagination of those
histories and actual model continuation. OPENAI_API_KEY, ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN were absent;
personal config/credentials/history were not read to compensate. Existing E2E
tests retain their explicit opt-in skips; a skipped test is not a real pass.
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
