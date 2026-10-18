# Subagent capability matrix

Current creation target: **CLI 2.1.258**, returned by the installed
CLI `--version` on 2026-10-18. Official SDK **0.3.258** manifest
and declarations were rechecked for this exact version. No upgrade or model
call was made. See [source provenance](type-sources.md).

**Created is conditionally implemented.** It is a distinct sdk-derived fact
under the [foreground or corroborated successful result criteria](subagent-events.md#creation-evidence).
Associated remains observation only. No live subagent capability is verified.
Newer schema fields remain losslessly preserved; they are not claimed as emitted
by current 2.1.258.

## Versions and evidence levels

| Label | Source / meaning | Boundary |
| --- | --- | --- |
| C | Official npm SDK **0.3.258**, manifest CLI **2.1.258**; local binary read-only inspection | Current creation baseline, E1; `--version` inspection only, no live subagent validation |
| P | Official Python SDK `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`, source CLI target **2.1.302** | E1 source review; not a tested minimum CLI version |
| T | Official TS npm **0.3.302**, `sdk.d.ts` SHA256 `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773` | E1 wire declarations; not runtime or Go API parity |
| D | [Hooks](https://code.claude.com/docs/en/hooks#subagentstart) and [Python SystemMessage](https://code.claude.com/docs/en/agent-sdk/python#systemmessage), reviewed 2026-10-18 | Mutable official docs; E1 only |
| E1 | Read source, declarations or documented contract | Does not mean tests ran |
| E2 | Executed original synthetic codec / subprocess / transport tests | Includes the separately reported race suite; no actual provider process |
| E3 | Actual provider process with a mock backend | **Not run for subagent validation** |
| E4 | Real model end-to-end | **Not run for subagent validation** |

The installed version is verified as 2.1.258 by `--version`; no installed CLI
subagent behavior is live-verified. Partial live checks of other APIs in the main compatibility inventory do not
transfer here. Go **1.26.9**, standard library only. Source pins, hashes and
licenses are retained in [type sources](type-sources.md) and [NOTICE](../NOTICE).

## Per-capability contract

Every row has E1 source/implementation review and the listed E2 fixture coverage.
**All rows have E3/E4 = not run**, including rows whose SDK implementation is
complete. Native availability below means a pinned contract exists, not that
an arbitrary installed CLI emits it or accepts the option.

| Capability | Native availability / version source | Go SDK implementation | E2 evidence | Limits |
| --- | --- | --- | --- | --- |
| Forwarded child text | P initialize `forwardSubagentText`; T option | Open always sends true at initialize; Options.Subagents only controls bounds/history | `TestForwardSubagentTextHandshakeAndOptions`, `TestTreeForwardingBeforeFirstNativeAndDisconnect` | Ack cannot prove forwarding honored; first record says unverified |
| One managed root stream | P/T forwarded envelope contracts | One owned Client, sole Receive consumer, no child attach/resume/hook/process | `TestTreeSingleLayerNativeLosslessAndAssociationOrder` | Only frames emitted on that stream; no historical-file watcher |
| Native text blocks | T assistant and stream envelopes | Preserve content and source; no parent answer concatenation | Single-layer fixture; `TestTreeParentResultChildThinkingAndUsageStaySeparate`; offline example | Missing or suppressed text cannot be recovered |
| Native thinking blocks | T content blocks | Preserve complete blocks and partial native frames with Raw | Same single-layer and usage fixtures | No promise that provider emits hidden thinking; snapshot and delta are distinct |
| Tool use / result | T assistant/user envelopes | Preserve tool IDs, inputs/results, unknown blocks; tool ownership helps parent mapping | Single-layer; `TestTreeLegacyRecursiveToolOwnershipAndEnrichment` | Agent tool name/input alone does not prove a successful spawn |
| Associated before child Native | T local-agent task/message identity; internal relationship policy | SDK-derived association, recursive ancestor ordering, later enrichment/alias merge | `TestTreeModernRecursiveRunsAndLateFrames`, `TestTreeBufferedSourceResolvesBeforeNativeWithoutReplay` | Unknown ancestors remain unresolved; no depth/name guessing |
| Newly Created entity | C foreground resumed-task exclusion; AgentInput has no resume; completed/async_launched AgentOutput | Conditional sdk-derived Created, exact CLI 2.1.258 only | `TestTreeCreatedForegroundAndLateBackground`, `TestTreeCreatedCorroborationOrders`, conservative gates/history/nested/alias/resource fixtures; executed offline example | Requires fresh owned epoch, stable session, builtin invocation, matching native task/tool evidence and attributable ancestry; absent proof means no Created |
| Stable entity, separate runs | T newer schema only; C has no run_id | Same association for later runs, explicit run scope, old-run isolation | Modern recursive runs; `TestTreeStaleMetadataCannotPoisonCurrentRun` | Message frames without run_id stay legacy-unknown, even if current run known |
| Patch-only terminal | P/T task_updated patch completed/failed/killed | Per-run sticky terminal; native killed preserved | `TestTreeRunTerminalsAndBackgroundLevel`, `TestTreeTerminalKindsAndReceiveCancellation`, offline example | Unknown/pending/running/paused are not success; no agent deletion |
| Notification terminal | P/T task_notification completed/failed/stopped | Ends only matching run; duplicate terminal idempotent | Terminal tests above | Cannot end a newer run using an older or missing run ID |
| Native session state | T `session_state_changed`; D emission opt-in | Typed State, unchanged Raw; caller may set `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` through Env | `TestSessionStateAndBackgroundSnapshotCodec`, run terminal tests | Open does not set opt-in; result, idle, task terminal, waiting for action are independent |
| Background task snapshot | T `background_tasks_changed` REPLACE level signal | Raw Tasks entries preserved | Session state codec / run terminal tests; offline example empty snapshot before terminal | Replace application snapshot; process-local, asynchronous, no promised startup snapshot; absence is not terminal/deletion |
| History / resume | P explicit --resume; T history queries are file helpers | Caller-verified SubagentOptions.History association seeds; explicit ResumeID only | `TestTreeHistorySeedAndFreshEpoch` | No native history query API, transcript replay, live poll, or Created; permanent history-gap |
| Permission / question | P/T can_use_tool metadata and response | Original sole PermissionRequest; RequestID/ToolUseID/Epoch/context retained; default deny and cancellation unchanged | `TestTreePermissionUsesOriginalHandleAfterParentResult`, question/queue/cancel tests in `tree_bounds_test.go` | Relationship metadata has no answer authority; no automatic approve or synthetic question responder |
| Raw / unknown / null | T wire envelopes | Event.Raw byte preservation; unknown fields/types, absent/null/value codecs | Single-layer; `TestTaskLineageFieldsLosslessNullAbsentEdits`, `TestTreeUnknownSystemSubtypeNeedsSourceEvidence`, envelope tests | Receiving raw data does not implement a new native action |
| UUID dedup / no-ID deltas | T known envelope UUID; SDK scope policy | Bounded epoch/session/source/task/run/type identity cache | `TestTreeUUIDDedupFullScopeAndNoIDPreservation`, `TestTreeNoRunUUIDNotSuppressedAcrossObservedRunBoundary` | No text/hash dedup, no cross-epoch exactly-once, unknown system subtypes not deduped, partial and complete distinct |
| Unresolved attribution | SDK observation contract | Bounded wait/count/bytes; diagnostics then unresolved Native; later relation only, no replay | `TestTreeUnresolvedExpiryPreservesRawAndOnlyEnrichesLater`, capacity/order fixtures | Buffering may change local delivery order; scope unresolved is not a proven child |
| Resource / control isolation | Existing Client limits plus SubagentOptions | Positive graph/depth/run/dedup/creation-evidence/fanout/held bounds; explicit ResourceLimit; controls bypass relation wait | Bounds, full-queue Interrupt, original question and Close race fixtures | Terminal exhaustion closes owned client; not an independent detachable subscription |
| Root answer / usage | T native result and per-source usage | Root result preserved; UsageScope unknown; no sums | Usage isolation fixture; offline example | Parent aggregate plus child usage must not be added; no fee calculation |
| Close / disconnect | Existing owned stdio Client | Receive cancellation leaves process running; Close owns process; stream-ended incomplete on unexpected exit | Forwarding disconnect, terminal cancellation/history, bounds/Close tests | No task success inference, replay, automatic retry/resume, durable cursor, or restored old handles |

## Execution mode limits

| Mode | Tool events | Text / thinking | Parent and run semantics | Native / SDK evidence and runtime limit |
| --- | --- | --- | --- | --- |
| Current 2.1.258 foreground child | Preserve tool use/results | Preserve forwarded native text/thinking, including partial frames | Observed tool ownership/task bridge; no documented current run ID | P/T E1, single-layer E2; no E3/E4; root answer remains separate |
| Current 2.1.258 background child | Preserve later tool/control events | Continue Receive after root result for later text/thinking | Parent turn result never ends child run; only matching native terminal does | P/T E1, terminal/permission/example E2; no E3/E4; REPLACE snapshot omission does not end child |
| Current 2.1.258 nested child / grandchild | Preserve observed tools and ownership | Same root stream for all attributable descendants | Verified tool owner; known ancestors first; unknown parent unresolved; current CLI does not document parent_task_id | T E1, recursive/held-source E2; no E3/E4; no parent_agent_id live contract or depth guessing |
| Additional tool-only frame with no task evidence | Preserve native tool identity | Preserve whatever arrives, including text/thinking | Scoped SDK tool association; AgentID/TaskID remain empty until proven bridge; run unknown | C parent_tool_use_id / SDK conservative policy E1, legacy E2; no E3/E4; cannot reconstruct unavailable IDs or distinguish legacy executions |
| CLI rejects / ignores forwarding or omits identities | Existing received tool frames remain raw | No guarantee of child text/thinking | Reject initialize => Open fails; accepted but incomplete output remains unverified/unresolved | P/T option E1, handshake/unresolved E2; no E3/E4; no recovery RPC invented |

## Runnable offline example and lifecycle

Run from the native `claude/` directory, without a Claude installation or account:

```sh
mise exec go@1.26.9 -- env GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off CLAUDE_E2E=0 go run ./examples/subagents-offline
mise exec go@1.26.9 -- env GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off CLAUDE_E2E=0 go test -count=1 ./examples/subagents-offline
```

[The example](../examples/subagents-offline/main.go) re-executes itself as an
original synthetic peer under a replacement environment and isolated HOME/workdir.
Its embedded [NDJSON](../examples/subagents-offline/testdata/events.ndjson) emits
root → child → grandchild, text/thinking/tools, root result, an empty background
snapshot, more child text, patch-only child/grandchild terminals, and native idle.
The single consumer prints native Raw frames directly and separate derived JSON
notifications. Derived notifications carry Source, Parent and run/observation
metadata; native events carry only their original fields. Association precedes
attributed child delivery. It uses
the current 2.1.258 shape (no invented run/message-agent/parent-task IDs), emits
foreground grandchild Created and late background child Created after child
output, with the root tool receipt before root result and later task evidence
completing creation proof. The empty snapshot does not mark either task terminal.

The demo explicitly calls Close at scripted idle and drains the closed record
and EOF. That is a demo policy, not an inference that idle means all tasks ended.
Production callers choose their Close policy and retain the original permission
handles. Parent answer, child run status, native idle/requires_action, observation
health and background snapshot are independent dimensions. Do not end the receive
loop at the first result when following background work.

Session-state emission requires the documented opt-in on an applicable CLI.
For a real process, a caller can copy the inherited environment and append
`CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` to Options.Env; non-nil Env replaces the
whole environment. This example's peer emits the event deliberately, so it proves
SDK handling only and needs no real CLI opt-in.

Unexpected disconnect creates incomplete/stream-ended and a permanent gap;
explicit resume/history creates incomplete/history-gap. Open a new observation
explicitly after disconnect: it has a new epoch and request handles. Neither local
sequence nor native session ID supplies a replay cursor. No request, permission
answer or user input is automatically replayed. See the [API and bounds](subagent-events.md)
for all defaults and ceilings and the [source provenance](type-sources.md).

## Sources and license boundary

Python MIT sources were read for protocol behavior, particularly initialize,
patch terminals and keeping stdin/control open after result. No Python source or
fixtures were copied. TypeScript 0.3.302 is **not MIT**: its declarations were
cross-checked as schema evidence only; no package implementation or declarations
are redistributed. Original Go implementation, example and fixtures remain
Apache-2.0; no third-party dependency is used. See [type sources](type-sources.md).

The [ACP Subagent Sessions RFD at 014b1391ee8a49f3fb7a5536cd4a6cce4116825c](https://github.com/agentclientprotocol/agent-client-protocol/blob/014b1391ee8a49f3fb7a5536cd4a6cce4116825c/docs/rfds/subagents.mdx)
is design context only: a draft, not a stable standard, runtime dependency or
conformance claim. This SDK does not implement ACP.

中文事件时序说明：[前台/后台任务、子流、审批和结束边界](event-sequences.md)。
