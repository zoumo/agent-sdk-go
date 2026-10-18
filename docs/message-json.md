# Message, content and result JSON contract (ticket02)

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

`ControlRequest`, cancellation handles, permissions, lifecycle and transport
remain the existing implementations. Control frames retain unknown envelope
fields, but ticket02 does not add new permission schemas, question handling,
control methods, generators, hosted MCP or hooks. No real Agent, account,
credential or Mac test is part of this ticket.

## Evidence

`types/json_test.go` checks synthetic message/content/system/result branches,
unknown field/subtype preservation, semantic round trips, decoded and constructed
presence, nested typed mutation, repeated decode and illegal unions.
`types_compatibility_test.go` checks the same codec through root and `types`
imports. Existing synthetic runtime, cancellation, single-answer, failure,
bounded-queue and lifecycle tests remain in the full suite.
