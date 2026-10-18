# Default managed subscriptions — offline acceptance

This ticket implements the approved `.scratch/default-managed-events` spec and issue 01 from the planning workspace. Baseline and V2 prerequisite: **0db0a97271cb881745dd33e735266253ab798ed9**. The prerequisite's [CI 39105250983](https://github.com/zoumo/codex-sdk-go/actions/runs/39105250983) was independently queried: that exact head passed. Its existing historical evidence remains unchanged.

## Delivered contract

- The sole entry is `Client.Subscribe(ctx, rootThreadID, TreeOptions{}) (*Subscription, error)`; `Events() <-chan TreeEvent`. Removed SubscribeTree/TreeSubscription and the native-only registry/fanout, without aliases or mode switches. Zero options always manage root/descendant observation.
- [derived_events.go](../../derived_events.go) contains SDK wrapper, identity, evidence and derived kind definitions. Event's fields, native `types`, schema, codec and generated declarations were not changed. Native Params/Raw remain original bytes, unknown payloads and absent/null remain distinct, and derived records do not fabricate native methods.
- The V2 activity parser is unchanged apart from receiver type renaming. Tests retain actual Activity evidence, successful completed Started without thread/started, history suppression, malformed/non-creation cases, nesting and conflicts. Former broadcast comparisons now check independently owned managed streams and their scoped native records.
- Original Requests/AnswerHandle remains the only answer authority. Parent completion leaves descendants observable; no automatic approvals, child RPCs, extra connections, merged answers or usage totals were added. Existing bounds, atomic fanout, overflow isolation, reserved disconnect gap and first-cause semantics remain.
- [Migration](../default-managed-migration.md) documents the breaking root scope, initial state, no replay and close semantics. [Seven user stories](../event-sequences.md) explain actions, native/derived sequences, IDs, end criteria, state/output updates and forbidden inference, with seven Mermaid diagrams. CLI/source scope is 0.162.1 / 092d3acd6bec3e3a14bdc7e7a2810ab628ab759d.
- Examples and live harness consumers compile against the sole entry. The ordinary observer keeps consuming after root completion. CI now directly runs observe-subagents-offline in addition to its byte-for-byte fixture test and publicconsumer.

## Validation

Local host: macOS arm64, pinned `go1.26.9`, `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off`. All live switches were explicitly 0. The first sandboxed baseline attempt could not open loopback test listeners; the final acceptance ran with approved local listener/cache access. No real Codex executable, authentication change or model run was used.

| Gate | Command | Local result |
| --- | --- | --- |
| Toolchain | `mise exec -- go version` | go1.26.9 darwin/arm64 |
| Dependencies | `go mod download`, `go mod verify`, module diff | PASS, exit 0 |
| Inventory | `python3 docs/protocol/verify_inventory.py` | PASS, exit 0 |
| Generation | `mise exec -- bash scripts/check_generated.sh` | PASS, exit 0 |
| CI negative fixtures | `python3 -m unittest discover -s scripts -p test_ci_checks.py -v` | 6 tests PASS, exit 0 |
| Unit/mock | `mise exec -- go test -v -count=1 ./...` | PASS, exit 0; 3 live tests SKIPPED |
| Race | `mise exec -- go test -race -v -count=1 ./...` | PASS, exit 0; 3 live tests SKIPPED |
| Vet | `mise exec -- go vet ./...` | PASS, exit 0 |
| Public consumer | `mise exec -- go run ./examples/publicconsumer` | PASS, exit 0 |
| Offline example | `mise exec -- go run ./examples/observe-subagents-offline` | PASS, exit 0; output matches expected.txt in unit test |
| External consumer | `mise exec -- go run -mod=mod .` in independent temporary module | PASS, exit 0 |
| Native/request invariants | `git diff --exit-code -- types requests.go` | Unchanged, exit 0 |
| Working whitespace | `git diff --check` | PASS, exit 0 |
| Commit-range whitespace | `GITHUB_EVENT_NAME=push GITHUB_EVENT_PATH=<exact event> python3 scripts/check_whitespace.py` | Performed after commit; see release verification |
| Hosted Linux/macOS | Offline SDK checks for release SHA | See exact run/job URLs in release verification |

The whitespace script requires an actual GitHub event file; a bare invocation failed with missing GITHUB_EVENT_PATH. After committing, it is rerun with a synthetic push event whose before is the exact baseline and whose head is the release commit. Working-tree `git diff --check` is a separate gate. The final delivery records this check and hosted Linux/macOS jobs for the exact release SHA; old prerequisite CI is not implementation acceptance.

An independent temporary module uses `replace github.com/zoumo/codex-sdk-go => <source repo>` and compiles the new Subscribe/Subscription/TreeEvent/types boundary, then prints a no-connection marker. It opens no SDK connection. Seven `.mmd` blocks were extracted and rendered with cached beautiful-mermaid 1.1.3 / github-light to SVG and PNG and visually inspected. The generic skill CLI lacked installed dependencies; the same renderer and PNG helper were used from existing cached tooling without changing another repository or installing packages.

Supporting log hashes (logs are retained with this execution’s release verification):

| Log | SHA-256 |
| --- | --- |
| unit.log | `414f8e2481c3ea276f33395843db3f644a58489bb95cfe78569c34e0c02d66bd` |
| race.log | `9d72dfbb8ca4f08fa6ad53e73156c04f640ff5c53c78e1d28e7067dd24065cba` |
| ci-negative.log | `7d6c37e043b8bec4dcafe31f7c74335029585852476e1cc07370447096c14eab` |
| external-consumer.log | `bbc04e1ee054fb32a90fdfc368b43505a3d3da41fcd03a658462a0caf2625ed9` |
| offline-example.log | `3aaaef7fd4404ca70341386cffe678af14e196a791a3ac4318b8b0f4388b4d43` |

## Limits

These are E1/E2 source/offline results only. Real recording remains a separate ticket, not a pass here. Native auto-attach remains best-effort; initial incomplete, missed notifications and permanent disconnect gaps prevent a completeness guarantee. History imports relationships only, with no Native replay or creation confirmation. Sequence/Epoch are local, not provider cursors or cross-epoch exactly-once identities. Background output is bounded by provider watcher/transcript limits; model write_stdin and client notification paths remain distinct. Queue-only child completion does not require a new parent turn at ordinary idle; durable sleep has a separate wake path.
