# Claude Agent SDK for Go

Shell commands below run from the repository root; see [root validation](../docs/testing.md).

An experimental, independent native Go client for a locally installed Claude Code CLI. No other Go SDK or language runtime is required by the client. Original code is licensed under Apache-2.0.

## Compatibility

Experimental; no CLI version is fully live-validated. The live protocol target is
CLI **2.1.258** / official TypeScript **0.3.258**. Broader Python source references
are separate baselines; exact hashes and licenses remain in
[type sources](docs/type-sources.md). Synthetic tests do not certify live behavior.

This implements a local stdio client with narrow input, permission/question,
model/thinking, application-owned SDK MCP and local-history APIs. It does not
provide full official configuration/control parity, hook callbacks, agent-definition
registration, remote transports, administration, user-dialog/elicitation APIs or
durable recovery. Unknown frame preservation does not implement those operations.
There is no public generic raw-write escape hatch. Inherited CLI configuration may
still affect execution. See [offline checks](../docs/testing.md).

## Session usage

A Session owns the native process and one FIFO. `Open` requests native forwarding;
accepting initialize alone does not prove all children will be forwarded. Running
this example uses your explicitly selected CLI and its configured account. Default
tests compile it and use only offline peers for execution.

```go
package main

import (
 "context"
 "fmt"
 "log"
 claude "github.com/zoumo/agent-sdk-go/claude"
)

func main(){
 ctx:=context.Background()
 session,err:=claude.Open(ctx,claude.SessionOptions{CLIPath:"/explicit/path/to/claude",WorkDir:"/absolute/project"})
 if err!=nil{log.Print(err);return}
 defer session.Abort()
 submitted,err:=session.Submit(ctx,claude.Input{Text:"Explain this directory."})
 if err!=nil{log.Print(err);return}
 fmt.Println("complete local write",submitted.Written)
 for{
  received,err:=session.Receive(ctx);if err!=nil{log.Print(err);return}
  if received.Request!=nil{
   fmt.Println("decision required",received.Request.ID(),received.Request.Handle().ToolName())
   // Pass the original request to your UI. The SDK does not choose its answer.
  }
  if received.Event!=nil{fmt.Println(received.Event.Type,string(received.Event.Raw))}
  if received.Observation!=nil{fmt.Println(received.Observation.Reason)}
 }
}
```

`Submit` retains native text or structured user content: text, image, document and
tool_result. Use either Text or User, never both. Image sources support base64/url;
document sources support base64/url/text/content. Unsupported variants fail before
writing. Success proves a complete local write, with no invented CLI acceptance receipt or Claude Turn.
The main envelope is fixed; session_id and parent_tool_use_id are not arbitrary
input targets. A native result, including is_error, does not terminate Session.

