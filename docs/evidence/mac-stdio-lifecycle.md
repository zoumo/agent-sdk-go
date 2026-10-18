# Mac live ticket 01: stdio lifecycle and history

On 2026-10-18 UTC, the real stdio lifecycle test passed in 22.24 seconds.
This evidence applies to SDK implementation commit
`c3842e2516e8caf383447379557b3bc8702e530a`, with the opt-in harness added
by the commit containing this report. No SDK implementation or protocol changed.

## Environment and toolchain

| Item | Observed value |
| --- | --- |
| OS / architecture | Darwin / arm64 |
| CLI executable | `/Users/jim/.bun/bin/codex` |
| CLI version | `codex-cli 0.162.1` |
| SDK protocol source target | `0.160.1`, revision `d27764b82f7118f674371e6d6e76271d9d606edb` |
| Go | `go1.26.9 darwin/arm64`, managed by mise |
| mise | `2026.5.3 macos-arm64` |
| Initial local main and remote main | `c3842e2516e8caf383447379557b3bc8702e530a` |
| Initial worktree | Clean; no repository or ancestor AGENTS.md or repository `.agents/skills` |

The project configuration was inspected, then trusted under the user's existing
authorization. It declares only Go 1.26.9 and `GOTOOLCHAIN=local`. Only
`mise install go@1.26.9` was run. No Java, Node or Rust installation was requested.
The inherited global configuration uses an Aliyun Go mirror and
`go_skip_checksum=true`; that configuration was not changed. Before SDK testing,
the official archive was downloaded independently and checked against the Go
release metadata, then its extracted contents were compared recursively with
the mise installation. The comparison had no differences.

Official archive: `go1.26.9.darwin-arm64.tar.gz`, 64675343 bytes.
SHA-256: `f9bb7c0a02506c5d9bf0d1eb1f7ee6c7684f844ae49558308a0427b830e022cc`.
Metadata source: `https://go.dev/dl/?mode=json&include=all`.
Archive source: `https://dl.google.com/go/go1.26.9.darwin-arm64.tar.gz`.
The attempted `.tar.gz.sha256` URL returned HTML and was not used as checksum evidence.

Project mise.toml SHA-256:
`8ad88218c480621fa7031dd04c53665a1a673c7a27a161af3845c94fe7852220`.
Global config SHA-256:
`a2b6692afd44cb12e1f1a932881c26034794b2b0fd3584d9432d8cdc664dde2e`.
No material configuration changes were made; mise trust state and the installed
Go toolchain are the authorized local changes outside Git.

## Reproduction and isolation

Run from the repository, explicitly opting into real account/model use:

```sh
rtk proxy sh -ec 'test "$(mise exec go@1.26.9 -- go env GOVERSION)" = go1.26.9; RUN_MAC_STDIO_LIVE=1 MAC_LIVE_CODEX=/Users/jim/.bun/bin/codex mise exec go@1.26.9 -- go test -v -count=1 -timeout=10m ./tests/live'
```

The executed command redirected its safe test output to
`/tmp/ticket01-stdio-sanitized.txt` before displaying it. That file contains only
allowlisted assertions, counts and timing; no raw wire or child stderr was
captured. Child stderr is drained through SDK redaction. Normal login was
inherited without opening, copying or displaying credentials. The harness uses
a fresh `t.TempDir()` for child and thread cwd, a read-only sandbox and a durable
new thread. It does not enumerate existing threads or access business files.
Temporary cwd cleanup belongs to Go testing; the synthetic Codex history was
retained, with no deletion of user state.

RPC calls retain the SDK's 30-second limit, each turn observation has a
120-second limit, and the test has a 10-minute outer limit. Close retains the
default 2-second shutdown limit and has a 5-second observation window.
There is no automatic replay, reconnect, or extra prompt loop. An unexpected
reverse request is rejected and makes the scenario fail.

## Observed acceptance results

Native IDs were compared exactly in memory. Public evidence aliases the thread
as A and its turns as T1/T2; actual IDs and payloads are not published.

