# Shared terms

These terms describe the shared stories and acceptance criteria. They introduce
no shared public protocol or client API. Native event names and evidence stay
in [Codex event sequences](../codex/docs/event-sequences.md) and
[Claude event sequences](../claude/docs/event-sequences.md).

| Term | Meaning and boundary |
| --- | --- |
| Submission | A native request or input was sent with the native API's documented outcome. A successful write does not establish model acceptance, execution or exactly-once delivery; ambiguous outcomes remain visible. |
| Completion | A native turn/task result was observed. Root completion does not prove that children, background work, or the owned transport have stopped. The application chooses its stopping policy. |
| Observed association | Available native evidence confirms a relationship or observation membership. Association alone does not establish a newly created agent, and the relationship graph does not interpret native evidence. |
| Created evidence | A native adapter's explicit supported evidence for creation. It is separate from merely discovering an existing relationship. See [Codex evidence](../codex/docs/subagent-compatibility.md) and [Claude evidence](../claude/docs/subagent-compatibility.md). |
| Root membership | An identity is connected to the selected observation root through confirmed relationships, subject to the native observer's policy and bounds. A root ID alone does not authorize answering child requests. |
| Unknown parent | A parent identity or its relationship to the observation root is not established. Preserve that uncertainty; do not fabricate a root edge or a created event. |
| Gap | Observation is incomplete, for example because native evidence is missing, the adapter cannot resolve an identity, or an observation/resource bound prevents coverage. A gap is visible and is not a successful verification result. |
| Recovery | An explicit native or application action to continue observation after a gap or disruption, where supported. It does not imply transparent replay, retry, exactly-once delivery or restored answer authority. |
| Original answer handle | The native request object/handle authorized to answer a pending approval or question. Preserve its context, cancellation, connection ownership and single-use rules. Relationship records never create an answer entry point. See [Codex requests](../codex/README.md#answer-server-requests-explicitly) and [Claude permissions](../claude/README.md#deferred-permissions). |
| Epoch / run | Native observation or execution identity scoped by each adapter. Claude run IDs and aliases remain distinct from Codex observation/turn identity; the shared graph does not equate them or own their lifecycle. |

Capability status (supported, conditional, unsupported) is separate from evidence
status (source inspection, offline contracts, real observations). The
[shared stories](stories/index.md) retain both. Historical sanitized recordings
do not prove current live compatibility; current successful native subagent
interoperability remains unverified.
