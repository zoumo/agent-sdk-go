# Subagent ticket 02: recursive ordering and reliable deduplication

Date: 2026-10-18. Base: `bf7654035f1e073a2374bf2600e667d947684ac7`.
Toolchain: existing mise Go 1.26.9, Darwin arm64. Delivery is the commit containing
this report; final source/test hashes are in `subagent02-*` records in
[protocol evidence](../protocol/evidence.json). This is E2 offline evidence only.
No real Codex process, model, auth change, branch or worktree was used.

The public-client synthetic peer now covers stdio, loopback WS and synthetic-trust
WSS with these observable contracts:

- Root → child → grandchild and deeper descendants, including native events and
  parent edges arriving before root membership. Proven ancestors precede child
  records; pending events retain source order and exact native envelope bytes.
- Thread parent/source and successful collab identities agree on one node.
  A later live spawn confirms Created once on the existing relation key. Repeated
  turns keep the same entity; derived evidence retains the actual native source.
- Conflicts retain the accepted edge and expose both accepted/rejected evidence;
  cycles are diagnosed without rebinding. Multi-receiver order remains stable.
  Activity paths and unknown activity kinds do not create inferred parents.
- Known terminal item identities deduplicate replay in a bounded connection-local
  FIFO cache. Distinct turns, partial versus complete items, identical no-ID
  deltas and unknown item types with identical IDs remain separate. Eviction
  permits older identities to be delivered again.
- Count, stored-byte and age bounds explicitly deliver unresolved Raw with an
  incomplete diagnostic. Later association does not replay delivered natives.
  Graph/depth exhaustion terminates only the local observer with `ErrLimit`.
- Two consumers remain independent, native Subscribe remains a raw broadcast,
  and transcript assertions permit only initialize/initialized/root turn-start:
  zero child resume/turn/subscribe RPCs and no additional connection.

API additions: `TreeOptions.MaxDepth`, `PendingEvents`, `PendingBytes`,
`PendingAge`, `DedupEntries` (defaults 32, 128, 4 MiB, 5 seconds, 8192).
Bytes count retained Raw plus Params; zero selects defaults and negatives fail.
`TreeEvidence.ParentThreadID` identifies proposed parents;
`TreeEvent.PreviousEvidence` retains the accepted conflicting edge.
`Scope="unresolved"` explicitly does not establish membership.

Final checks (all exit 0):

```sh
export RUN_MAC_STDIO_LIVE=0 RUN_MAC_INTERACTIONS_LIVE=0 RUN_MAC_WEBSOCKET_LIVE=0
export GOPROXY=off GOSUMDB=off
mise exec -- go test -v -count=1 ./...
mise exec -- go test -race -v -count=1 ./...
mise exec -- go vet ./...
mise exec -- go mod verify
python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v
python3 docs/protocol/verify_inventory.py
mise exec -- bash scripts/check_generated.sh
git diff --check
```

Final SDK package timings: normal 24.303 seconds; race 145.653 seconds.
Full logs from this execution: `/tmp/ticket02-test-final.log` and
`/tmp/ticket02-race-verified.log` (local temporary files).

All three opt-in live tests explicitly SKIP. Six CI gate self-tests pass. The
inventory still has 170 client requests, 11 server requests, 85 notifications and
one client notification; regeneration reproduces 397 types and 744 cases from
154 pinned definitions. No protocol wire types or generated schema were changed;
thread lineage is extracted from the existing pinned native shape with exact Raw
retained. Hosted CI also checks committed-range whitespace on Linux/macOS.

Remaining limits: server auto-attach is best-effort source evidence at pinned
0.160.1, not a live runtime guarantee. Unknown versions remain unverified.
History seeds, cold-thread recovery and reconnect replay are outside this ticket.
Unrelated connection sources may be emitted as explicitly unresolved after bounds.
Cache eviction and new connection epochs do not promise exactly-once delivery.
No usage aggregation, final-answer merging or new request authority is added.