`Receive` is the sole consumption entry for native Event, real IncomingRequest,
observation evidence. Concurrent calls compete for the same
FIFO. Applications own UI/logging fan-out. Native Raw/unknown values and repeated
UUIDs remain intact. Confirmed members do not wait for a complete parent path.
Unknown envelopes and unconfirmed identities remain bounded `Unattributed`
diagnostics with explicit coverage gaps; later evidence does not replay old facts.
See [child observation](#child-observation).

## Actual permission and question requests

Use the `IncomingRequest` delivered by Receive. `Approve(ApprovalOptions)` retains
updatedInput and updatedPermissions. `Deny(DenialOptions)` retains message and
interrupt. `Answer(QuestionAnswers)` answers a real AskUserQuestion using full
question-text keys and preserves the original questions and unknown input fields.
`Handle()` retains the original native PermissionRequest for explicit supported
native responses. A parsed event, relationship or history record has no authority.

No default answer or SDK approval deadline is imposed on Session requests. The
registry enforces cancellation, single use and invalidation at close. A queued
answer canceled before writing remains NotSent; a started write may be
OutcomeUnknown and is never automatically retried. Local NotSent rejection leaves
a still-live handle reusable. Keep receiving while a person decides.
Refusing a permission is not a sandbox or an interception guarantee for every tool.

## Configuration and stored records

Creation Model/Thinking options are separate from dynamic SetModel/SetThinking.
Creation Effort passes a nonempty native --effort value literally; empty omits it.
It does not map to a dynamic thinking budget. Dynamic Model nil omits the field;
empty non-nil strings are invalid. Thinking budget nil clears with null; zero stays
zero. Display omission, summarized/omitted and explicit clear remain distinct.
A configuration acknowledgment does not prove the active model or children changed.

Open with ResumeID requests native continuation in a fresh process; SessionID
reports observed identity separately. Initialization does not prove recovery.
Use [saved-history queries](#saved-history) for independent content reads.

## Cancellation, shutdown and resources

Canceling Receive cancels only that wait. Interrupt waits for a matched native
control acknowledgment, not execution completion or rollback. Close invalidates
owned handles and reaps owned resources without waiting for event consumption.
Done closes at interaction termination; Err preserves its first cause. Close
reports cleanup separately. Accepted facts remain drainable; Abort explicitly
abandons unread backlog. No automatic reconnect, resend or request restoration
is implemented.

The Session FIFO grows with unread events and is limited by available memory;
there is no crash/OOM delivery guarantee. Frame sizes, pending controls, request
identities, tool/member indexes, diagnostic stderr and quarantine have independent
limits. Zero resource options select defaults; invalid negative values fail.
Subagents limits bound membership, optional parent knowledge and quarantine
(default 128 records, 4 MiB, five seconds). Parent rejection does not discard
already confirmed native facts. No occurrence/UUID ledger or ancestor-first
sorting is required for normal Session delivery.

Env=nil inherits the native environment; an explicit Env replaces it. Process
options are literal argv, not shell commands. The SDK does not bundle a CLI or
manage accounts. POSIX cleanup targets the owned process group; non-POSIX
cleanup owns only the direct child. Escaped process groups remain outside that
bound. See [compatibility](#compatibility) for native and evidence limits.

## Types, licensing and offline checks

Native pure data and codecs live in `claude/types` with standard-library-only
production dependencies. Unknown JSON fields/blocks are retained; handwritten
types do not imply full official SDK parity. Original code uses Apache-2.0; see
[NOTICE](NOTICE), [LICENSE](LICENSE) and [type provenance](docs/type-sources.md).

Validation commands and evidence limits are maintained in [testing](../docs/testing.md)
and [recording provenance](../tools/record-user-story/README.md#fixtures-仿真录制与测试输入).
Breaking changes are in the [migration guide](../docs/migration.md#session).

## Child observation

Exact CLI 2.1.258 init plus local_agent task_started with task/tool IDs establishes
an alias; forwarded parent_tool_use_id may use that alias. Unknown agent_id,
parent_task_id and run_id remain raw extensions, not identity or order evidence.
The parent graph rejects cycles/conflicts/depth excess without revoking membership.
MaxNodes defaults to 4096 (maximum 65536); MaxDepth defaults to 32 (maximum 256).
Other diagnostic defaults are described above. No successful live child forwarding
or combined parent/child completion is certified.

Set ChildDetailPolicy to ChildDetailSuppress at Open, including ResumeID, to omit
reviewed pure child detail. Empty/ChildDetailPreserve retains it; invalid values
fail before spawn. Initialize still requests forwardSubagentText:true. Mixed,
unknown, tool, permission and terminal frames remain intact. This is neither
redaction nor token-cost control; see the
[closed filtering contract](../harness/modules/native-session/design.md#child-detail-可选子详细事件交付).

```sh
# From the repository root; executes only a synthetic peer:
mise exec -- go run ./claude/examples/subagents-offline
```

## Saved history

Use explicit scope to discover metadata and then read saved content, without a CLI:

```go
scope := claude.SessionQueryScope{ConfigDir: "/explicit/config", ProjectDir: "/absolute/project"}
page, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 100})
// Check err and page.Coverage; partial metadata may accompany errors.
info, err := claude.GetSessionInfo(ctx, sessionID, claude.SessionInfoOptions{Scope: scope})
// Check err and info.Coverage; Info may be nil for explicitly excluded storage.
messages, err := claude.GetSessionMessages(ctx, sessionID, claude.SessionMessagesOptions{Scope: scope})
// No messages are returned on error. Each call is independent.
```

The supported layout is `ConfigDir/projects/<project-key>/<UUID>.jsonl` plus the
selected parent's subagents tree. No implicit HOME, worktree expansion, remote
store or arbitrary transcript path is used. Limit/Offset slice results; each call
can scan the bounded source again. Metadata samples head/tail; complete metadata
does not validate the unread middle or prove running. Main messages recover
compaction/parallel-tool structure; child messages follow their own last-leaf chain.
Queries return detached data and never create live membership or answer authority.

Main defaults: metadata page 100, message page 1000; source and inventory limits,
strict parsing differences, errors and mutation checks are specified in the
[history contract](../harness/contracts/history-query.md). Atomic is always false;
there is no shared snapshot across pages. Inspect errors and Coverage together.

For child content, call package-level `ListSubagents(ctx, parentSessionID, options)`
and `GetSubagentMessages(ctx, parentSessionID, agentID, options)` with the same
explicit scope. All child branches are checked for duplicate IDs, cycles and
missing parents; the [child-history contract](../harness/contracts/history-query.md#children-claude-独立本地子历史)
defines limits, containment and mutation checks. Raw data and sidecars can be private.

Session.ListSubagents instead uses Open's captured environment/cwd and observed
main identity. It returns partial records with issues, samples metadata and permits
append activity. Defaults are 10000 records, 64 MiB bytes, 100000 metadata records,
100000 entries, depth 64 and 64 KiB per-file sample. It neither requires a frozen
main chain nor replaces independent package-level ListSubagents/GetSubagentMessages.

```sh
# From the repository root; uses temporary synthetic storage:
mise exec -- go run ./claude/examples/session-metadata-offline
mise exec -- go run ./claude/examples/session-messages-offline
```

## Message JSON

Event keeps its wire envelope: message content lives inside message, stream deltas
inside event. Content can be a string or ordered blocks; preserve unknown blocks
and large numeric literals. Check result.is_error as well as subtype; a result
does not close Session. Native cumulative cost is not a per-message increment.

Raw retains unknown JSON. Typed edits override supported fields; Fields specifies
Default/Present/Null/Absent where a zero Go value cannot express wire presence.
Use the native codecs rather than rebuilding JSON through map[string]any/float64.
Detached permission/question codecs are data only and do not grant answer authority;
runtime answers accept native string-valued answers, not output-only fields or
unverified array extensions. See the [codec implementation](types/messages.go).

## Go: configuration, session, input, streaming and approvals

The application owns its decision UI. Requests arrive in the same FIFO as
native events; a decision uses the actual IncomingRequest or its native handle.

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

## Application SDK MCP servers

Set `SessionOptions.SDKMCPServers` to explicit application server names. Native `mcp_message` controls for those names arrive through `Receive` as original `IncomingRequest` handles. `MCPRequest()` returns the server name and exact JSON-RPC message; implement native initialize, notifications, tools/list and tools/call in the application. `ReplyMCP(ctx, response)` wraps the unchanged JSON-RPC object in native `mcp_response`, validates its original ID, and consumes only that handle. A notification has no inner ID but its outer control still requires acknowledgment. `Approve`, `Deny` and permission `Answer` are separate and reject this request kind. Invalid answers retain authority; canceled/closed/used handles cannot answer again. Unknown delivery outcomes are not retried.

The SDK forwards a native `--mcp-config` declaration with `type:sdk` for each name, without starting an external server, adding a callback, or implementing tool dispatch. Names are frozen at Open. Other server names and unsupported controls retain the existing explicit rejection behavior. See [test input and simulation boundaries](../tools/record-user-story/README.md#fixtures-仿真录制与测试输入) for the evidence distinction.
