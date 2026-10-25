# Claude Agent SDK for Go

Commands in this native documentation run from `claude/`; see [root validation](../docs/testing.md).

An experimental, independent native Go client for a locally installed Claude Code CLI. No other Go SDK or language runtime is required by the client. Original code is licensed under Apache-2.0.

**Experimental core implemented; not production-validated. Live Claude CLI compatibility: UNKNOWN.** See the [canonical compatibility matrix and complete official feature inventory](docs/compatibility.md) for implemented APIs, synthetic/race evidence, and unsupported features. The current CLI target is **2.1.258**, with official TypeScript contract **0.3.258**. The broader schema reference is the official Python SDK at [`f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab), which references CLI 2.1.302. Synthetic tests do not establish real runtime interoperability or official endorsement.

## Documentation

- [API comparison and examples](docs/api-comparison.md), [message JSON](docs/message-json.md)
- [Compatibility and limitations](docs/compatibility.md), [type/version provenance](docs/type-sources.md)
- [Subagent API and bounds](docs/subagent-events.md), [capability matrix](docs/subagent-compatibility.md)
- [Event sequences / 用户故事](docs/event-sequences.md), [architecture](docs/design/sdk-design.md)
- [Testing and CI](docs/ci.md), [offline recorded replay](docs/record-replay.md), [manual live checklist](docs/live-compatibility.md)

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
tool_result. Unknown input variants are explicitly rejected. Success proves a
complete local write, with no invented CLI acceptance receipt or Claude Turn.
The main envelope is fixed; session_id and parent_tool_use_id are not arbitrary
input targets. A native result, including is_error, does not terminate Session.

`Receive` is the sole consumption entry for native Event, real IncomingRequest,
observation evidence. Concurrent calls compete for the same
FIFO. Applications own UI/logging fan-out. Native Raw/unknown values and repeated
UUIDs remain intact. Confirmed members do not wait for a complete parent path.
Unknown envelopes and unconfirmed identities remain bounded `Unattributed`
diagnostics with explicit coverage gaps; later evidence does not replay old facts.
See [native task/tool evidence](docs/session-migration.md).

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
OutcomeUnknown and is never automatically retried. Refusing a permission is not
a sandbox or an interception guarantee for every tool.

## Configuration and stored records

Creation `Model`/`Thinking` options are separate from dynamic SetModel/SetThinking.
Native acceptance is distinct from observed configuration. Model nil omits the
model field; empty non-nil strings are invalid. Thinking budget nil clears with
null; zero remains zero. Display omission, summarized/omitted and explicit clear
remain separate. See [input/configuration](docs/session-input-config.md).

`ListSubagents` scans the fixed Session's ConfigDir/cwd/native identity with
explicit count/byte bounds. Inspect coverage together with errors; a partial or
unsupported scan is not a complete empty list. Querying stored records does not
resume them, prove they are running or change observation membership.

Native continuation uses `Open` with `SessionOptions.ResumeID`. This requests the
CLI's native resume behavior; initialization does not prove historical recovery.
Independent saved messages use package-level `GetSessionMessages` with an explicit
absolute `SessionQueryScope.ConfigDir`, without Open or a runtime executable. See
[local message queries](docs/session-messages.md),
[session discovery and metadata](docs/session-metadata.md), and the
[offline example](examples/session-messages-offline/main.go). Internal legacy
parser/projection helpers remain independently tested; queries return detached
messages rather than Events.

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
bound. See [compatibility](docs/compatibility.md) for native and evidence limits.

## Types, licensing and offline checks

Native pure data and codecs live in `claude/types` with standard-library-only
production dependencies. Unknown JSON fields/blocks are retained; handwritten
types do not imply full official SDK parity. Original code uses Apache-2.0; see
[NOTICE](NOTICE), [LICENSE](LICENSE) and [type provenance](docs/type-sources.md).

Use the mise-pinned Go 1.26.9 compiler and root offline gate. The subagent example
runs its own synthetic peer; recorded replay preserves the original sanitized
CLI authentication failure. That capture is not successful model/tool/child
coverage. Default live initialization tests remain opt-in/skipped. No live
compatibility follows from a passing synthetic or replay test.

See the [breaking migration guide](../docs/session-migration.md),
[CI checks](docs/ci.md) and [recording provenance](docs/record-replay.md).
