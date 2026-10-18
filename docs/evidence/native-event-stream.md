# Native event stream validation

Date: 2026-10-18 UTC. Baseline: main 0b6447cf085e783830bf841cfeb385438106973b.
CLI target 2.1.258; TypeScript contract 0.3.258. Python comparison pin and its
newer CLI target are explicit in [API comparison](../api-comparison.md).

Receive returns a direct native Event value or SubagentAssociated,
SubagentCreated, ObservationState. No public envelope or SDK-origin string
remains. Subagents configuration only controls bounds/verified history; forwarding
and conservative relationship tracking stay automatic and private. Native Event,
its Origin object and all files in types remain unchanged. No mode was added.

The existing relationship tests now share the implementation package to inspect
private attribution/ledger state. Native transport, permissions, questions and
recorded replay still exercise public Receive. A new direct Receive regression
checks all concrete convenience types, notification order, exact Raw, unknown
native fields and absent/null/object Origin JSON. The independently compiled
consumer checks the public union, types.Event identity and the exact full Go
comparison example. Offline child output compares every original native frame.

Go 1.26.9 checks completed successfully:

- go test -count=1 -timeout 180s ./...
- go test -race -count=1 -timeout 300s ./...
- go vet ./...
- scripts/check-managed-consumer.sh (offline public consumer and comparison code)
- Both offline examples executed successfully.
- python3 scripts/check-transcripts.py (digest, canaries, typed IDs, paths)
- No diff in types, recorded transcript fixtures or synthetic child NDJSON.
- TypeScript example: strict tsc --noEmit against pinned 0.3.258 declarations,
  skipLibCheck enabled; upstream declaration internals are not revalidated.
- Python example: AST syntax check plus pinned source review; no imported SDK or
  runtime/model execution is claimed.

All Go commands used GOTOOLCHAIN=local, GOENV=off, GOPROXY=off, GOSUMDB=off,
CLAUDE_GO_LIVE=0. Race also used SDK_REAL_AGENT_E2E=0. The initial test command
inherited an incompatible GOROOT; setting it to the pinned 1.26.9 installation
resolved that environment-only failure.

No live CLI/model call, capture retry, authentication/security change or fixture
rewrite occurred. Historical evidence retains the APIs it validated at the time.
The concise spec and one focused local ticket are under .scratch/native-events/.
Remote SHA and CI are verified separately after the containing commit is pushed.
