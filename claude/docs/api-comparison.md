# Go and official Agent SDK API comparison

CLI target **2.1.258**. Examples below are original usage examples, not copied upstream implementation. No model calls were run.

## Fixed sources

- TypeScript **0.3.258**: [versioned declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk.d.ts), [official npm archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz). Static local declarations inspected; SHA256 `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`. See [provenance](type-sources.md). Its `query()` and `Query` contract are the comparison target; no unstable session API is assumed.
- Python **commit f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab**: [client](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py), [streaming examples](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/examples/streaming_mode.py), [types](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py). This source targets CLI **2.1.302**, not 2.1.258. Python objects flatten wire messages; Go preserves stream-json envelopes. This is a separate source comparison, not matching-version parity.

## Surface comparison

| Task | Go | Official TypeScript 0.3.258 | Official Python pinned commit |
| --- | --- | --- | --- |
| Configure | `SessionOptions{CLIPath, WorkDir, Model, Thinking, Subagents}`; explicit `LoadOptions` for recovery | `query({prompt, options:{cwd, model, resume, canUseTool}})` | `ClaudeAgentOptions(cwd=..., model=..., resume=..., can_use_tool=...)` |
| Create | `Open(ctx, SessionOptions)` returns `*Session` | `query(...)` starts query/process | `await client.connect()` or async context manager |
| Resume | explicit `Load(ctx, LoadOptions)`; fixed baseline fails `ErrResumeUnconfirmed` | `options.resume` on new query | `ClaudeAgentOptions(resume=id)` on new client |
| Send | `session.Submit(ctx, Input{Text:text})` | string or async prompt iterable; `Query.streamInput` also declared | `await client.query(text)` |
| Receive | `(Received,error)`; Event, real Request, Observation or Load boundary | async iteration yields `SDKMessage` union | `receive_messages()` yields messages; `receive_response()` stops at result |
| Approve/question | real `IncomingRequest`; Approve/Deny/Answer and original native handle | `canUseTool`; updated input for questions; separate `onUserDialog` contract | `can_use_tool` and permission results; updated input for questions |
| Children | Session native forwarding, task/tool evidence and optional parent knowledge | `forwardSubagentText`, `agents`; native parent/tool/task evidence | `forward_subagent_text`, `agents`; parsed messages |
| Close | `session.Close()` | `q.close()` | `await client.disconnect()` or context exit |

Go does not expose the official `agents` configuration used in the examples below; child relationships are observed automatically from the owned CLI stream. Go does not implement the entire official configuration, agent-definition registration, hooks, MCP server configuration, history query helpers, or TypeScript `onUserDialog`/elicitation surface. Receiving unknown frames losslessly does not implement their response APIs. Native `Event.Origin` stays an official object; derived records carry their own provenance. Derived conveniences are implemented by Go and are not official message variants.

## Go: receiving events

```go
received, err := session.Receive(ctx) // (claude.Received, error)
if err == nil {
    if received.Event != nil { fmt.Println(received.Event.Type) }
    if received.Request != nil { fmt.Println(received.Request.ID()) }
    if received.Observation != nil { fmt.Println(received.Observation.Reason) }
}
```

## Go: configuration, session, input, streaming and approvals

The application owns its decision UI. Requests arrive in the same FIFO as
native events; a decision uses the actual IncomingRequest or its native handle.
Load is explicit and cannot succeed at the fixed baseline without a confirmed
non-executing recovery mechanism. It does not replace missing recovery with
ordinary input.

```go
func consume(ctx context.Context, cwd string,
    decide func(*claude.IncomingRequest) claude.PermissionAnswer) error {
    session, err := claude.Open(ctx, claude.SessionOptions{CLIPath:"claude",WorkDir:cwd})
    if err != nil { return err }
    defer session.Abort()
    if _,err=session.Submit(ctx,claude.Input{Text:"Explain this directory."});err!=nil{return err}
    for {
        received,err:=session.Receive(ctx)
        if err!=nil{return err}
        if received.Request!=nil{
            answer:=decide(received.Request)
            if err:=received.Request.Handle().Answer(received.Request.Context(),answer);err!=nil{return err}
        }
        if event:=received.Event;event!=nil{
            fmt.Println(event.Type,event.ParentToolUseID)
            if event.Stream!=nil{fmt.Println(event.Stream.Delta)}
        }
        if received.Observation!=nil{fmt.Println(received.Observation.Reason)}
        // Continue after root result when later child output matters.
    }
}

func answerQuestions(request *claude.IncomingRequest,answers map[string]string)error{
    // Pure decoding does not create authority. This is the original live request.
    var input claude.AskUserQuestionInput
    if err:=json.Unmarshal(request.Handle().Input(),&input);err!=nil{return err}
    fmt.Println("questions",len(input.Questions))
    return request.Answer(request.Context(),claude.QuestionAnswers{Answers:answers})
}
```

SessionID reports observed identity separately from the requested resume ID.
Close reaps the owned process and leaves accepted facts drainable; Abort explicitly
abandons unread backlog. The [offline example](../examples/subagents-offline/main.go)
retains exact native facts after a parent result, reports unconfirmed parent
knowledge and closes at scripted idle. Native structures remain independent of
Codex and no synthetic Claude Turn is introduced.

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
