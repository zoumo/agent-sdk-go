# Recorded native events and offline replay

The real sanitized fixtures were captured at SDK revision
`d465677969cf968c6245e0adeaf7e32c42356913`. Only the public `StartThread` → `Subscribe(ctx, root, types.SubscriptionOptions{})` →
`StartTurn` flow is used. `Requests` and the original answer handle service
reverse requests while the managed observation drains.

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

Existing authentication is used without copying credentials. Mandatory enterprise
audit hooks remain enabled. User hook identities and legacy notify are disabled
for this process only; no persistent configuration or daemon is changed. Raw
protocol, control and stderr are private temporary files. Neither managed hook
payloads nor integration diagnostics are public fixture content. Their exclusions
are recorded by original ordinal in each manifest; this is a partial transcript,
not a claim of every native message being published.

## Default offline verification

```sh
python3 -m unittest discover -s scripts -p 'test_recording_sanitize.py' -v
python3 scripts/check_recordings.py
go test -count=1 -run 'TestRecorded|TestSyntheticManaged' ./internal/recording
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

`internal/recording/testdata/transcripts/real-sanitized` holds recorded evidence;
`internal/recording/testdata/synthetic-faults` holds explicitly synthetic protocol
preparation/fault inputs. Each real manifest records source revisions, binary
digest, prompt/input digests, retained coverage frame indices, exclusions,
budgets, actual transport duration, cleanup and file hashes. ID relations publish
replacement identities and frame references only. Secret canaries test failure
before publication. Unknown structures quarantine the capture.

## Explicit future real-agent maintenance

The independent maintenance entry point is never called by the workflow:

```sh
go build -o /tmp/record-events ./internal/cmd/record-events
SDK_REAL_AGENT_E2E=1 python3 scripts/record_real.py --record \
  --cli /absolute/path/to/pinned/native/codex \
  --runner /tmp/record-events --session-root /tmp/new-private-recording
```

Requires Python 3.11+, an absolute approved CLI 0.162.1, existing login and a new
private session directory. Missing opt-in/configuration refuses to start. Any
nonempty `CI` refuses even with opt-in. Old `RUN_MAC_*_LIVE` switches do not enable
this tool. S1/S2 each run once, including failed startup; no automatic retry or
extra turn is available. Per-scene capture is capped at 2 MiB protocol/control and
256 KiB stderr; each scene has a 120-second deadline, the invocation has a
480-second model-activity budget and bounded cleanup. Only owned process groups
are terminated. No daemon/proxy, extra child connection or history read is used.

The tool creates private raw evidence, not public fixtures. Publication is a
separate allowlist/schema/secret/identity validation step followed by offline
replay and review. A nonzero recorder result or incomplete cleanup is evidence of
failure, not permission to retry. Token usage is retained when observed; exact
monetary cost and a dollar hard stop are unavailable and remain unknown. No
provider retry configuration is changed or claimed as a token/cost limit.

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
