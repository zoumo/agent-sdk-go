# Session native input and configuration

Contract: [Session Interface](../../harness/modules/native-session/interfaces.md).
Native basis: TypeScript SDK 0.3.258 / CLI 2.1.258, as recorded in
[fixed evidence](../../docs/research/claude-session-native-evidence.md).

`Session.Submit(ctx, Input{Text: text})` uses the same native writer as `Input{User: &types.UserInput{...}}`. A successful Submission
means a complete local frame write only. Text cannot be combined with User.
User's Message uses `types.InputMessage{Role: "user", Content: types.Content}`.
The input-specific value avoids output-only Message defaults such as usage and
stop_reason. Raw and Fields retain unknown keys and omitted/null/zero fields;
typed edits use the existing native codecs' precedence rules.

The send allowlist is text, image, document and tool_result. Image/document
blocks use `types.ContentBlock{Type: ..., Raw: ...}` so sources, media, citations,
cache data and extensions stay native. Image source types are base64/url;
document source types are base64/url/text/content. Tool results and document
content validate nested supported blocks; nested tool_result is unsupported.
Other content/source variants are rejected before dispatch even when the output
codec can decode them. This is not a blanket MessageParam compatibility claim.
Native schema/model validation beyond these local checks remains with the peer.

UserInput exposes uuid, priority, origin, shouldQuery, timestamp and
`tool_use_result` independently of message.content's tool_result. Raw retains
unknown envelope/message fields. Session fixes `type:user`, `session_id:""`,
`parent_tool_use_id:null` as in the pinned streaming writer, even after observing
a native main ID or child output. Conflicting target markers are invalid;
there is no child selector. Neither tool-result form grants answer authority.
Use the original IncomingRequest handle to answer permissions or questions.
Priority/shouldQuery carry native intent; the SDK does not schedule execution.

Creation uses SessionOptions.Model and `Thinking *types.ThinkingConfig`:
enabled with a budget emits --max-thinking-tokens (including 0), enabled without
a budget or adaptive emits --thinking=adaptive, disabled emits --thinking=disabled.
Adaptive/enabled display emits --thinking-display. Unsupported combinations fail
before process launch. OptionsFromTypes/Types preserve this configuration.

Dynamic `SetModel(ctx, ModelChange{Model: &identifier})` sends the literal native
identifier. `ModelChange{}` omits model and requests the native default. Empty
identifiers fail locally; null and cross-provider aliases are not synthesized.
`SetThinking` sends set_max_thinking_tokens: a nil budget sends null (clear), a
pointer to 0 sends 0. Nil display omits it; summarized/omitted sends the value;
ClearDisplay sends null and cannot be combined with a display value. Creation
thinking structures are not dynamic commands. Deprecated thinking budgets may
act as on/off on some models and do not guarantee a hard token limit.

ConfigurationReceipt contains the requested payload, complete native
control_response (including rejection and unknown fields), and Accepted only on
success ack. Observed stays nil; later Receive events are separate observations.
Ack does not prove that active execution or descendants changed configuration.
Controls do not depend on Receive progress or wait for idle. They do not rebuild,
interrupt or recursively configure children. Invalid/unsupported requests return
DispatchError NotSent and leave the session usable; native rejection retains
ControlError with original Raw and request ID. Cancellation/transport ambiguity
after dispatch returns OutcomeUnknown with no automatic retry.

Offline synthetic peers validate exact transport content, local errors,
ack/rejection, input while no result/idle has arrived, canceled queued/started
writes and missing acknowledgements. No real CLI/Agent/model, media acceptance,
model scheduling, effective configuration or new recording was exercised.

## Offline implementation checks (ticket 07)

Pinned Go 1.26.9 darwin/arm64 via mise. Public-seam red→green slices covered
structured UserInput, native dynamic controls, creation-only thinking, unknown
source rejection and invalid UTF-8 rejection before encoding. Additional public
regressions cover native rejection, no-result input submission, missing ack,
queued cancellation and started-write ambiguity. T05's handle regressions remain.

Passed: `go test ./claude ./claude/types -count=1`; Session race with
`GORACE=atexit_sleep_ms=0 GOMAXPROCS=2 go test -race ./claude -run '^TestSession'
-count=1 -timeout=180s`; `go vet ./...`; changed document relative links and
`git diff --check`. All Go commands used mise. The complete
`scripts/check-offline.sh` passed with the Python 3.13 shim prepended inside
mise's shell. It includes dependency boundaries, module verification, six CI
gate tests, protocol inventory, 412 generated types/775 synthetic cases,
seven sanitizer tests, recording/transcript provenance checks, all Go packages,
the public consumer and four offline examples. Live e2e gates stayed disabled.

Independent code-review axes used fixed base
`b75ae8cb16a4b7911e60de933f8c9008e6ac288f`: Spec reported zero findings;
Standards reported one independent-assertion issue plus two clarity suggestions.
All three were fixed and the Standards reviewer verified the fixes. Targeted
Session tests/race and vet were rerun after the review edits.

The validation above records the input/configuration ticket. Current
[discovery](session-discovery.md) and the completed
[single-Session migration](../../docs/session-migration.md) have separate evidence. Actual CLI/model/media/configuration-effect verification remains
outside this offline ticket's evidence; no push or release was performed.