| Scenario | Result and evidence |
| --- | --- |
| First initialize / initialized | Passed: NewStdio returned after matching initialize response and initialized write; ServerInfo user agent nonempty |
| Durable thread/start | Passed: nonempty native A, ephemeral=false, fresh temporary cwd |
| T1 turn/start | Passed: matching RPC response, nonempty native T1; subscription active before dispatch |
| T1 events | Passed: matched `turn/started` → `item/completed` agentMessage → `turn/completed`, status `completed`; deltas and completed agent text both exactly `SDK_MAC_LIVE_OK` |
| First history read | Passed: A, exactly T1, completed status and synthetic agent marker |
| First connection close | Passed: Close=nil, Done closed, Err=ErrClosed, elapsed 258 ms |
| Fresh connection initialize | Passed: independent NewStdio after first Close |
| Disk history before resume | Passed: fresh connection ReadThread returned A/T1 and marker |
| Same-ID thread/resume | Passed: ResumeThread returned A; no StartThread fallback |
| T2 contextual continuation | Passed: prompt asked for the prior assistant marker without repeating it; exact marker returned, matching T2 events and completed status |
| Final history read | Passed: A with exactly T1 and T2, both completed and both containing synthetic agent marker |
| Local subscriptions | Passed: each Close=nil, Done closed and Err=nil; no observed overflow, decode error or terminal stream error |
| Native thread/unsubscribe | Passed: status `unsubscribed` after final read |
| Second connection close | Passed: Close=nil, Done closed, Err=ErrClosed, elapsed 326 ms |
| Owned child exit | Passed through public Close contract: nil Close means SDK reaping/cleanup completed; no private process inspection or unrelated process termination |
| Reverse requests | None received; concurrent consumer active; unexpected request count 0 |

Unknown notifications were allowed and were not interpreted as failures or as
evidence of extra capabilities. The listed order concerns asserted typed events,
with agent deltas also checked; this is not a complete wire transcript.

The first prompt was: `Do not use tools or access files. Reply with exactly SDK_MAC_LIVE_OK and nothing else.`
The continuation was: `Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.`

No SDK defect was observed. Approval interactions, interruption, WebSocket,
WSS/public deployment and actual execution of tools are not covered by this
ticket. CLI 0.160.1 itself was not tested; this does not establish a version range.

## Budget and checks

Exactly **2 turn/start attempts** were dispatched and both responded. No failed,
uncertain or replayed write was observed. The four-ticket shared budget has
**7 of 9 attempts remaining**. Do not rerun this live test within that budget.
Ordinary tests skip it unless `RUN_MAC_STDIO_LIVE=1` is explicitly set.

Relevant validation commands (without the live opt-in):

```sh
rtk proxy mise exec go@1.26.9 -- go test -count=1 ./...
rtk proxy mise exec go@1.26.9 -- go test -race -count=1 ./...
rtk proxy mise exec go@1.26.9 -- go vet ./...
rtk proxy mise exec go@1.26.9 -- go mod verify
rtk proxy uv run --no-project docs/protocol/verify_inventory.py
rtk git diff --check
```

The first unmodified `go test -count=1 ./...` failed in the existing
`TestExplicitCommandEnvironmentAndDirectory`: `t.TempDir()` used `/var/...`,
while the child reported the equivalent physical `/private/var/...` path.
The test compares these paths as strings. The subsequent unit and race commands
used `TMPDIR="$(cd "$TMPDIR" && pwd -P)"` as a process-local prefix, leaving
the directory and persistent configuration unchanged. This is a separate test
portability issue, not a stdio live failure. Suggested independent repair ticket:
compare canonical directory identities in that existing test, while preserving
its argument, environment and cwd assertions. No repair was included here.

Final checks passed: unit/mock suite (root package 24.980 s), race suite (root
package 135.888 s), vet, `go mod verify` (`all modules verified`), protocol
inventory (170 client requests, 11 server requests, 85 server notifications,
1 client notification, 23 controls), and whitespace checks. Both ordinary suites
skipped this live test. No additional model requests were made by these checks.

The delivery commit is the commit containing this report. Exact pushed SHA and
the hosted CI result are recorded in the execution handoff, avoiding a
self-referential commit identifier in the committed file.
