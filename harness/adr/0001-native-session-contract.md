---
type: Architecture Decision
title: Native evidence in long-running sessions
status: stable
---

# Native evidence in long-running sessions

The SDKs target the repository's common capability set and user stories through long-running sessions with continuing event streams. Their purpose is session control and native event delivery, rather than complete parity with each official SDK; application-restart persistence is outside this design's scope.

Preserve native evidence and input scheduling semantics. Do not require a common public Turn object, invent turn identities or lifecycle boundaries, or reject input solely because foreground execution is active. Native validation and rejection remain observable, and submission success carries only the guarantee established by the native operation.

Permission approval requires a common SDK operation interface to reduce application integration cost. Common operations cover receiving requests, approving or denying them, and observing cancellation or invalidation. Retain native details and expose specialized operations, such as input modification or scoped authorization, only where the request supports them. Answers must retain the authority of their original native request handles. The common interface preserves native protocol independence and original answer authority.

Automatic subscriptions serve observation only: obtain available native events without starting, resuming or rerunning tasks or approving permissions. Expose unavailable observation as a gap. Native forwarding can satisfy observation without an extra subscription.

Default observation includes the session's directly and indirectly associated child agents wherever native evidence establishes membership, without a preset hierarchy depth. Root execution completion does not end this observation. Native or resource limitations must expose gaps; automatic subscription does not promise replay of events missed before subscription unless the native protocol supports it.

SDKs must also support explicitly listing subagents belonging to the current session, using native query or retained-record discovery facilities rather than only agents seen in the current event stream. Preserve native identities and available metadata; parentage and runtime state are optional evidence and must not be invented. A listed record does not by itself prove that the agent is currently running or that its live events are subscribed. Listing is a discovery operation, not an implicit resume, history replay or subscription operation. Native version, storage access and query coverage limits must remain explicit. See [US-013](../contracts/stories/US-013.md) for the common acceptance target; this addition is a design requirement, not an implementation claim.

Each session provides one ordered event consumption entry point; applications distribute events to their own consumers. Delivery preserves SDK reception order and does not claim a global occurrence order across agents. Independent subscriber buffers, cursors and replay are outside this contract.

Closing a session ends SDK interaction and observation and releases SDK-owned resources. Interrupting execution is a separate native operation that can leave the session available for further events and input. Closing an owned process may stop its work, while closing an external connection may leave remote work running. Neither successful close nor an interrupt acknowledgment universally proves all agents stopped; no recursive stop guarantee is introduced.

## Disconnection and recovery

The provisional remote recovery contract is superseded by [ADR-0002: disconnect reporting and explicit Load](0002-disconnect-reporting-and-explicit-load.md). SDKs report connection loss without initiating reconnect or guaranteeing missed-event recovery. Callers decide whether to reconnect and can explicitly use Load to resume the same native session and replay retained history. Same-object recovery, subscription restoration and approval-handle recovery are deferred. Transport loss remains distinct from evidence of native execution termination.

## Approval delivery and local cancellation

Deliver approval requests through the session's single event stream with a common answer handle. Applications can continue consuming events while deferring an answer; no separate approval callback delivery mechanism is required by this contract. Reconnection does not automatically restore an old handle's usability: restoring or replacing an answer entry point requires native request replay and validity evidence. Handle recovery design is deferred under [ADR-0002](0002-disconnect-reporting-and-explicit-load.md).

Canceling a local event wait ends only that wait, without closing the session or interrupting native execution; consumption can be attempted again. Interrupt and close are explicit operations. When an input or approval answer may already have been sent, preserve an unknown outcome rather than claiming nonexecution or automatically resending it.

Approval requests await an explicit application answer under native waiting, timeout and cancellation rules. The SDK does not automatically approve or deny an unanswered request or impose an additional common approval timeout. Applications own their decision and timeout policies; native cancellation, timeout and session termination update handle validity. This target changes any existing no-handler default-deny behavior; it is not a claim about current implementations.

## Failures and event draining

Native execution or tool failures remain native events and do not by themselves terminate the SDK session. Report SDK interaction failures, such as transport loss or invalid protocol data, as errors with an explicit indication of whether the current interaction has terminated. Do not present SDK failures as normal native execution results.

Session termination and event exhaustion are separate observations. After close or a terminal failure, callers can drain events already accepted by the SDK, then observe the stream's terminal outcome. Resource cleanup does not wait for event consumption. This is an in-process delivery contract and does not guarantee persistence or delivery after a process crash.

## Inputs and unsupported operations

Text is a common baseline input capability. Preserve provider-specific representations for supported images, attachments and structured content rather than reducing all input to strings or synthesizing prompts. Common user goals and acceptance criteria do not require identical wire formats; inability to express a required story is an explicit capability gap.

Ordinary input targets the native main conversation that the SDK opened or resumed. Receiving a child agent's events does not redirect subsequent input to that agent or grant control over it. Direct child-agent messaging, if introduced, requires an explicit target and a separate capability contract.

Approval and interactive question answers use their original native request handles and answer structures, not ordinary conversation input. Observing a tool invocation or question text does not create answer authority. Expose an answer entry point only when the native protocol supplies an actual interactive request, retaining its identity and validity constraints.

Explicitly reject known unsupported operations or options instead of silently ignoring or downgrading them. Distinguish SDK implementation support from availability in a native version or a specific request. Check known restrictions before submission where possible and preserve native rejection when availability cannot be established in advance. This does not require a universal live capability-discovery service.

## Common configuration controls

Model selection and modification belong to the common control contract, using consistent operation concepts and naming while retaining native model identifiers. Distinguish selection at session creation from modification during a session. Reject unsupported dynamic changes explicitly; do not introduce common model aliases or implicitly recreate a session to simulate a change.

Thinking configuration likewise has a common operation concept, with values expressed through each provider's native types. Do not assume cross-provider low/medium/high levels are equivalent or translate levels into budgets without an explicitly researched mapping required by a common user story. Provider-specific configuration remains native; these controls do not aim at complete official SDK configuration parity.

Configuration changes promise only the outcome and effective timing established by native evidence. Do not schedule changes locally, wait for execution completion, interrupt or restart work to simulate a change. An accepted setting request does not prove the active execution switched configuration; a requested value is not an observed current value without confirmation or query evidence.

Preserve native configuration scope and inheritance rules. Changing the current session's configuration does not cause the SDK to traverse child agents and issue corresponding changes. Effects on existing or future children follow native behavior. Automatic subscription grants observation, not child-agent configuration authority; targeted child control requires a separate contract.

The session event stream is the primary source of execution evidence. Preserve native status and expose SDK connection/close state, but do not synthesize an aggregate busy/idle state across agents. Applications determine completion according to their goals. Session observation membership is the required relationship: deliver child-agent events established to belong to the session, retaining their available source identities. Preserve parentage where native evidence supplies it, without inferring missing parents or requiring a complete tree as a condition of event delivery.

These choices preserve protocol evidence where native execution models differ while keeping common user goals as the acceptance target. This is a design decision, not a claim that all SDKs already implement the target architecture.
