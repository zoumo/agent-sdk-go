# Message, content, result and permission JSON contract

This contract describes the source-reviewed, synthetic-tested Go subset at the
[pinned sources](type-sources.md). It does not assert live CLI interoperability.
The canonical types and codecs are in `types`; root aliases have identical type
identity and codec methods. Both packages export `ParseEvent`.

## Wire layout and supported views

`Event` is the stream-json envelope. Assistant and user payloads remain nested
under `message`; `Message` describes only that nested object. The outer
`parent_tool_use_id`, `error`, `tool_use_result`, `origin`, `request_id`, UUID,
session ID, replay/synthetic markers and user-message UUID stay on `Event`.
Unknown envelope and nested payload fields are retained. `MessageOrigin.Kind`
is an open string; further provenance keys stay in its `Raw`.

`Content` is a string or ordered block array. Typed branches are text, thinking,
redacted thinking, tool use/result, server tool use and these server results:
`advisor_tool_result`, `web_search_tool_result`, `web_fetch_tool_result`,
`code_execution_tool_result`, `bash_code_execution_tool_result`,
`text_editor_code_execution_tool_result`, `tool_search_tool_result`.
Server result content remains opaque JSON. Client tool-result content accepts
string, object array, null or absence. Tool inputs, when supplied, must be JSON
objects. Images, documents and future content kinds remain unknown blocks with
complete raw payloads. Server tools have no new execution or answer API.

`SystemEvent` exposes init metadata, compact metadata and shared task
started/progress/notification/updated fields without flattening the task patch.
Task usage, MCP/plugin init entries and compact metadata stay raw JSON. System
subtypes are an open set; an unknown subtype is still a `SystemEvent`.
`RateLimitEvent` and `ConversationResetEvent` have dedicated envelope views.
Rate-limit keys inside `rate_limit_info` follow the camelCase wire schema.
These data views do not implement tasks, session stores, MCP management or hooks.

`ResultEvent` remains an open-subtype record, supporting `success`,
`error_during_execution`, `error_max_turns`, `error_max_budget_usd`,
`error_max_structured_output_retries`, and future values. `success` may carry
`is_error: true`, error text and `api_error_status`; never derive success solely
from subtype. The result exposes stop reason, model usage, permission denials,
deferred tool use, origin and existing duration/cost/usage/output/error fields.
The wire key is **`modelUsage`**, with camelCase `ModelUsage` fields such as
`inputTokens` and `costUSD`. Aggregate `Usage`, permission denials and structured
output remain raw JSON. Model usage and cost can be cumulative across results;
do not sum successive running totals as if they were per-turn usage.

## Constructing and editing

```go
text := "hello"
event := types.Event{
    Type: "user",
    User: &types.Message{
        Role: "user",
        Content: types.Content{Text: &text},
    },
    Fields: map[string]types.FieldState{
        "parent_tool_use_id": types.Null,
    },
}
frame, err := json.Marshal(event)
// frame contains {"type":"user", "message":{...}, "parent_tool_use_id":null}.
// Handle err; these codecs do not start a process or send a frame.
_, _ = frame, err
```

For block content, supply `Blocks: []types.ContentBlock{...}` and the matching
`Type` and exactly one typed block pointer. An explicit empty block slice emits
`[]`. A zero `Content` also constructs `[]`; a supplied `Raw` can initialize a
string/array when neither typed alternative is set. Text and blocks together
are rejected. Multiple event branches, mismatched discriminators, missing typed
branches for known kinds, invalid content unions and invalid field states
return errors. An unknown block needs its complete `Raw` object and matching
`Type`. An unknown event can be constructed with `Type` and optional `Raw`.
Known types may be constructed with keyed literals; no constructor functions
or code generation are required.

Repeated unmarshaling resets the receiver, including all union pointers and
presence overrides. An unsuccessful custom unmarshal leaves a zero receiver.
`ParseEvent` still validates UTF-8 and the existing control/cancellation shape.
These are structural codecs, not exhaustive validation of all CLI semantics;
for example, sparse source/mock result frames remain accepted.

## Raw, typed edits and presence

