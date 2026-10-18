# Mac live ticket 03: authenticated loopback WebSocket lifecycle

On 2026-10-18 UTC, the real authenticated WebSocket lifecycle test passed in
10.53 seconds (package 11.228 s, exit 0). Both model turns passed. This evidence
covers only the exact local scenarios below, not a supported version range.

## Environment

- SDK base/main `b6a42a4a3746488464fc9ee19dedc8f3d9e70346`; clean worktree.
  Authoritative remote main matched before editing. Prerequisite hosted CI
  `39086088795` was read back as completed/success at this exact SHA.
- Darwin arm64; `/Users/jim/.bun/bin/codex`, `codex-cli 0.162.1`.
- mise-managed `go1.26.9 darwin/arm64`, asserted before live execution.
  Ticket 01 records official archive/hash verification; no reinstall here.
- SDK protocol target remains 0.160.1 at
  `d27764b82f7118f674371e6d6e76271d9d606edb`. CLI 0.160.1 was not run.
- Harness `tests/live/websocket_lifecycle_test.go`, executed SHA-256
  `f68e2d257531d34befa76c04e3f1ae84d4a281e85cc7e02eb45e13fc70c4594b`.
  No SDK implementation, generated protocol or compatibility inventory changed.
- Read spec, issue 03, planning README and mise supplement, global Codex
  AGENTS.md/RTK.md and Mac ticket 01/02 reports. No applicable repository or
  ancestor AGENTS.md was found. Ticket 04 and Claude were not executed.

## Isolation

One newly owned loopback service per invocation used this template:

```text
codex app-server --listen ws://127.0.0.1:<ephemeral-port>
  --ws-auth capability-token --ws-token-file <owned-temporary-path>
```

A fresh cryptographically random 256-bit test token was stored in a 0600 file
outside the model cwd. Only its pathname appeared in argv. SDK BearerToken
received it in memory; no token appeared in URL, output or source. No persistent
credential registration was required. Cleanup deleted the token and verified
absence. Go testing removed the temporary directories. No long-term auth or
network security configuration changed.

Normal CLI login was inherited by this new service. The harness did not open,
copy or display user credentials, business files or private history; no existing
threads were enumerated. The model used a fresh empty temporary cwd and explicit
read-only sandbox. Newly created synthetic durable history was retained. No
existing daemon was connected, restarted or shut down.

NewRemote explicitly supplied initialize parameters; AllowInsecureWS stayed
false, with no TLS verification override. PID/PPID-only inspection selected this
launch tree, and lsof confirmed exactly one TCP listener at the requested
127.0.0.1 address. Other process arguments/environments were not inspected.
Incorrect synthetic credentials received HTTP 401 before model dispatch.

## Actual live results

Native IDs were compared exactly in memory and excluded from output. A aliases
the new thread, T1/T2 its two native turns.

| Scenario | Result |
| --- | --- |
| Owned authenticated listener | Passed: exact loopback endpoint and one listener; wrong bearer rejected with 401 |
| First NewRemote initialize | Passed: initialize/initialized exchange; nonempty ServerInfo user agent |
| Persistent thread/start | Passed: nonempty A; ephemeral=false, empty cwd, requested read-only sandbox |
| T1 | Passed: matching RPC ID, turn/started, completed agent item, turn/completed completed; deltas and agent message exactly SDK_MAC_WS_OK |
| First ReadThread | Passed: A, exactly T1, completed status and exact agent marker |
| First client Close | Passed: nil, Done closed, ErrClosed, 0 ms at millisecond resolution |
| Ownership after Close | Passed: original owned process alive; fresh authenticated NewRemote initialized on same listener |
| New-connection history | Passed: A/T1 and exact marker before ResumeThread |
| ResumeThread | Passed: same A; no fallback StartThread |
| T2 contextual continuation | Passed: prompt omitted marker; exact marker returned with matching T2 and completed terminal |
| Final history | Passed: A, exactly T1/T2, both completed with marker |
| Native unsubscribe | Passed: unsubscribed |
| Second client Close | Passed: nil, Done closed, ErrClosed, 0 ms; owned listener remained TCP-connectable |
| Subscriptions | Passed: active before dispatch; Close=nil, Done closed, Err=nil; no observed decode error, overflow or stream loss |
| Reverse requests | None; concurrent rejection consumer active, unexpected count 0 |
| Owned service cleanup | Passed: SIGTERM isolated owned group, launcher Wait completed, process group absent, temporary token removed |

Each asserted sequence was turn/started -> item/completed agentMessage ->
turn/completed completed, with exact deltas checked too. Unknown notifications
were allowed; this is not a complete wire transcript.

