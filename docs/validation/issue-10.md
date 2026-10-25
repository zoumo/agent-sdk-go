# Issue #10: Claude root tooling and #12 handoff

Base: `52b329a77333e55063a97eaf1304c82b30a99a59` (#8 / PR #24).
Scope: [#10](https://github.com/zoumo/agent-sdk-go/issues/10), parent
[#6](https://github.com/zoumo/agent-sdk-go/issues/6).
Implementation evidence follows the [testing standards](../../harness/testing.md)
and [tool layout](../../harness/code-layout.md). This document grants zero live
calls or data publication. The original real auth-failure fixture is unchanged.

## Delivered boundary

The public manual recorder and private proxy both refuse before any CLI probe or
launch while independent managed-policy and financial/token/generation/retry
isolation are unproven. No success flag can unlock them. `--mode preflight`
reports those blockers without checking an account, reading private sessions or
creating captures. No live probe, recording, authentication repair or mandatory
policy change was performed.

Public offline tool seams exercise fresh synthetic root modes S1/S2/R1/R2/R3,
original permission/AskUserQuestion handles, exact allow/deny and path policy,
configuration receipts, input/interrupt barriers, result error + idle drain,
cleanup, explicit ConfigDir saved-content queries, and strict versioned
publication/replay. Missing execution barriers remain untriggered; model error
with subtype success remains failure. Synthetic peers do not establish actual
ask-policy effectiveness, model availability, persistence or hook-free startup.

The durable aggregate ledger retains attempt reservations, charged turn
allowances, pre-dispatch turn intent and terminal outcomes. Unknown/corrupt/
legacy/locked ledgers fail closed, including after restart; no automatic refund,
retry or migration is available. Ledger limits are constraints, not authorization.
The parent must reconcile previous spent/failed budgets across all ticket ledgers.

## Concrete #12 requested table (pending, not authorized)

Use the existing [capture plan](../capture-plan.md) numbered gates 1–7. For
Claude, freeze the selected model (proposal `claude-opus-5[1m]`), exact absolute
2.1.258 executable and SHA256, clean SDK/tool source + build digest, neutral
prompt/input hashes, approved opaque auth transport and fresh isolated roots.
The historical macOS digest is provenance only; it approves no current binary.
SetModel(default)/SetThinking(clear) in the tool plan do not prove an effective
selected model or a token cap; the actual run table must resolve this first.

| Activity | New roots | Submitted turns | Provider generations including retries | SDK/native retries | Children/depth | Total scene wall | Wire/storage | Stderr |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| R1 continuation/config + isolated persistence | 1 | 2 | 6 | 0/0 | 0/0 | 120 s | 2 MiB | 256 KiB |
| R2 original permission/question | 1 | 2 | 6 | 0/0 | 0/0 | 120 s | 2 MiB | 256 KiB |
| R3 execution input/interrupt | 1 | 2 | 4 | 0/0 | 0/0 | 120 s | 2 MiB | 256 KiB |
| R4 saved-root local queries, at most six calls | 0 | 0 | 0 | 0/0 | 0/0 | 30 s | 2 MiB | 256 KiB ceiling |
| Requested Claude total | 3 | 6 | 16 | 0/0 | 0/0 | 390 s | 8 MiB | 1 MiB |

Requested token ceilings: each generating bundle 20,000 input (cached input
counted once) + 1,500 output; Claude total 60,000 + 4,500. Requested money:
USD 1 per bundle, USD 3 Claude total, including failures/retries. The parent
proposal's USD 6 / 780 s both-provider totals and 900 s coordinator hard ceiling
remain cumulative; historical failures do not replenish these proposals.
S1/S2 historical budgets are not added to R1–R3, and S3 has zero authority.

## Actual launch and publication gates

| Gate | Current evidence / remaining action | State |
|---|---|---|
| Parent run approval and historical ledger reconciliation | No new model budget or publication scope approved; reconcile old auth-failure attempt and all spent ledgers before approving exact table | Pending external approval |
| CLI/version/flags/source | Offline exact 2.1.258/flag/digest checks implemented. No current native CLI executable was probed; 2.1.310 rejected | Pending current approved binary validation after isolation |
| Actual authentication | Operator must supply an existing approved recognized transport opaquely and attest account/model availability; tooling must not inspect credentials or fix login. Minimal replacement-env historical failure is not an account diagnosis | Pending external evidence |
| Managed policy/hooks/tools | No independent proof of pre-launch isolation. Safe-mode/hook-event rejection is insufficient; no mandatory control may be disabled. No environment/boolean bypass exists | Blocked, fail closed |
| Financial/token/generation/retry enforcement | Native max-budget-usd, thinking budget, wall/byte/turn caps are insufficient; no independent enforcement or billing proof | Blocked, fail closed |
| Isolated persisted storage and query | Fresh HOME/config/project mode plus public local-query synthetic tests; no default/private history scan. Actual native persistence and exact query fixtures remain unverified | Offline only |
| Publication | Only future fresh neutral synthetic-task data; separate reviewed approval, frozen per-version schema, typed IDs/timestamps, canaries, hashes, strict EOF/cleanup, review and exact-SHA six CI checks. Unknown/new data quarantined | Pending separate approval |

#12 must stop if any gate is unknown, auth fails, mandatory isolation cannot be
established, provider accounting is unavailable, a request is out of policy,
execution barrier is untriggered or owned cleanup fails. No retry, policy repair,
extra prompt, new ledger or mock pass can replace those requirements.