`Raw` is an owned snapshot for inspection and unknown-field retention. Marshaling
reconstructs JSON; key order/whitespace are not guaranteed. Use `Raw` itself for
an exact original frame. Parsed caller input is not aliased by retained Raw.

For each modeled field, unchanged decoded values retain the original presence
and null representation. Changed typed values override Raw, including edits in
nested blocks, maps and stream deltas. Unknown fields come from the relevant
Raw object. For an unknown message/block with no typed branch, Raw is its body.
Do not edit Raw to change modeled fields: edit the typed field instead. When
replacing a whole typed branch, its own Raw controls unknown fields inside that
branch. Sharing a decoded value's maps/slices between goroutines still requires
caller synchronization or a copy.

`Fields` maps modeled wire key names to an explicit presence override:

| State | Output |
| --- | --- |
| `Default` (or no entry) | Preserve decoded absence/null/unchanged value; emit typed changes |
| `Present` | Emit typed value, including false, zero, empty string or nil (null) |
| `Null` | Emit JSON null irrespective of typed value |
| `Absent` | Omit the key irrespective of typed value or Raw |

For newly constructed structs, `omitempty` zero fields are absent; other fields
emit their typed zero values. Thus `TextBlock{Text: ""}` emits an empty string.
Use `Present` to turn a decoded absent scalar into an explicit zero without
changing existing bool/int/string APIs. Use a non-nil pointer for explicit
false/zero on pointer fields. Nil pointers alone cannot distinguish absent from
null; `Fields`, plus Raw on decoded objects, supplies that distinction.

Common envelope metadata exists on some typed views as well for compatibility.
When both copies are edited, the `Event` field wins; an unchanged envelope copy
does not mask a changed view. Explicit `Event.Fields` overrides take precedence
over a branch's fields. Prefer editing metadata on `Event` and subtype on the
system/result view. Content block `Type` and event `Type` are mandatory and
cannot be removed by a field override.

Detached permission and question data follow the same JSON contract. Answer
handles, cancellation, lifecycle and transport remain in the runtime. The data
codecs supply no control sending, hosted MCP, hook or permission persistence API.

## Detached permission data

`PermissionMetadata` describes the **nested** `can_use_tool` request, including
tool name/input/use ID, agent ID, suggestions, blocked path, decision reason and
display fields. `ControlRequest.PermissionMetadata()` decodes the raw nested
request; runtime `PermissionRequest.Metadata()` returns an owned snapshot with
the same schema. Neither can answer a request. The request ID remains on the
outer control event and on the runtime handle's `RequestID()` accessor. Metadata
has no connection, epoch, cancellation registry or answer state.

The nested request requires subtype `can_use_tool`, nonempty `tool_name` and
object `input`. The tool-use ID remains optional for compatibility with existing
sparse fixtures. Optional suggestions accept an object array or null and remain
opaque data; they do not install rules. Unknown metadata and input fields remain
in Raw. A zero/nil runtime handle returns `ErrInvalidPermissionRequest` from
`Metadata()`. An unsupported raw control subtype returns a metadata decode error.

