---
type: Glossary
title: SDK 共享术语
status: stable
---

# Shared terms

这些术语用于[通用架构](sdk/architecture.md)及[共同验收](sdk/user-stories.md)，不要求原生事件模型相同。

| Term | Meaning and boundary |
| --- | --- |
| Session | A continuing native conversation context that can accept multiple inputs and expose events across multiple execution cycles. An individual execution result does not end the session; the term does not imply persistent recovery after an application restart. |
| Connection reception | A native record has been read by the connection. This alone does not establish which Session owns it. |
| Session reception | A record is accepted for a Session using available native membership evidence; optional parent knowledge is not an admission prerequisite. |
| Isolated diagnostic material | Bounded retained connection records whose Session membership was unconfirmed at reception. Reading them does not replay a Session stream or create answer authority. |
| Session observation membership | Available native evidence establishes that an agent's events belong in a session's observation stream. Membership does not require a known direct parent or a complete agent tree. |
| Agent output settings | Native controls that determine which child content the runtime emits. They are separate from SDK delivery policy. |
| Child detail events | Text, thinking and tool progress attributable to a child agent. Parent tool results, relationship facts, completion/failure notifications and actual approval requests retain their distinct roles. See [ADR-0004](adr/0004-subagent-event-delivery.md). |
| Internal result return | Native orchestration that supplies a child task's result to its parent, independently of application event consumption. |
| Approval controller / observer | The controller owns the original authority to answer a live native request; an observer can inspect evidence without acquiring that authority. History is observation only. |
| Submission | A native request or input was sent with the native API's documented outcome. A successful write does not establish model acceptance, execution or exactly-once delivery; ambiguous outcomes remain visible. |
| Completion | A native turn/task result was observed. Root completion does not prove that children, background work, or the owned transport have stopped. The application chooses its stopping policy. |
| Observed association | Available native evidence confirms a relationship or observation membership. Association alone does not establish a newly created agent, and the relationship graph does not interpret native evidence. |
| Created evidence | A native adapter's explicit supported evidence for creation. It is separate from merely discovering an existing relationship. |
| Root membership | An identity is connected to the selected observation root through confirmed relationships, subject to the native observer's policy and bounds. A root ID alone does not authorize answering child requests. |
| Unknown parent | A parent identity or its relationship to the observation root is not established. Preserve that uncertainty; do not fabricate a root edge or a created event. |
| Gap | Observation is incomplete, for example because native evidence is missing, the adapter cannot resolve an identity, or an observation/resource bound prevents coverage. A gap is visible and is not a successful verification result. |
| Recovery | An explicit native or application action to continue observation after a gap or disruption, where supported. It does not imply transparent replay, retry, exactly-once delivery or restored answer authority. |
| Original answer handle | The native request object/handle authorized to answer a pending approval or question. Preserve its context, cancellation, connection ownership and single-use rules. Relationship records never create an answer entry point. |
| Epoch / run | Native observation or execution identity scoped by each adapter. Each native adapter retains its execution identities and aliases; relationship bookkeeping does not equate them or own their lifecycle. |

能力状态与证据状态分别记录。源码、离线测试、真实录制和真实运行的结论不能互相替代。
