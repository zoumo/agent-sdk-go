# Recorded test data

Only the **real-sanitized** rows below contain captured provider traffic. Files
are checked into this repository under the native `tests/testdata` directories. No new
recording was made for the testify test migration. Sanitizer rules, manifests,
file hashes, typed ID mappings, source commits and evidence limits remain intact.

Current per-Story scope: [19-goal/14-scenario evidence matrix](validation/story-coverage.md).
Future root bundles and requested budgets: [capture gate proposal](capture-plan.md).
Neither adds model-call or publication authority.

## Real recordings

| Capture | Native wire data | Provenance and file hashes | Expected observations | ID mappings / transport |
|---|---|---|---|---|
| Codex S1: successful root task with two dynamic tool calls | [protocol.jsonl](../codex/tests/testdata/recordings/real-sanitized/s1/protocol.jsonl) | [manifest.json](../codex/tests/testdata/recordings/real-sanitized/s1/manifest.json) | [expected-observations.json](../codex/tests/testdata/recordings/real-sanitized/s1/expected-observations.json) | [id-relations.json](../codex/tests/testdata/recordings/real-sanitized/s1/id-relations.json) |
| Codex S2: successful user-question request and original correlated answer | [protocol.jsonl](../codex/tests/testdata/recordings/real-sanitized/s2/protocol.jsonl) | [manifest.json](../codex/tests/testdata/recordings/real-sanitized/s2/manifest.json) | [expected-observations.json](../codex/tests/testdata/recordings/real-sanitized/s2/expected-observations.json) | [id-relations.json](../codex/tests/testdata/recordings/real-sanitized/s2/id-relations.json) |
| Claude S1: real CLI authentication failure, **no successful model or tool execution** | [wire.jsonl](../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/wire.jsonl) | [manifest.json](../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/manifest.json) | [expected-observations.json](../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/expected-observations.json) | [id-relations.json](../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/id-relations.json), [transport.json](../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/transport.json) |

Codex's [recording summary](../codex/tests/testdata/recordings/real-sanitized/recording-summary.json)
describes the original capture activity. Both Codex captures used CLI 0.162.1;
Claude used CLI 2.1.258. These are historical observations of those captures,
not current live compatibility claims. Codex S1 does not cover native shell or
approval requests; S2 is a user-question interaction, not a native approval run.
Claude's `result` can have subtype `success` while `is_error` is true: the recorded
authentication failure must not be counted as a successful model response.

**Real subagent recordings are still missing for both SDKs.** Every available
real capture records zero children. Nested/parallel child handling, child
forwarding, parent-first completion and background work remain covered by
synthetic tests; these tests do not establish successful real-agent behavior.

## Synthetic fixtures

| Fixture | Files | Meaning |
|---|---|---|
| Codex synthetic lifecycle/faults | [protocol.jsonl](../codex/tests/testdata/synthetic-faults/lifecycle/protocol.jsonl), [manifest.json](../codex/tests/testdata/synthetic-faults/lifecycle/manifest.json) | Handwritten fault/lifecycle traffic; not captured provider output |
| Claude synthetic control protocol | [controls.jsonl](../claude/tests/testdata/synthetic-faults/controls.jsonl), [README](../claude/tests/testdata/synthetic-faults/README.md) | Handwritten control/permission/cancellation cases |
| Claude offline subagent example | [events.ndjson](../claude/examples/subagents-offline/testdata/events.ndjson) | Synthetic nested/background task sequence |
| Shared story protocol peers | [peer_test.go](../tests/stories/peer_test.go) | Generated in-process/self-executed native peers, not recordings |

Source schema-derived round-trip cases and fake subprocess/transport tests are
also synthetic. A native event's `provenance: live` distinguishes a current
observation stream from history; it does not turn a synthetic fixture into real
capture evidence.

## Offline verification

Run `mise exec -- bash scripts/check-offline.sh`. It checks the Codex fixture
whitelist, manifest hashes, ID relationships and sanitizer canaries, the Claude
transcript digests/ID references/secret canaries, and both native replay suites.
The replay examples are `codex/examples/replay-recorded` and
`claude/examples/recorded-replay`; both use local sanitized data.

The migration inventory found **20 fixture assets, all tracked**; the two small
Go embedding containers are separate from those assets.
`git ls-files --others --exclude-standard '*testdata*'` and the corresponding
ignored-file inventory were empty. Private raw captures, credentials and
environment/configuration copies are not published. Do not add raw data or
reclassify synthetic fixtures to fill the missing real-subagent evidence.

## Directory responsibilities

See the [layout standards](../harness/code-layout.md) for the authoritative
cmd/tools/testdata responsibilities. The current entry points are
`codex/cmd/record` and `claude/cmd/record`; they delegate to
their native `tools/recorder`, which uses native `tools/replay` helpers. The data
links above point to the single published fixture copies.

The example-local [Claude subagent events](../claude/examples/subagents-offline/testdata/events.ndjson) remain explicitly synthetic. The library package names `recording` and `transcript` remain protocol-specific even though their directory layouts align.
