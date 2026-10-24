# Session approval and question requests

`Open` delivers real `can_use_tool` requests through `Session.Receive`, with
`Kind == ReceivedRequest` and the original `IncomingRequest`. `Raw` retains the
complete control envelope and `Params` retains its nested native request. These
are detached snapshots; `Handle()` is the same connection-local permission
authority used by the native reader and writer. Parsing an event, observing a
`tool_use`, reading history or inspecting quarantine never creates that authority.

Keep receiving while a person decides. There is no default decision or SDK
approval timer. Native cancellation and shutdown invalidate `Context()` even
when nobody is calling `Receive`. `Approve(ctx, ApprovalOptions)` supports
`UpdatedInput` and `UpdatedPermissions`; `Deny(ctx, DenialOptions)` supports the
native message and optional interrupt flag. `Answer(ctx, QuestionAnswers)` is
only for an actual AskUserQuestion permission request: keys are full question
text, values are native strings (including comma-separated multi-select text),
and the original question/options/unknown fields are preserved. Runtime sending
rejects array-valued answers and the output-only response field; the detached
question codec retains its earlier broader data-reading support.

Permission updates cover add/replace/remove rules, setMode and add/remove
directories with native destinations. Unknown variants, invalid combinations,
and the unconnected toolUseID/decisionClassification answer extensions return
an explicit error without silently dropping fields. `DispatchError.State` is
`NotSent` for local rejection or cancellation before writing: a still-live
request can be answered later. `OutcomeUnknown` means bytes may have reached
the CLI; it consumes the attempt and must not be retried. Successful answers
prove complete local writes, not CLI acceptance or tool completion.

Session rejects the legacy PermissionHandler option with a migration error.
The temporary NewClient callback/default policy remains only for existing
consumers during expand-contract; Session does not use that callback pool or
automatic-denial path. Runtime `PermissionAnswer` now lives in `claude`; native
decision and update data live in `claude/types`.

Identity replay protection is bounded and never evicted to regrant authority.
Duplicate IDs or exhausted permission/history limits explicitly terminate the
interaction; already accepted FIFO entries remain drainable. Ended identities
retain only ID tombstones in the registry. Application-held requests remain
readable after answer, cancellation or close.

The target contract is [native Session interfaces](../../harness/modules/native-session/interfaces.md).
The source baseline is the fixed TypeScript SDK 0.3.258 / CLI 2.1.258 recorded in
[native evidence](../../docs/research/claude-session-native-evidence.md), including
`sdk.d.ts` PermissionResult and PermissionUpdate declarations. Public Session
tests use isolated synthetic subprocess peers and actual OS write barriers.
They establish Go delivery/authority/wire behavior, not real CLI compatibility.
No live CLI, Agent, model or new recording was run for this change.
