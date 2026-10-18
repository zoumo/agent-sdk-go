# Go and official Agent SDK API comparison

Status: implemented; previous Go baseline main `0b6447cf085e783830bf841cfeb385438106973b`. CLI target **2.1.258**. Examples below are original usage examples, not copied upstream implementation. No model calls were run.

## Fixed sources

- TypeScript **0.3.258**: [versioned declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk.d.ts), [official npm archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz). Static local declarations inspected; SHA256 `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`. See [provenance](evidence/subagent-ticket06.md). Its `query()` and `Query` contract are the comparison target; no unstable session API is assumed.
- Python **commit f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab**: [client](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py), [streaming examples](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/examples/streaming_mode.py), [types](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py). This source targets CLI **2.1.302**, not 2.1.258. Python objects flatten wire messages; Go preserves stream-json envelopes. This is a separate source comparison, not matching-version parity.

## Surface comparison

| Task | Go current baseline | Go implemented | Official TypeScript 0.3.258 | Official Python pinned commit |
| --- | --- | --- | --- | --- |
| Configure | `Options{CLIPath, WorkDir, Model, ResumeID, Tree, PermissionHandler}` | `Subagents` bounds/history | `query({prompt, options:{cwd, model, resume, canUseTool}})` | `ClaudeAgentOptions(cwd=..., model=..., resume=..., can_use_tool=...)` |
| Create | `Open(ctx, Options)` | unchanged | `query(...)` starts query/process; no Go-style Open/Send pair | `await client.connect()` or async context manager |
| Resume | `Open(ctx, Options{ResumeID:id})` | unchanged | `options.resume` on new query | `ClaudeAgentOptions(resume=id)` on new client |
| Send | `client.Send(ctx, text)` | unchanged | string prompt or `AsyncIterable<SDKUserMessage>`; `Query.streamInput` also declared | `await client.query(text)` |
| Receive | `(TreeRecord,error)` with `record.Native` | `(ReceivedEvent,error)`; type switch on direct `Event` or concrete derived event | `for await (const message of q)` gets `SDKMessage` union | `receive_messages()` yields message union; `receive_response()` stops at a result |
| Approve/question | original `PermissionRequest` callback; `Input()` and `Answer(PermissionAnswer)` | unchanged | `canUseTool` callback; AskUserQuestion replies via updated input; distinct `onUserDialog` contract also exists | `can_use_tool` callback and permission result classes; AskUserQuestion via updated input |
| Children | automatic forwarding + public relationship wrapper | automatic forwarding + private ledger; separate `SubagentAssociated` / `SubagentCreated` | `forwardSubagentText`, `agents`; native parent tool/task evidence; no Go-derived creation event | `forward_subagent_text`, `agents`; typed/parsed messages; no Go-derived creation event |
| Close | `client.Close()` | unchanged | `q.close()` | `await client.disconnect()` or context exit |

Go does not expose the official `agents` configuration used in the examples below; child relationships are observed automatically from the owned CLI stream. Go does not implement the entire official configuration, agent-definition registration, hooks, MCP server configuration, history query helpers, or TypeScript `onUserDialog`/elicitation surface. Receiving unknown frames losslessly does not implement their response APIs. Native `Event.Origin` stays an official object; SDK origin strings disappear. Derived conveniences are implemented by Go and are not official message variants.

## Go: previous versus implemented

Current baseline:

```go
record, err := client.Receive(ctx) // (TreeRecord, error)
if err == nil && record.Native != nil {
    fmt.Println(record.Native.Type)
}
```

Implemented signature (a direct native value, not an envelope):

```go
message, err := client.Receive(ctx) // (ReceivedEvent, error)
if err == nil {
    switch event := message.(type) {
    case claude.Event:
        fmt.Println(event.Type, event.ParentToolUseID)
    case claude.SubagentCreated:
        fmt.Println(event.Source.TaskID)
    }
}
```

## Go: configuration, session, input, streaming and approvals

The application supplies `decide`, which can wait for its human UI using the
original request context. It returns `PermissionAllow` or `PermissionDeny` via
`PermissionAnswer`; no `Allow`, `Deny` or `Questions` request methods exist.
For questions, decode `request.Input()` into `AskUserQuestionInput`, collect
answers keyed by full question text, call `input.WithAnswers(answers)`, and
pass the result as `PermissionAnswer.UpdatedInput` to `request.Answer`.

