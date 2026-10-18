# Default managed Open: offline implementation evidence

Date: 2026-10-18 UTC. Baseline main:
`a2cb8b0093f0aa5e287281ca2ef09dd5935f8fa4`.
The implementation commit is the containing commit; exact pushed SHA and
Linux/macOS Actions conclusions are reported with delivery, not inferred from
baseline CI or queued jobs. Historical ticket evidence is unchanged.

## Delivered contract

Open returns Client, always requesting native forwarding. Receive returns
TreeRecord through private managed state and one native dequeue. There is no
TreeClient/OpenTree or forwarding/mode option. Resource/history validation and
seed fanout admission occur before spawning. Epoch is stamped on delivery so
pre-spawn history records carry the actual process epoch when delivered.
Options.Tree is runtime-only, omitted by Options.Types; history slices are not
retained after seeding. Independent derived definitions are in
[derived_events.go](../../derived_events.go).

Native Event/ResultEvent declarations, origin object types and parser match the
baseline exactly after excluding comments; native JSON codecs are untouched.
New `TestDefaultManagedOpenAndNativeOriginBoundary` exercises ordinary zero-Tree
Open, first unverified record, true handshake, exact Raw, Origin absent/null and
object with unknown members, and native codec round trips. Existing native,
unknown-event/content and type-identity tests remain. Wrapper Origin is separate;
derived records have no Native payload. See the [migration](../default-managed-migration.md).

All prior tree creation/lineage/terminal/bounds suites now call ordinary Open
with Options.Tree. Native-focused tests use a test-only public Receive loop;
production exposes no native-only reader. Permission and control implementations
are unchanged; original handle/epoch, cancellation, blocked-handler, full queue,
no-handler and lifecycle tests pass through the new default path. No extra
reader, child process, turn, resume, subscription, hook or file watcher was added.

## Validation

Local platform: macOS darwin/arm64, mise Go 1.26.9. Checks ran with `env -i`,
isolated temporary HOME/GOCACHE, `GOTOOLCHAIN=local`, `GOENV=off`, `GOPROXY=off`,
`GOSUMDB=off`, `CLAUDE_GO_LIVE=0`, `GORACE=atexit_sleep_ms=0`.

| Check | Result |
| --- | --- |
| Full offline `go test -count=1 -timeout 180s ./...` | PASS |
| Full `go test -race -count=1 -timeout 300s ./...` | PASS |
| `go vet ./...` | PASS |
| Verbose TestLiveCLIInitialization gate | Explicit LIVE SKIPPED, PASS |
| `go list -deps` for types | Only github.com/zoumo/claude-agent-sdk-go/types beyond stdlib |
| `go run ./examples/subagents-offline` | PASS: 23 records; 13 Native, 4 Associated, 2 Created, 4 ObservationState |
| `bash scripts/check-managed-consumer.sh` | PASS: independent temporary consumer compiles; no CLI execution |
| Mermaid user-story diagrams | All nine sources render as SVG with github-light theme |
| Native event/result shape and parser comparison | Same baseline code excluding comments |

Local logs and rendered diagram previews are under `/tmp/default-managed-validation`
and `/tmp/default-managed-diagrams`; they are not redistributed source or live traces.
Precommit diff/gofmt and committed BASE→HEAD checks are required before push.
CI additionally executes the offline example and external consumer on both OSes.

## Source and remaining boundaries

The retained official 0.3.258 sdk.d.ts hashes to
`b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`,
matching [ticket06](subagent-ticket06.md). Lines 1727–1732 describe forwarded child
text/thinking; lines 5011–5019 define idle after heldBackResult flush and the
background-agent loop; lines 5145–5165 describe task_progress metadata.
No upstream code/declarations are copied into this repository.

[User stories](../event-sequences.md) distinguish root result, actual native idle,
background work, progress metadata and output. Background command stdout is not
promised in real time; SDK never reads output files to fill gaps. Forwarding
acknowledgment remains unverified. EOF/disconnect retain observation gaps; no
cross-epoch exactly-once or seamless recovery is claimed. Creation remains gated
to exact CLI 2.1.258. **Real CLI/model execution, authentication changes and CLI
upgrades were not performed.** Real story recordings belong to a separate
follow-up validation ticket and remain pending.
