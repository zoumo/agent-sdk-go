# Recorded wire replay

The checked-in sample contains no successful model/tool transcript. One real CLI
process attempted the read/write story (S1); its replacement environment had no
usable authentication. The CLI returned `authentication_failed` and zero native usage/cost. This does not establish that the normal interactive CLI lacks authentication. No credentials were inspected or changed, no CLI was upgraded, and the attempt was not retried.

The published sample is a **real CLI authentication failure**, not model-generated output. S2 was not attempted after the authentication failure. S3 was not attempted because count/depth/child-tool restrictions were not proven enforceable. The minimum successful recording criterion remains blocked. Existing synthetic child/permission tests remain synthetic evidence.

## Offline use

```sh
CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 go test -count=1 -timeout 180s ./...
CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 go run ./examples/recorded-replay
python3 scripts/check-transcripts.py
```

The example and integration test use `Open -> Send -> Receive(ReceivedEvent)` against a re-executed fake process with a replacement HOME/environment. The installed CLI, account, network and live fallback are never used. A poison CLI sentinel, native Raw comparison, dynamic initialize ID, complete-frame marker and deadline verify this. The example consumes the native idle **after** the error result and explicitly closes.

The peer replays captured direction/order dependencies without sleeps. It supports multiple in-flight controls and notification-before-response, binds dynamic client request IDs with string/number type preservation, validates reverse requests/replies exactly once, and fails on extra/wrong/duplicate requests, missing IDs, early EOF or pending controls. It deliberately checks recorded request order; it is not an arbitrary scheduling emulator. Test callers bound its lifetime. Tool IDs and other server-owned native IDs remain the consistent sanitized fixture IDs. Native byte order is retained except for dynamic control response ID substitution.

## Evidence and scope

[Manifest](../tests/testdata/recordings/real-sanitized/s1-auth-blocked/manifest.json), [wire](../tests/testdata/recordings/real-sanitized/s1-auth-blocked/wire.jsonl), [ID relations](../tests/testdata/recordings/real-sanitized/s1-auth-blocked/id-relations.json).

| Story | Real attempts | Children | Seconds | Result |
| --- | ---: | ---: | ---: | --- |
| S1 | 1 | 0 | 2.241 | authentication failure; 11 captured frames; offline replay passes |
| S2 | 0 | 0 | — | stopped after S1 authentication failure |
| S3 | 0 | 0 | — | enforceable child isolation not established |

One root attempt is conservatively charged to the budget even though native telemetry reports no API generation. Native input/output usage and cost are zero; independent billing verification is unavailable. No successful local tool, permission answer, child, streaming delta or background boundary was observed. No child spawning or additional turns occurred. SDK Close succeeded and the recorder exited; owned processes were checked separately after capture.

Frames 1/4 correlate initialize, requesting `forwardSubagentText:true`. Acceptance is not proof of forwarding. Frames 2/3 are actual SessionStart hook events despite `--safe-mode`, `--restricted` and explicit `disableAllHooks`: the CLI help exempts admin-managed policy from safe-mode. The source/configuration of this hook was not inspected. Its captured output/stdout/stderr were empty. The initial recorder allowed the turn to proceed; the hardened recorder now rejects startup hooks before forwarding initialize completion/user input. A hook may already have executed before its event is observable: this guard cannot establish a hook-free launch. Future recording remains blocked until policy isolation is independently established.

Frame 9 is a native synthetic authentication-error assistant, frame 10 has `subtype:success` **and** `is_error:true`, `terminal_reason:api_error`; frame 11 is idle. Always inspect error fields, not only subtype. This refines the successful-path diagrams in [event sequences](event-sequences.md); neither idle nor initialize acceptance proves successful model work.

## Publication boundary

Private raw wire, derived observations and stderr were created outside the repository with directory/file modes 0700/0600. The publisher validates a reviewed field allowlist, preserves absent/null and structure, maps IDs consistently, replaces the synthetic root/runtime socket/PID, resets timestamps to a neutral epoch while retaining offsets, checks each ID occurrence/equality class, then scans token/header/home/email canaries. Ambiguous values shared across ID namespaces are quarantined rather than merged. Unknown fields or sensitive free text fail before creating a public directory. The allowlist is specific to this CLI/sample, not a general-purpose safe JSON sanitizer. No original ID mapping or environment values are published. Public Raw means sanitized wire bytes. Protocol/control have separate channel tags and one ordinal stream; stderr and derived expectations remain separate.

