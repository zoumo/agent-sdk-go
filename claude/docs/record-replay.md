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

The example and integration test use `Open -> Submit -> Session.Receive(Received)` against a re-executed fake process with a replacement HOME/environment. The installed CLI, account, network and live fallback are never used. A poison CLI sentinel, native Raw comparison, dynamic initialize ID, complete-frame marker and deadline verify this. The example consumes the native idle **after** the error result and explicitly closes.

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

`claude/cmd/record` has no CI invocation. **Every real launch currently fails
closed**, including direct proxy dispatch, before any version/help executable,
configuration/auth lookup or capture directory creation. `recorder.Preflight`
returns separate managed-policy-isolation and independent financial/token/
generation/retry-enforcement blockers. No environment assertion or supplied
boolean can satisfy these gates. `--safe-mode`, `disableAllHooks` and the observed
hook guard cannot establish pre-launch isolation or disable mandatory policy.

Inspect the static plan without starting a real agent:

```sh
go run ./claude/cmd/record --help
go run ./claude/cmd/record --mode preflight  # expected refusal, no CLI executed
```

The manual launcher retains CI, explicit `record|e2e`,
`SDK_REAL_AGENT_E2E=1`, absolute CLI, exact approved SHA256, source revision and
new private-directory guards. It validates the exact 2.1.258 version string and
required flag tokens only **after** independent preflight can pass; 2.1.310 is
not accepted. This ticket has not run a native probe or established compatibility
of another binary. No operator-supplied version claim proves native behavior.

The offline-tested root plans additionally implement:

- **R1:** Ready/Done ordinary inputs in the same Session, one SetModel(default)
  and SetThinking(clear) between them; preserve native receipts/rejections and
  leave `Observed` unasserted. Requires `--synthetic-persistence` and a newly
  owned HOME/ConfigDir/project/tmp. It omits `--no-session-persistence` only for
  this explicit mode. Other modes forbid persistence. Creation model/settings
  and actual native persistence still require the #12 approved run table.
- **R2:** exact Read once and Write deny once, then one original
  AskUserQuestion with `Which synthetic color?` (Blue/Green) and
  `Enter a short label.` (sample/other), answered Blue/sample. Hold the first
  original request while a clear-thinking control and Receive progress; a
  missing barrier stops without retry. Denial is not write success.
- **R3:** on a native root stream-event barrier submit the one extra input and
  Interrupt once. No barrier means untriggered; no replacement turn. A receipt
  does not assert an interrupted terminal outcome or steer semantics.
- **R4:** use existing ListSessions/GetSessionInfo/GetSessionMessages with only
  R1's explicit isolated ConfigDir/project. No launcher, resume, default-history
  scan or independent child API is added. Saved content is not a wire-event log.

`recorder.Drive` operates an already opened native Session and owns cleanup;
its caller must reserve the ledger before Open. Offline regressions use only
re-executed synthetic mock peers and public native Session/query APIs. Plans
are tooling support, not successful real captures.

One canonical private aggregate ledger (`attempts.jsonl`, 0600 in 0700) is shared
across tickets. Atomic crash-preserving locks, append + file/directory fsync and
strict bounded parsing retain every root reservation, turn dispatch intent and
cleanup-inclusive outcome. A reservation charges the scene's entire turn
allowance before startup; every Submit adds its durable dispatch intent. At most
three root reservations and six reserved turns fit one ledger. Each scene is
single-use; failed/unfinished attempts block all later scenes. Torn/unknown
records, legacy `.attempt` files, unsafe paths and stale crash locks refuse
rather than reset/migrate/refund. The coordinator must reconcile historical
spent ledgers; a new path never grants new authority.

Every scene retains a 120-second deadline including initialization (30 seconds)
and cleanup (3 seconds), 2 MiB wire and observations, 256 KiB stderr; no scene
retry/resume/child/extra turn. These bounds cannot independently enforce provider
requests, retries, tokens or money. The [#12 handoff](../../docs/validation/issue-10.md)
records requested cumulative ceilings and remaining gates.

Recognized operator-provided `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and
`ANTHROPIC_BASE_URL` are passed opaquely to an isolated environment only after
launch authority exists. Values are never inspected, copied from personal or
platform configuration, logged or repaired. Their presence is not usable-auth
proof. The original minimal-environment auth failure says nothing about current
account availability.

Publication requires explicit 2.1.258 source identity matching private status and
native init fields. The publisher uses a frozen per-version key allowlist and
rejects unknown wrappers, duplicate JSON keys and unreviewed versions before
creating output. ReplayRecording rejects other source versions and mismatched
native init versions; replay rejects unknown wrapper fields while preserving
unknown native fields exactly. Unknown publication data remains quarantined,
not ignored. Originals and manifest hashes are unchanged.

## Source revision for new recordings

The recorder reads Go build VCS metadata only when the main module is the SDK before attempting a recording. A clean
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
