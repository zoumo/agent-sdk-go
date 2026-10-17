# Final live smoke attempt

## Exact scope

- SDK revision: `a83ab64915e0bb7e7290d74ca3ac0204d11ec61c`.
- Installed official executable reported `codex-cli 0.159.2`.
- SDK protocol source target remains `0.160.1` at
  `d27764b82f7118f674371e6d6e76271d9d606edb`; this attempt does not establish
  compatibility with either version or a version range.
- Harness compiled and ran with mise-managed `go1.26.9 linux/amd64`.
- One stdio construction attempt, with a 90-second context bound. No retry,
  alternate authentication, credential inspection, or model turn occurred.

## Scenario and command

An opt-in, untracked Go harness used the public `NewStdio` API against the
installed official binary with arguments `app-server --stdio`, an empty temporary
working directory, and normal inherited CLI authentication. The planned sequence
was initialization, ephemeral `StartThread`, subscription before `StartTurn`,
exact agent-message and thread/turn-ID checks, and bounded close. The prompt was:

> Reply exactly SDK_LIVE_OK. Do not use tools, read files, or execute commands.

Incoming reverse requests would be rejected with an RPC error; none could be
observed because construction failed. The harness was invoked from the SDK module
using `mise exec go@1.26.9 -- go run ./.scratch/final-live` after loading the existing
mise environment. The harness and private output are intentionally not committed.

## Result

**Blocked before initialization completed.** The SDK returned:

```text
RESULT failed: initialize: codex: initialize (outcome-unknown): EOF
exit status 1
```

`NewStdio` returned no client. No `thread/start` or `turn/start` was sent by the
harness. The child connection ended during initialization. The SDK's bounded,
redacted diagnostics did not expose the raw child stderr, so a separate bounded
startup diagnostic launched the same official binary and arguments in another
empty temporary working directory. It held stdin open, sent **zero protocol
messages**, and observed process exit code 1 within 10 seconds, with zero stdout.
The sanitized fatal diagnostic was:

```text
Error: failed to initialize sqlite state runtime under <private-path>: failed to initialize state runtime at <private-path>
```

The process also warned that PATH aliases could not be created because of a
read-only filesystem (OS error 30). The fatal diagnostic establishes an installed
CLI state-runtime startup blocker independent of SDK framing. Its deeper cause
was not investigated through restricted state directories. This is not evidence
of an authentication or model-network failure. No state paths, permission settings,
Codex home, or authentication configuration were changed. Raw startup stderr was
kept private outside the repository.

The outcome-unknown initialize request was not replayed. WebSocket transport,
live turn execution, native ephemeral-thread behavior, durable resume, reverse
request workflows, and successful close/cleanup behavior remain unverified by
this attempt. No production files were changed, and no live capability matrix
cell was promoted to passing evidence.
