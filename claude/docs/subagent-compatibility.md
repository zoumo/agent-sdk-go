# Session subagent capability matrix

Relationship baseline: SDK 0.3.258 / CLI 2.1.258. No live subagent capability
is certified. The installed version observation below is historical, dated
2026-10-18. See [Session behavior and bounds](subagent-events.md).

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

## Current capabilities

| Capability | Session behavior | Offline regression evidence | Limit |
|---|---|---|---|
| Forwarding | Initialize requests forwardSubagentText; reports coverage uncertainty | client_test.go; session_native_regression_test.go startup/early-ack cases | Ack does not establish complete forwarding |
| Native text/thinking/tools/usage | Exact owned Raw and source; partial/complete snapshots remain separate | session_native_regression_test.go native usage/terminal cases | No aggregate parent result or child fee total |
| Membership / parent evidence | Fixed native task/tool alias and observed tool owner; parent optional | session_test.go, session_native_regression_test.go conflict/non-agent/resource cases | Unknown extensions never prove aliases or ancestry |
| Duplicates / run fields | Native facts survive in receive order | session_test.go 8300 messages; native terminal matrix; subagent_codec_test.go | No current/stale run ordering or UUID cache |
| Unknown / foreign material | Bounded diagnostic quarantine, copies, active expiry, no backfill | Session quarantine count/byte/oversize/expiry cases | Evicted/expired records remain explicit gaps |
| Original permission / questions | Same-stream actual IncomingRequest and one native handle | permissions_test.go, permission_cancellation_test.go, question_interaction_test.go, Session request tests | No default policy; unsupported controls use bounded protocol errors |
| Close / failure | Stop admission, bounded cleanup, accepted drain, explicit Abort | lifecycle tests; Session fault/concurrent-close cases | No recursive completion, automatic reconnect or retry |
| Ordinary resume | Open with literal ResumeID, fresh process, no replay | Actual captured argv/stdin in resume_interrupt_test.go | Requested identity is not recovery confirmation |
| Record discovery | Read-only native local-file enumeration and coverage | session_discovery_test.go | Independent of current observation; not proof of live activity |

Tests and examples use synthetic isolated peers. The checked-in real Claude
capture is an authentication failure, not successful child evidence.
The [offline example](../examples/subagents-offline/main.go) prints native facts
and SDK observations separately, then explicitly closes and drains. Example
policy does not make idle or a result mean all children are finished.

Source pins, hashes and licensing stay in [type sources](type-sources.md) and
[NOTICE](../NOTICE). No third-party implementation or generated native schema
was upgraded in this migration. ACP material is historical design context;
this SDK does not implement ACP.


## Pinned source references

- [Hooks](https://code.claude.com/docs/en/hooks#subagentstart)
- [Python SystemMessage](https://code.claude.com/docs/en/agent-sdk/python#systemmessage)
- [ACP Subagent Sessions RFD at 014b1391ee8a49f3fb7a5536cd4a6cce4116825c](https://github.com/agentclientprotocol/agent-client-protocol/blob/014b1391ee8a49f3fb7a5536cd4a6cce4116825c/docs/rfds/subagents.mdx)