`ControlResponse` models the complete envelope and `ControlResponseData` its
correlated body. `Event.Response` exposes this view through both `ParseEvent`
entry points. The shape is:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"original-id","response":{"behavior":"allow","updatedInput":{}}}}
```

The body requires a nonempty request ID and subtype `success` or `error`.
Success accepts absent/null/object response data and rejects an `error` field.
Error requires a string `error` (including empty string) and rejects a response
payload. A flat top-level request ID/subtype cannot replace this nested body.
Unknown envelope and body fields survive round trips. These are codecs, not a
generic API for sending control frames or recovering pending answer authority.

`PermissionResult` models the innermost wire decision. `allow` accepts absent
or object `updatedInput` and rejects `message`; `deny` requires string `message`
and rejects `updatedInput`. Null/array/scalar updated inputs and `ask` decisions
are invalid. New denials emit `message:""` if no message was supplied. The wire
key is camelCase `updatedInput`, not Python's `updated_input` attribute.
Python callback objects and flattened message objects are not wire envelopes.

`PermissionAnswer` remains the existing runtime command accepted by
`PermissionRequest.Answer`. It is distinct from the detached `PermissionResult`
wire view: nil UpdatedInput means use the handle's original input; `{}` replaces
it explicitly. Runtime replies retain the existing allow/updatedInput and
deny/message subset. Unknown wire result fields are inspectable/preservable in
the data codec, not additional runtime answer options or permission persistence.
The runtime continues to reject contradictory/invalid answers after consuming
the sole attempt, with the existing visible failure semantics.

## AskUserQuestion data and answering

`AskUserQuestionInput` contains `Questions []Question`, optional `Answers`
and optional general `Response`. Questions expose text, header, ordered options
and multiSelect; options expose label, description and optional preview. All
three object levels use the same Raw/typed/Fields rules above. A question must
have nonempty text and an options array; an option must have a nonempty label.
The questions array must exist; empty arrays are structurally accepted. Duplicate
question texts are rejected because an answers map cannot distinguish them.
Header/description and multiSelect may be absent. These are structural checks,
not CLI UI policy validation (question count, header length, option count or
whether a label was offered). Future fields remain raw, including nested values.

Each `QuestionAnswer` sets exactly one of `Text *string` or `Labels []string`.
Text supports selected labels, joined multiple labels or custom free text.
Labels emits an array and requires a question with multiSelect true. Empty text
and empty label arrays are supported; null, numbers, objects, nonstring array
elements and simultaneous Text/Labels are rejected. Answer keys must equal
the complete question text, including punctuation and Unicode. `q_0`, a header,
or any other unrecognized key is rejected. Partial maps and explicit `{}` are
accepted; the codec does not invent missing answers. An explicit null answers
object is invalid. Optional Response is a separate general reply; it does not
change answer key validation.

`WithAnswers` replaces the answers map and returns detached JSON while retaining
the input's original questions and unknown fields. It rejects nil maps, validates
the resulting input, and does not mutate the receiver or send anything:

```go
// p is the original *claude.PermissionRequest received by PermissionHandler.
// Handle each error before proceeding; first check p.ToolName().
var input types.AskUserQuestionInput
if err := json.Unmarshal(p.Input(), &input); err != nil { return err }
text := "Summary"
updated, err := input.WithAnswers(map[string]types.QuestionAnswer{
    input.Questions[0].Question: {Text: &text},
})
if err != nil { return err }
return p.Answer(ctx, types.PermissionAnswer{
    Behavior: types.PermissionAllow, UpdatedInput: updated,
})
```

Check that the input contains the expected questions before indexing in an
application. Only the original `can_use_tool` handle may answer. An assistant
`tool_use` block named AskUserQuestion may be decoded for display but carries
no answer authority. No public constructor converts metadata/events/tool-use
IDs into a handle. Prepared JSON cannot bypass cancellation, close, consumed
attempts, timeout/error propagation or uncertain-write handling. Returning from
the permission handler still does not answer; the application may retain the
handle while collecting input, respecting its Context.

For every new data object, repeated decode resets the receiver and failed decode
leaves it zero. Required fields cannot be removed or nulled via Fields; the final
JSON is structurally validated. Presence overrides may remove optional fields,
but cannot make contradictory decisions valid. Unknown fields are retained
without assigning them supported operations.

## Evidence

`types/json_test.go` checks synthetic message/content/system/result branches,
unknown field/subtype preservation, semantic round trips, decoded and constructed
presence, nested typed mutation, repeated decode and illegal unions.
`types_compatibility_test.go` checks the same codec through root and `types`
imports. Existing synthetic runtime, cancellation, single-answer, failure,
dynamic-event-queue, bounded-control and lifecycle tests remain in the full suite.

`types/permission_data_test.go` adds synthetic permission/response/question
round trips, edits, presence, owned metadata, invalid shapes and answer keys.
`question_interaction_test.go` exercises real Go client handles against a mock
subprocess, checking response nesting, unknown input preservation, display-only
assistant events, single-use answers and cancellation after answer preparation.
These mocks are not live CLI validation.