First prompt: `Do not use tools or access files. Reply with exactly SDK_MAC_WS_OK and nothing else.`
Continuation: `Do not use tools or access files. Return exactly the marker from your previous assistant reply, with no additional text.`

## Budget, bounds and initial harness limitation

Exactly **2 turn/start attempts**, both responded, no replay or uncertain write.
With ticket 01 using 2 and ticket 02 using 5: **9/9 used, 0 remaining**.
Do not rerun live opt-ins within that budget. Ticket 02 approvals and interrupt
remain untriggered; its BLUE answer/resolution pass remains unchanged.

An initial invocation stopped at launcher-only lsof inspection before any
thread or turn: the launcher itself did not hold the listener. Its Wait
completed after SIGTERM and its temporary token was removed. The harness was
corrected to inspect only that launch tree and use an isolated owned process
group for bounded cleanup. A final process lookup narrowly matched to this
harness invocation shape found no remaining test server. This was a harness
failure, not an SDK defect; only the successful invocation supports live claims.

A helper uv cache restriction was resolved with a temporary cache. An initial
sandboxed remote check could not reach the inherited local proxy; elevated
readback succeeded without changing proxy/network configuration. A report-write
shell quoting error caused malformed Go package-argument errors before any test
binary ran; the report was rewritten through a literal heredoc. None of these
steps dispatched an additional model request.

NewRemote had a 60 s outer context and the SDK default 30 s call limit; ordinary
RPCs retained 30 s. Each turn observation had 120 s, with at most one explicit
interrupt on timeout (not needed); test outer limit 10 minutes. Socket Close
had a 5 s observation window over SDK defaults. Process cleanup used 3 s before
kill, 2 s reaping, then a 2 s group-absence check. No daemon-shutdown RPC was
used. The second client was the single explicitly planned fresh connection.

Live command (safe output redirected to `/tmp/ticket03-websocket-sanitized.txt`;
no raw wire or child stderr retained):

```sh
rtk proxy sh -ec 'test "$(mise exec go@1.26.9 -- go env GOVERSION)" = go1.26.9; RUN_MAC_WEBSOCKET_LIVE=1 MAC_LIVE_CODEX=/Users/jim/.bun/bin/codex mise exec go@1.26.9 -- go test -v -count=1 -run "^TestWebSocketLifecycleHistory$" -timeout=10m ./tests/live'
```

## Limits and ticket 04 handoff

All ticket-defined local WS lifecycle assertions actually passed. No target
behavior was untriggered, no current environment blocker remained, and no SDK
failure was observed in this scenario. No SDK repair ticket is indicated.

**Not covered:** live WSS/TLS, public/cross-host networking, reverse proxies,
JWT issuer/signed bearer mode, existing daemon ownership, token revocation,
crash recovery, active-turn detach/resume, approvals or interruption. Fixture
TLS results do not promote these to live passed. This verifies CLI 0.162.1
against this SDK target, not CLI 0.160.1 or a version range.

Ticket 04 should consolidate the three Mac reports, retain ticket 02 coverage
limits and the known macOS temporary-path test issue, and record exact commits
and hosted CI. It has no remaining model budget. This ticket does not implement
04, change compatibility claims, repair SDK source, or run Claude.

## Offline verification

All live opt-ins were unset:

```sh
rtk proxy sh -ec 'export TMPDIR="$(cd "$TMPDIR" && pwd -P)"; mise exec go@1.26.9 -- go test -count=1 ./...'
rtk proxy sh -ec 'export TMPDIR="$(cd "$TMPDIR" && pwd -P)"; mise exec go@1.26.9 -- go test -race -count=1 ./...'
rtk proxy mise exec go@1.26.9 -- go vet ./...
rtk proxy mise exec go@1.26.9 -- go mod verify
rtk proxy sh -ec 'UV_CACHE_DIR=/tmp/ticket03-uv-cache uv run --no-project docs/protocol/verify_inventory.py'
rtk git diff --check
```

The process-local canonical TMPDIR avoids the known /var versus /private/var
comparison in an existing test; no unrelated repair was mixed in. Vet and module
verification passed. Inventory passed: 170 client requests, 11 server requests,
85 server notifications, 1 client notification, 23 controls. No protocol
regeneration was needed. Final suite outcomes and exact pushed commit/hosted CI
are recorded in the execution handoff.

Normal suite passed (root package 23.710 s; protocol and live package compilation
also passed with opt-ins skipped). The successful live run ended with
`turn_start_attempts=2 remaining_global_budget=0 unexpected_requests=0`, then
confirmed owned process-group absence and temporary-token removal.
Full race suite passed (root package 136.490 s; protocol 3.407 s; live opt-ins
skipped, package 3.424 s). All local required checks completed successfully.