```go
func consume(ctx context.Context, resume string,
    decide func(*claude.PermissionRequest) claude.PermissionAnswer) error {
    client, err := claude.Open(ctx, claude.Options{
        CLIPath: "claude", WorkDir: ".", ResumeID: resume, // empty creates a session
        PermissionHandler: func(request *claude.PermissionRequest) {
            answer := decide(request) // application-owned human UI
            _ = request.Answer(request.Context(), answer)
        },
    })
    if err != nil { return err }
    defer client.Close()
    if err := client.Send(ctx, "Explain this directory."); err != nil { return err }
    for {
        message, err := client.Receive(ctx)
        if err != nil { return err }
        switch event := message.(type) {
        case claude.Event:
            fmt.Println(event.Type, event.ParentToolUseID)
            if event.Stream != nil { fmt.Println(event.Stream.Delta) }
            // Native children use native IDs; keep receiving after root result
            // when later child output matters. ctx/application policy ends this loop.
        case claude.SubagentAssociated:
            fmt.Println(event.Source, event.Parent)
        case claude.SubagentCreated:
            fmt.Println(event.Source.TaskID)
        case claude.ObservationState:
            fmt.Println(event.State, event.Reason)
        }
    }
}

func answerQuestions(request *claude.PermissionRequest,
    answers map[string]claude.QuestionAnswer) error { // answers come from the human UI
    var input claude.AskUserQuestionInput
    if err := json.Unmarshal(request.Input(), &input); err != nil { return err }
    updated, err := input.WithAnswers(answers)
    if err != nil { return err }
    return request.Answer(request.Context(), claude.PermissionAnswer{
        Behavior: claude.PermissionAllow, UpdatedInput: updated,
    })
}
```

`client.SessionID()` returns the observed ID for a later `Open` with ResumeID.
`client.Close()` explicitly closes and reaps the owned process; the defer covers
all exits here. [Compiling examples](../example_test.go) also show create/resume,
interrupt and permission denial. The [executed offline child example](../examples/subagents-offline/main.go)
shows continued output after the parent result, creation and explicit close at
scripted idle. Source links: [client](../client.go), [permission handle](../permissions.go),
[question edit helper](../types/questions.go), [separate derived types](../derived_events.go).

## Official TypeScript: create/resume, send, stream, approvals, children, close

Original example against the fixed declaration contract. Omit `resume` to create a new session; use the `session_id` from a received message to resume later. `prompt` is the initial send; an async prompt iterable is the multi-turn input surface. Do not close at a root result if the application wants later child messages.

```typescript
import { query, type CanUseTool } from '@anthropic-ai/claude-agent-sdk';

// Application UI adapter, supplied by your host.
declare function askHuman(question: string): Promise<string>;

const canUseTool: CanUseTool = async (name, input) => {
  if (name === 'AskUserQuestion') {
    // Answer keys are the full question strings.
    const questions = input.questions as Array<{ question: string }>;
    const answers = Object.fromEntries(await Promise.all(
      questions.map(async q => [q.question, await askHuman(q.question)])
    ));
    return { behavior: 'allow', updatedInput: { ...input, answers } };
  }
  return { behavior: 'deny', message: 'Approval required from the application user.' };
};
const q = query({
  prompt: 'Explain this directory.',
  options: { cwd: '.', resume: 'existing-session-id', canUseTool,
             includePartialMessages: true, forwardSubagentText: true,
             agents: { reviewer: { description: 'Review source', prompt: 'Review the source.', tools: ['Read'] } } }
});
try {
  for await (const message of q) {
    console.log(message.type);
    if ('parent_tool_use_id' in message) console.log(message.parent_tool_use_id);
    // Native assistant/stream/task messages carry child evidence.
    // Application policy decides when to stop; no inferred Created equivalent.
  }
} finally { q.close(); }
```

## Official Python: separate client send/receive

Original example for the pinned source. Omit `resume` for a new session. Keep `receive_messages()` running if later child output matters; `receive_response()` is a turn helper and stops at ResultMessage. CLI option presence is source evidence, not live verification.

```python
from claude_agent_sdk import (
    ClaudeSDKClient, ClaudeAgentOptions, PermissionResultAllow,
    PermissionResultDeny, AssistantMessage, AgentDefinition,
)

async def run(ask_human, resume=None):
    async def approve(name, tool_input, context):
        if name == "AskUserQuestion":
            answers = {q["question"]: await ask_human(q["question"]) for q in tool_input["questions"]}
            return PermissionResultAllow(updated_input={**tool_input, "answers": answers})
        return PermissionResultDeny(message="Approval required from the application user.")
    options = ClaudeAgentOptions(
        cwd=".", resume=resume, can_use_tool=approve,
        include_partial_messages=True, forward_subagent_text=True,
        agents={"reviewer": AgentDefinition(description="Review source", prompt="Review the source.", tools=["Read"])},
    )
    async with ClaudeSDKClient(options=options) as client:
        await client.query("Explain this directory.")
        async for message in client.receive_messages():
            if isinstance(message, AssistantMessage):
                print(message.content, message.parent_tool_use_id)
            # Application policy decides when to stop; context exit disconnects.
```

These examples describe API usage and were not executed against a model. Go compilation is covered by the independent consumer and examples. TypeScript is checked against the pinned declarations; Python is source-reviewed and syntax-checked, not runtime-verified.
