# agent-sdk-go

Go clients for the Codex app-server and Claude Code CLI. Both SDKs use the
`github.com/zoumo/agent-sdk-go` module and require Go 1.26 or newer.

| If you need | Use | Get started |
|---|---|---|
| Codex protocol types, turn operations, or saved thread queries | `github.com/zoumo/agent-sdk-go/codex` | [Codex SDK](codex/README.md) |
| Claude protocol types, CLI options, or saved session queries | `github.com/zoumo/agent-sdk-go/claude` | [Claude SDK](claude/README.md) |
| The same live session API for Codex and Claude | `github.com/zoumo/agent-sdk-go/agent` and one adapter per Core | [Run the unified session example](#optional-unified-live-layer) |

The native SDKs have separate `codex/types` and `claude/types` packages. Use the
native SDK for protocol-specific controls and saved history even when your
application uses `agent` for live sessions. See the [dependency rules](docs/architecture.md).

| If you are | Read |
|---|---|
| Moving from the earlier standalone modules | [Migration guide](docs/migration.md) |
| Testing an SDK change | [Validation guide](docs/testing.md) |
| Running an SDK against a recorded case | [Simulation guide](tests/simulation/drivers/README.md) |
| Looking up contracts or terms | [Documentation index](docs/index.md), [glossary](GLOSSARY.md) |
| Preparing a release | [Release checks](docs/testing.md#release-preparation), [licenses and attribution](NOTICE) |

## Optional unified live layer

Run the example from the repository root. The example uses synthetic providers.

```sh
go test -count=1 -v ./agent -run '^ExampleCore$'
```

The test passes after both providers report a reply, close the session, resume
it by its saved ID, and stop. Follow the same lifecycle in your application.
[ExampleCore source and expected output](agent/core_example_test.go)

1. Create a Core with `agentcodex.New` or `agentclaude.New`. Call `Core.Start`
   to initialize it and get its capabilities.
2. Call `Core.Open` with an absolute `OpenOptions.Cwd`. To restore an existing
   native session, call `Core.Resume` with its saved `SessionID` and the open
   options. Use the native SDK to read saved content.
3. Call `Session.Events` once. Assign one consumer to read the event stream
   with `Next` and distribute events to the rest of your application.
4. Call `Session.StartOrSteer` to submit input. Check the receipt before
   correlating events with the submission. If the result is `InputNotApplied`,
   your application still owns the input and controls any retry.
5. When an `InteractionRequestedPayload` arrives, show the request to your user
   or apply your application's policy. Send the resulting answer to
   `Session.Answer` with the original `Interaction` reference. For a permission
   answer, use the `OptionID` of the selected option. The offline example uses
   `allow-once` for its synthetic request.
6. Consume events until your application has the result it needs. Session state
   such as `idle` describes the session. Use the input receipt and the relevant
   events for submission-specific evidence.
7. Call `Session.Close` to release each session, then call `Core.Stop`.
   `Core.Stop` rejects the call while a session is open or an open or resume
   operation is in progress.

Read your adapter's package docs for setup and capabilities.
[Codex adapter](agent/codex/doc.go) · [Claude adapter](agent/claude/doc.go)

Unified Claude `Open` returns the native session ID from `claim_session` before
the first input. Local startup checks for a stable Claude CLI at version 2.1.293
or newer. [Session identity and startup contract](docs/modules/unified-agent/design.md)

To append context without requesting a model response, check whether the
session implements `agent.Injector`, then call `Inject`. The receipt records
acceptance. An unsupported native version returns `agent.ErrorUnsupported`.
[Context injection contract](docs/modules/unified-agent/design.md#context-inject)

### Restrict local tools

Set `OpenOptions.Features.RestrictLocalTools` when you open or resume a session.
`Session.EffectiveFeatures` reports whether the adapter configured its native
restrictions. Claude sends a tool deny list. Codex disables the shell, image
viewing, and image generation features. The Codex mapping still permits other
local capabilities, including `apply_patch` where the model exposes it.
[Mappings and limits](docs/modules/unified-agent/design.md#restrict-local-tools)

### Additional system prompt

Set `OpenOptions.AdditionalSystemPrompt` to add text to the native system
prompt. Supply the value on each `Open` or `Resume` call that needs it.
[Additional system prompt contract](docs/modules/unified-agent/design.md#additional-system-prompt)

### External MCP servers

Set `OpenOptions.MCPServers` to the full list of external servers for an `Open`
or `Resume` call. Declare each server as `mcp.TransportStdio` or
`mcp.TransportHTTP`. If your application combines runtime defaults with session
settings, compose the list before calling the Core. Match names case-sensitively
and replace a same-name server as a whole. Send the full list again on resume.

Run the offline configuration example from the repository root. The test
validates the declared fields for both transports.

```sh
go test -count=1 -v ./agent -run '^ExampleOpenOptions_mcpServers$'
```

If your application requires a fixed set of servers, isolate the native
configuration. Verify connectivity and tool availability through the native
provider after opening the session. Check provider support and configuration
precedence before adding credentials or resuming a session.
[Codex MCP options](codex/README.md#thread-mcp-servers) ·
[Claude MCP options](claude/README.md#external-mcp-servers) ·
[Unified MCP contract](docs/modules/unified-agent/design.md#mcp-configuration) ·
[Example source](agent/mcp_example_test.go)
