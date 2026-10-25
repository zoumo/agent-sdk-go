# Recorded native events and offline replay

The real sanitized fixtures were captured at SDK revision
`d465677969cf968c6245e0adeaf7e32c42356913`. The original capture used the public `StartThread` → `Subscribe(ctx, root, types.SubscriptionOptions{})` →
`StartTurn` flow. Its historical API names describe capture provenance. Current offline
playback uses Session and replay.RunSession with the same native frames and
original handles. Capture bytes and expected protocol order are unchanged.

## Recorded scenario scope

| Story | Synthetic input and allowed operations | Expected partial order | Limits |
| --- | --- | --- | --- |
| S1 | `numbers.txt` contains `17\n25\n`; `fixture_read` reads it once; `fixture_write` creates `summary.txt` once with `sum=42\n` | initialize, root response, subscription, turn start; tool request before its answer; item completion before root completion | One root, two operations, 120 seconds |
| S2 | Ask once for Amber or Indigo with native `request_user_input`; original handle answers Amber | root response, subscription, turn start; question before its answer; completion | One root, one interaction, 120 seconds |
| S3 | One direct child plus background output | Requires enforceable cumulative child count, depth and child tool restrictions | Not attempted: concurrent-thread limits do not enforce cumulative creation count |

S1 uses application-provided dynamic tools. It does not claim native shell or
permission-approval coverage. Native environments, shell, multi-agent tools,
Apps, MCP and plugin discovery are disabled. The dynamic handler validates exact
filenames, synthetic contents, ordering and operation count. Unknown requests
are rejected. No project history or private user repository is provided.

The historical captures used existing authentication without copying credentials.
Their hook exclusions and provenance remain unchanged. The current launcher no
longer discovers user hook/config identities; it requires an explicit reviewed
nonsecret isolated profile and fails closed before model spawn. See
[bounded root tooling](root-tooling.md) for the current support and live blockers.

## Default offline verification

```sh
python3 -m unittest discover -s scripts -p 'test_recording_sanitize.py' -v
python3 scripts/check_recordings.py
go test -count=1 -run 'TestManagedRecording' ./tools/replay
go run ./examples/replay-recorded
```

The example starts a copy of itself as the fake stdio peer. It never resolves a
`codex` binary, reads credentials or connects to a provider. Fixtures are embedded
in the compiled executable. Tests poison PATH with a sentinel CLI and repeat each
replay in a fresh SDK epoch. Missing or invalid fixtures fail; there is no live
fallback.

The peer matches client method/parameters and dynamically binds typed RPC IDs.
Recorded item/call/thread references remain consistent. Notifications may precede
responses and multiple requests may be in flight. Reverse answers must match
their original ID exactly once. Extra requests, missing responses, early EOF and
unconsumed frames fail. A private final assertion verifies complete consumption.
Deadlines bound deadlock; replay never sleeps to imitate wall-clock timestamps.

Native JSON, Params and Raw remain separate from SDK convenience event data.
Sanitized Raw is the sanitized wire representation, not the original live bytes.
Text and delta boundaries are evidence of this capture, not requirements on future
models. A parent result never proves whole-tree completion. No child behavior is
claimed from S1 or S2; existing synthetic tree contracts cover that boundary.

`tests/testdata/recordings/real-sanitized` holds recorded evidence;
`tests/testdata/synthetic-faults` holds explicitly synthetic protocol
preparation/fault inputs. Each real manifest records source revisions, binary
digest, prompt/input digests, retained coverage frame indices, exclusions,
budgets, actual transport duration, cleanup and file hashes. ID relations publish
replacement identities and frame references only. Secret canaries test failure
before publication. Unknown structures quarantine the capture.

## Explicit future real-agent maintenance

The independent maintenance entry point is never called by the workflow. From
the repository root:

The historical `--cli` / `--session-root` marker launcher is retired. Current
`record --record reviewed-nonsecret-profile.json` (or Python `--runner` /
`--profile`) validates explicit opt-in, CI exclusion, clean build/CLI identity
and isolated nonsecret paths, then returns the financial gate error before any
spawn. No current flag authorizes new recordings. Original S1/S2 strict offline
replays remain available. The [root-tooling handoff](root-tooling.md) documents
aggregate accounting, R1–R4 support, finite #11 budget proposal and the separate
financial, isolation, native-policy and publication prerequisites.

## Captured coverage

S1 retains 66 frames with 23 exclusions: text, two dynamic file tools, lifecycle
and native usage. S2 retains 28 frames with 17 exclusions: one native question,
original-handle answer, resolution, lifecycle and usage. S3 was not run.
Both captures ended after root completion with EOF and confirmed process reaping.
The manifests retain durations, usage, cleanup and per-frame publication provenance.
Exact monetary cost is unknown; the planning token estimate was not an execution
limit. Captured usage does not establish a reusable token or cost hard stop.

Existing `TestTreeParentIdleChildActive`, `TestTreeCloseCancelSharedSource`,
`TestTreeDisconnectFullQueueAndSeedBounds`, and the V2 creation/noncreation tests
retain responsibility for the unobserved child ordering, cancellation, EOF and
creation-evidence contracts. Their evidence remains synthetic.