`expected-observations.json` contains offline assertions, not invented recorded model/tool events. Additional malformed/control fixtures have `provenance=synthetic` under [synthetic-faults](../tests/testdata/synthetic-faults/README.md). Existing tree terminal/lineage/bounds and permission cancellation tests cover parent-result/late-child, unknown frames, EOF, cancellation and resource limits; none are counted as live observations.

## Recording scenarios

The recorder supports fixed synthetic stories, not arbitrary prompts:

- **S1 (read/write):** read `numbers.txt` containing `7\n11\n`, then write
  `summary.txt` containing `total=18\n`. Only Read/Write are exposed. Approval
  requires the exact canonical paths, no symlinks and exact write content, with
  at most two approvals. Completion requires both approvals and the output file.
- **S2 (question):** one AskUserQuestion asks for Blue or Green. The original
  matching handle answers Blue once; other requests are denied.
- **S3 (subagents):** unsupported by the recorder. Child count, depth and tool
  restrictions are not proven enforceable; prompt instructions alone do not
  establish isolation. Late child events remain synthetic coverage.

S1/S2 receive through the root result to native idle, subject to the deadline.
No fixed model wording or delta count is assumed. The recorded S1 error sample
establishes neither story's successful path.

## Explicit recording/E2E entry point

The manual `claude/cmd/record` is separate from all tests/examples and has no CI invocation. It refuses any nonempty `CI`, requires both `--mode record|e2e` and `SDK_REAL_AGENT_E2E=1`, an absolute approved CLI path, an absolute new private directory and an absolute durable `--session-ledger`. Reuse one ledger for the authorized recording session: each supported S1/S2 story is reserved exactly once before startup, with no retry, resume or extra turn; S3 is not implemented. Guard refusal is tested offline. The initialization probe’s `CLAUDE_E2E` flag cannot enable it.

From the repository root, inspect the flags without starting a real agent:

```sh
go run ./claude/cmd/record --help
```

An explicitly authorized future recording uses the same entry point:

```sh
SDK_REAL_AGENT_E2E=1 go run ./claude/cmd/record \
  --mode record --story s1 --cli /absolute/path/to/pinned/native/claude \
  --private-dir /absolute/new-private-recording \
  --session-ledger /absolute/durable-session-ledger
```

Each supported story has a 120-second deadline including initialization, 30-second initialization bound, 3-second SDK shutdown, 2 MiB combined wire and 256 KiB stderr cap. There are at most two supported 120-second stories per ledger (less than the authorized 480-second ceiling). A deadline/limit aborts the process and marks failure. Read/Write and AskUserQuestion have exact synthetic policies; S1 completion requires two approvals and the exact output file, S2 requires one matching original question answered Blue. Unknown/outside/symlink paths are denied. The CLI restricted tool sandbox provides the directory boundary; the callback alone cannot constrain auto-approved tools, and the ask-policy behavior was **not** exercised by this failed attempt.

Approved version is 2.1.258. The future entry point passes existing recognized authentication environment variables opaquely rather than inspecting/logging their values; the first attempt had retained only HOME/PATH/TMPDIR. No change was used for a second attempt. The explicit mode is not proof of a successful E2E run. Fixing authentication or managed-hook isolation is outside the recording workflow; do not rerun automatically. The public workflow keeps all live flags zero, uses no model secrets, and only runs offline replay/example/scans.

## Source revision for new recordings

The recorder reads Go build VCS metadata before attempting a recording. A clean
build records its full revision with `sdk_revision_source=go-build-vcs` and
`sdk_modified=false`; a modified checkout is rejected. When VCS metadata is absent,
`-sdk-revision FULL_SHA` is required and is recorded as
`caller-supplied-unverified`, with `sdk_modified=null`. A supplied revision that
conflicts with build metadata is rejected. This is source evidence, not proof of
native compatibility. Existing capture manifests keep their original revisions.
The pure offline `TestRecordingRevisionEvidence` checks these cases without
starting any CLI. Recording remains explicitly opt-in and is never run by the
default tests. Request policy now consumes real `Session.Receive` requests;
answer and cleanup failures remain observable without replacing the original
run error.
