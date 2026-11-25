# Unified Agent

Unified `Core.Open` and `Core.Resume` default to `fullAccess`. If your Codex or
Claude application relied on automatic review, set `PermissionPreset` to
`autoReview` on both calls. Pi accepts only `fullAccess`.

Run the example from the repository root. The example uses synthetic providers.

```sh
go test -count=1 -v ./agent -run '^ExampleCore$'
```

The two example providers report a reply, close the session, resume it by its
saved ID, and stop. Follow the same lifecycle in your application.
[ExampleCore source and expected output](core_example_test.go)

1. Create a Core with `agentcodex.New`, `agentclaude.New`, or `agentpi.New`.
   Call `Core.Start` to initialize it and get its capabilities.
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

Read your adapter’s setup and capabilities in its package documentation.
[Codex](codex/doc.go) · [Claude](claude/doc.go) · [Pi](pi/core.go)

For provider-specific behavior, see the [Codex tool event mapping](codex/docs/tool-events.md) and [Claude session contract](claude/docs/session.md).

Unified Claude `Open` returns the native session ID from `claim_session` before
the first input. Local startup checks for a stable Claude CLI at version 2.1.293
or newer. [Session identity and startup contract](docs/session.md)

To append context without requesting a model response, check whether the
session implements `agent.Injector`, then call `Inject`. The receipt records
acceptance. An unsupported native version returns `agent.ErrorUnsupported`.
[Context injection contract](docs/session.md#context-inject)

## Restrict local tools

Set `OpenOptions.Features.RestrictLocalTools` when you open or resume a session.
`Session.EffectiveFeatures` reports whether the adapter configured its native
restrictions. Claude sends a tool deny list. Codex disables the shell, image
viewing, and image generation features. The Codex mapping still permits
`apply_patch` where the model exposes it. Pi passes `--no-tools`.
[Mappings and limits](docs/session.md#restrict-local-tools)

## Additional system prompt

Set `OpenOptions.AdditionalSystemPrompt` to add text to the native system
prompt. Supply the value on each `Open` or `Resume` call that needs it.
[Additional system prompt contract](docs/session.md#additional-system-prompt)

## External MCP servers

Set `OpenOptions.MCPServers` to the full list of external servers for an `Open`
or `Resume` call. Declare each server as `mcp.TransportStdio` or
`mcp.TransportHTTP`. If your application combines runtime defaults with session
settings, compose the list before calling the Core. Match names case-sensitively
and replace a same-name server as a whole. Send the full list again on resume.

Use `mcp.Literal(name, value)` for explicit entries in `Env` or `Headers`. To
read a value from a file, set `ValueFrom.FileKeyRef` to an absolute path and
key. `EnvFrom` and `HeadersFrom` import all entries from a file. For paths from
other callers, pass a restricted filesystem such as `os.Root.FS()` through
`OpenOptions.MCPFiles` and use relative paths. The SDK resolves files again on each open or
resume and passes the resolved values through the native MCP configuration.

Run the offline configuration example from the repository root. The test
validates the declared fields for both transports.

```sh
go test -count=1 -v ./agent -run '^ExampleOpenOptions_mcpServers$'
```

If your application requires a fixed set of servers, isolate the native
configuration. Verify connectivity and tool availability through the native
provider after opening the session. Check provider support and configuration
precedence before adding credentials or resuming a session.
[Codex MCP options](../client/codex/README.md#thread-mcp-servers) ·
[Claude MCP options](../client/claude/README.md#external-mcp-servers) ·
[Unified MCP contract](docs/session.md#mcp-configuration) ·
[Example source](mcp_example_test.go)
