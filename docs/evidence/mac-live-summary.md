# Mac live validation: final compatibility report

Date: 2026-10-18 UTC. Mac ticket 04 is a documentation and offline-verification
delivery. It dispatches **zero real CLI/model requests**. The shared live budget
is exhausted: **2 + 5 + 2 = 9/9 turn/start attempts, 0 remaining**. No retries or
uncertain model writes were reported in the three successful Mac invocations.
Token usage and monetary charges were not measured; turn counts are not a cost
estimate. Do not rerun the live opt-ins under this completed budget.

## Exact versions and revisions

The official protocol target is **0.160.1**, pinned at
`d27764b82f7118f674371e6d6e76271d9d606edb`. The actual live CLI was
**codex-cli 0.162.1**, on Darwin/arm64 with mise Go 1.26.9 and
`GOTOOLCHAIN=local`. CLI 0.160.1 itself was not tested. These results certify only
the listed observations, without a supported-version range or all-method claim.

| Mac ticket / report | Tested SDK base | Harness/report delivery | Turns |
| --- | --- | --- | --- |
| [01 stdio](mac-stdio-lifecycle.md) | `c3842e2516e8caf383447379557b3bc8702e530a` | `cd5ed954e880ae468f56cdbe1b42122f216a1127` | 2 |
| [02 interactions](mac-interactions-interrupt.md) | `cd5ed954e880ae468f56cdbe1b42122f216a1127` | `b6a42a4a3746488464fc9ee19dedc8f3d9e70346` | 5 |
| [03 loopback WS](mac-websocket-lifecycle.md) | `b6a42a4a3746488464fc9ee19dedc8f3d9e70346` | `7e6468edb701d76f9fdedff53aeb09e45a031c6c` | 2 |

Those deliveries added harnesses and evidence, without production SDK changes.
The report commit is not a new live-tested implementation revision. Native IDs
were compared in memory; A/T1/T2/R in reports are aliases, not usable server IDs.
Detailed commands, bounded waits, event ordering, cleanup assertions and safe
synthetic prompts are retained in the linked reports.

## Results and coverage boundaries

| Scenario | Transport / capability evidence | Observed result | Classification |
| --- | --- | --- | --- |
| Initialize, durable thread, two turns, events, read history, explicit new connection and same-ID resume | stdio; public typed methods and native IDs | Exact marker in deltas/items, completed terminals, persisted two-turn history, unsubscribe and bounded owned-child Close | Actual pass (01) |
| User input | stdio; experimental schemas, plan mode advertised; typed request and answer | One BLUE answer → exact request/thread resolution → BLUE agent result → same-turn completed terminal | Actual pass (02) |
| Synthetic command effect | stdio; completed command item | Exact synthetic output, exit 0 and turn completed | Actual pass for effect only (02) |
| Command approval | Schema/typed representation; inherited approval policy never | No approval request; command effect does not prove approval | Untriggered (02) |
| File approval and file effect | Schema/typed representation; read-only sandbox | No approval, no completed file change, owned marker absent | Untriggered; no write pass (02) |
| Permissions approval | Schema/typed representation; model tool availability unestablished | No approval or permission expansion | Untriggered (02) |
| Interrupt | stdio; active turn observed | Exact command guard never matched; no InterruptTurn sent; turn completed normally | Untriggered; no ack, interrupted terminal or stopped-process pass (02) |
| Authentication | Real loopback WS capability-token service | Wrong synthetic token rejected with HTTP 401; correct token initialized | Actual pass for this local mode (03) |
| Two WS turns, history, fresh connection and same-ID resume | Real loopback WS; typed lifecycle/events | Exact markers, completed terminals, persisted history; client Close left service available; owned service/token cleanup passed | Actual pass (03) |
| Public/cross-host WSS, TLS proxy, JWT issuer/signed bearer, token revocation | No suitable live deployment exercised | Synthetic TLS fixtures remain offline evidence | Not covered |
| Legacy approvals, pending-approval interrupt, active-turn recovery, crash recovery | No corresponding live scenario | No inference from generated types or raw reachability | Not covered |

