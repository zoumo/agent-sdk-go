# Codex independent history query slice (ticket 01)

Repository: `https://github.com/zoumo/agent-sdk-go`; actual base
`bd557d91b98ab4db5e2b968b125fba6d583f791b`. The old
`/workspace/codex-sdk-go` checkout was inspected read-only and left unchanged.

Contracts: [history query](../../harness/contracts/history-query.md),
[US-014](../../harness/contracts/stories/US-014.md),
[native interfaces](../../harness/modules/native-session/interfaces.md).
Native support, exact source pins and differences:
[Codex history](../../codex/docs/history-query.md).

## Delivered behavior and evidence

Added typed `Client.ListThreads`, using the existing pinned types and Call path.
ReadThread/ListThreadTurns/ListThreadItems remain independent native calls.
Production code adds no dependency, storage reader, gate, retry, subscription,
Session state or history projection. Each response is owned by its caller.

`codex/TestIndependentHistoryQueries` crosses real subprocess stdio, loopback ws
and TLS wss with the existing public protocol peer bridge. It verifies discovery
and metadata without Open; exact filter/default/omitted/null/false/0/[] requests;
unknown response members/readState/readStates and source/status/item variants;
opaque next/backwards cursors, multiple and empty pages, repeated/out-of-order
turns, reverse re-inclusion, exclusive item anchors with nonempty turnId;
native invalid cursor/local-originator/legacy-items errors and raw data;
pre-canceled calls and in-flight cancellation after an explicit peer marker;
detached results; same-identity Session coexistence without stable/idle checks.
The peer emits a typed native agentMessage delta followed by a query-held marker
before cancellation, and another typed delta and query-end after the late response.
Receive checks native type, identity, content and order with no History/Request,
then drains to EOF after
explicit Close, and trace excludes extra start/resume/subscribe/input/answers.

`CommonStoriesSuite/TestIndependentHistoryCodex` enters the public native APIs
through the existing suite/adapter seam and checks an exact query-only trace,
metadata vs summary vs saved items, unknown content and pagination. This is
Codex evidence only: Claude independent history and full common completion are
separate work, not certified by this slice.

`codex/examples/history-query-offline` is an executable synthetic public usage
example, added to the offline gate. It self-executes a strict peer and needs no
installed agent, credentials, model or session files.

## Checks

Commands used mise-managed **Go 1.26.9 linux/amd64**, with isolated compiler,
module and build caches under `/tmp`. `/tmp/mrun` invokes mise with those paths.
Mise was fetched from its public GitHub release because the install-script
endpoint returned HTTP 403; this was a network response, not an approval denial.

| Check | Result |
|---|---|
| Red test for ListThreads, before implementation | Failed to compile: method undefined; then green after thin method and peer |
| Focused public history queries and common Codex story with race | Passed, all stdio/ws/wss cases |
| `bash scripts/check-offline.sh` | Boundary, module verification, inventory, generation, sanitization/hash and transcript checks passed; full Go suite failed on existing answer/resolution race described below |
| Full `go test -race -p=1 -count=1 -timeout=300s ./...` | Failed on the same existing answer/resolution assertion; all other packages passed, no DATA RACE report |
| `go vet ./...` | Passed |
| Public local `go run ./tests/consumers/codex-public` | Passed |
| All five offline examples, run separately after offline gate stopped | Passed, including new history example |
| `git diff --check` | Passed |
| macOS checks | Not executed in this Linux environment |
| New exact fetched-module consumer | Not verified: this result has not been published; no push authorized |
| Real server/model E2E | Disabled/skipped; no live compatibility or model execution claim |

Inventory remained 417 generated types / 783 synthetic cases, 173 client
requests, 11 server requests, 86 server notifications, 1 client notification and
24 controls. Generated schema/types and real fixtures are unchanged.

### Full offline failure and independent baseline evidence

This execution observed
`TestRemotePublicContractParity/ws/TestConcurrentAnswerAndNativeResolutionHaveOneOutcome`
fail at request_contract_test.go:967/977: expected not-sent and zero answer
frames, observed outcome-unknown and one frame. This does not contradict the
parent-reported successful baseline CI; it is a local timing-dependent failure.

An independent `git archive bd557d91b98ab4db5e2b968b125fba6d583f791b` tree at
`/tmp/history-query-baseline` reproduced the same assertion failure with:

```sh
GOMAXPROCS=2 /tmp/mrun go test ./codex \
  -run '^TestRemotePublicContractParity/(ws|wss)/TestConcurrentAnswerAndNativeResolutionHaveOneOutcome$' \
  -count=5
```

The command ran from the extracted baseline (stack traces name that path),
not from the changed checkout. No assertion was weakened, skipped or repaired
in this unrelated area. Full-suite green acceptance remains blocked by this
failure even though the focused slice passes.

Supporting logs are included in the handoff archive alongside the git patch:
`history-query-offline.log`, `history-query-race.log`, `history-query-vet.log`,
`history-query-baseline.log`, `history-query-focused.log`, and
`history-query-examples.log`. They contain synthetic paths/frames only.

## Review and limits

The required implement/tdd skills were loaded and the approved public seams
were used without another approval gate. Dual-axis code review found one Standards test dependency-check issue and one
Spec native-live evidence gap. Both were repaired: require checks precede indexes
and raw mutation; typed deltas cross the pending query and late response barriers.
Independent rechecks reported Standards 0 remaining and Spec 0 remaining.
Implementation checkpoint after corrections:
`cb26731b8a467ce458075bf6e302ef39c9c9cccd`; the subsequent evidence-only commit
is identified by the transferable handoff manifest. The focused verbose race log
also retains actual synthetic peer request traces. The public consumer now also
compiles all four query signatures, and the fetched-consumer script includes the
new example for its next published-revision run. No push, PR, external issue, real agent, new recording,
Mac code or private session access occurred. Local contract documentation does
not certify upstream execution or change the pinned schema.