Actual pass requires the scenario's assertions, not just a passing Go test or RPC
ack. Untriggered means the intended behavior did not occur. Environment-blocked
means a concrete prerequisite prevented execution. Not covered means unattempted
or outside the available environment. Failures and outcome-unknown operations
are recorded separately rather than recast as these categories.

The [historical Linux attempt](final-live.md#historical-linux-attempt) used CLI
0.159.2 and SDK `a83ab64915e0bb7e7290d74ca3ac0204d11ec61c`: initialization
ended outcome-unknown/EOF, and a zero-message diagnostic found a SQLite startup
blocker. It sent zero model turns and remains environment-blocked. It neither
invalidates the Mac observations nor becomes a pass. There is no current Mac
SDK defect or unresolved model-write outcome demonstrated by these reports.
The initial WS launcher-inspection failure occurred before any thread/turn and
was corrected in the harness; only its subsequent successful run supports pass.

## Offline repairs and final gate

These later changes affect tests only and do not extend live evidence:

| Commit | Scope / evidence |
| --- | --- |
| `1d15d9d2dd8d7499a2e6b350880cf43a14b20ffa` | Resolution/expiry ordering and stale ID handles use synctest; public stdio/WS/WSS cases retain normal lifetime. Race scheduling had allowed valid expiry before resolution. |
| `7d7e78f502d826d8ba844daa04fd3b0a2afd8129` | [Default TMPDIR identity](default-tmpdir-path-identity.md): compare directory identities; strict argv/env assertions retained. |
| `e558ef11c8cc3d2a5be6e0da6f58ef4cbc769b78` | [Default deadline isolation](default-call-timeout-isolation.md): normal startup budget for frame test, exact 50ms/20ms Call deadlines in synctest. |

The old hosted failures are retained in the repair evidence. They are test
timing/portability failures, with no demonstrated production defect. The earlier
Mac reports' process-local TMPDIR workaround is historical; the current gate uses
the inherited default TMPDIR, without normalization or global configuration edits.

Prerequisite CI [39088440655](https://github.com/zoumo/codex-sdk-go/actions/runs/39088440655)
was independently read back completed/success at exact `e558ef11`, updated
2026-10-18 02:02:02 UTC. This is offline CI, not a live model run. The final report
delivery must additionally pass CI at its own exact pushed HEAD; its URL and SHA
are returned in the delivery handoff to avoid a self-referential commit hash.

Ticket 04 full local gate uses Go 1.26.9 with all four live flags explicitly 0:
`RUN_REAL_CODEX_TESTS`, `RUN_MAC_STDIO_LIVE`, `RUN_MAC_INTERACTIONS_LIVE`,
`RUN_MAC_WEBSOCKET_LIVE`. Commands: `go test -count=1 ./...`,
`go test -race -count=1 ./...`, `go vet ./...`, `go mod download`,
`go mod verify`, `uv run --no-project docs/protocol/verify_inventory.py`,
`go test -v -count=1 ./tests/live`, JSON/link checks and `git diff --check`.
All these local checks passed, including full race, with all three live tests
explicitly SKIP. Normal root package: 24.211s. JSON, 50 local Markdown links,
privacy-path scan, exact commit existence and the 9/9 ledger checks passed.
Results are recorded in the evidence index and final handoff. Protocol
sources and generated bindings are unchanged; no regeneration or new live run.

## Privacy and disposition

Public evidence contains synthetic prompts/results, method ordering, aliased
IDs, versions and commits. No raw wire/stderr, token, authentication material,
private history, account directory or environment dump is added. Older public
Mac reports' executable/user temporary paths are replaced with portable aliases.
Only named documentation files are staged. Claude and global settings are untouched.

Mac tickets 01–03 are delivered; 02 is complete as bounded observation with the
explicit untriggered rows above. Offline repair tickets are delivered. Ticket 04
delivery and exact final CI disposition are recorded in the execution handoff. No SDK repair remains indicated.
Approval and interrupt live follow-up requires separate authorization and a new
model budget; it is not an unfinished action within this exhausted run.
