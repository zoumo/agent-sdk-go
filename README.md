# agent-sdk-go

Native Go SDKs for Codex and Claude in one module and one release stream.

```go
import (
    "github.com/zoumo/agent-sdk-go/agent"
    agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
    agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
    codex "github.com/zoumo/agent-sdk-go/codex"
    codextypes "github.com/zoumo/agent-sdk-go/codex/types"
    claude "github.com/zoumo/agent-sdk-go/claude"
    claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
    "github.com/zoumo/agent-sdk-go/mcp"
)
```

The native `codex` and `claude` packages remain independent, first-class SDKs.
The optional `agent` layer provides one live `Core`/`Session` contract above
them. It is not a root client and does not replace native protocols, raw values,
or history queries. `agent/codex` and `agent/claude` are separate adapters and do
not import each other. Both native `types` packages and the `agent` core depend
only on the Go standard library. The old module import paths have no compatibility
forwarding packages.

| Start here | Documentation |
|---|---|
| Use an SDK | [Codex](codex/README.md), [Claude](claude/README.md) |
| Use one live API for both | [Unified Agent Core](agent/doc.go), [offline session example](agent/core_example_test.go), [offline MCP example](agent/mcp_example_test.go) |
| Understand the design | [Harness](harness/index.md), [shared terms](GLOSSARY.md) |
| Test, record or inspect evidence | [Offline validation](docs/testing.md), [SDK simulation](tests/simulation/drivers/README.md), [pinned test library](https://github.com/zoumo/agent-sdk-testify) |
| Migrate or release | [Migration](docs/migration.md), [release checks](docs/testing.md#release-preparation) |
| Check licensing | [Licenses and native attribution](NOTICE) |

## Optional unified live layer

`agentcodex.New(agentcodex.Config{...})` and
`agentclaude.New(agentclaude.Config{...})` return concrete `*agentcodex.Core` and
`*agentclaude.Core` values that implement `agent.Core`. Start the selected Core,
then use `Open` or `Resume` to get
an `agent.Session`. `Cwd` must be absolute. A session exposes operations and one
event stream to one consumer:

```go
func consume(ctx context.Context, core agent.Core, cwd string) error {
    if _, err := core.Start(ctx); err != nil {
        return err
    }
    session, err := core.Open(ctx, agent.OpenOptions{Cwd: cwd})
    if err != nil {
        return errors.Join(err, core.Stop(ctx))
    }
    events, err := session.Events()
    if err != nil {
        return errors.Join(err, session.Close(ctx), core.Stop(ctx))
    }
    receipt, err := session.StartOrSteer(ctx, agent.Input{
        Content: []agent.ContentBlock{
            {Text: &agent.ContentBlockText{Text: "Explain this change."}},
        },
    })
    if err != nil {
        return errors.Join(err, session.Close(ctx), core.Stop(ctx))
    }
    if receipt.Result == agent.InputNotApplied {
        return errors.Join(
            errors.New("input was not applied; the caller still owns it"),
            session.Close(ctx),
            core.Stop(ctx),
        )
    }
    idle := false
    for !idle {
        event, err := events.Next(ctx)
        if errors.Is(err, io.EOF) {
            return errors.Join(
                errors.New("event stream ended before idle"),
                session.Close(ctx),
                core.Stop(ctx),
            )
        }
        if err != nil {
            return errors.Join(err, session.Close(ctx), core.Stop(ctx))
        }
        switch payload := event.Payload.(type) {
        case agent.InteractionRequestedPayload:
            if payload.Request.Kind == agent.InteractionPermission {
                _, err := session.Answer(ctx, payload.Interaction, agent.PermissionAnswer{
                    Outcome:  agent.PermissionSelected,
                    OptionID: payload.Request.Options[0].OptionID,
                })
                if err != nil {
                    return errors.Join(err, session.Close(ctx), core.Stop(ctx))
                }
            }
        case agent.StateUpdatePayload:
            idle = payload.State != nil && *payload.State == agent.StateIdle
        }
    }
    if err := session.Close(ctx); err != nil {
        return errors.Join(err, core.Stop(ctx))
    }
    return core.Stop(ctx)
}
```

To append context without requesting a model response, assert the optional
`agent.Injector` interface and call `Inject(ctx, input)`. Both adapters implement
it; unsupported native versions return `agent.ErrorUnsupported` without a prompt
fallback. The receipt describes acceptance, not model consumption. See the
[inject contract](harness/modules/unified-agent/design.md#context-inject) for
content, lifecycle, concurrency and version limits.

Claude `Open` generates a UUID and supplies it through the official
`--session-id` creation option, following the ACP adapter's startup mechanism.
After the native initialize exchange succeeds, `ID()` is nonempty and stable
without user input. Initialize does not echo the ID, and Open does not wait for
`system/init` or a model turn. Later native main-session messages must match the
assigned identity; a live, unique init must also identify the requested working
directory. A conflict terminates the session with `native_rejected`.

Use `core.Resume(ctx, agent.ResumeOptions{SessionID: savedID, OpenOptions:
options})` to restore an existing native session; the returned `agent.Session`
has the same API. Resume restores execution and never converts saved content into
live events. The Core must reject `Stop` while any session remains open.

History stays native: use the Codex thread/turn/item queries,
`(*agentcodex.Core).NativeClient()` for same-connection read-only access, or
Claude's package-level session/message/subagent queries. There is deliberately no
unified history, `LoadSession`, replay, or root convenience client. MASS resource
identity, history import, persistence, and multi-session aggregation also remain
outside this module.

### Restrict local tools

Set `Features.RestrictLocalTools` to apply available native restrictions to
built-in local file read, write and command tools on `Open` or `Resume`.
The default `false` preserves native configuration; repeat the request when
resuming.

```go
options := agent.OpenOptions{
    Cwd: "/absolute/workspace",
    Features: agent.Features{RestrictLocalTools: true},
}
```

Claude forwards a native tool deny list. Codex disables shell, image viewing and
image generation, but its public configuration cannot disable `apply_patch` or
provide a complete file-tool deny list. Accordingly,
`session.EffectiveFeatures().RestrictLocalTools` reports whether the adapter
configured the restrictions, including the partial Codex mapping. It does not
mean every local tool is disabled. These controls apply to known built-in tools;
MCP, hooks and the CLI's own file access are outside their scope. The
[unified contract](harness/modules/unified-agent/design.md#restrict-local-tools)
defines the mappings and gaps.

### Additional system prompt

`agent.OpenOptions.AdditionalSystemPrompt` adds optional caller text after
native defaults for one `Open` or `Resume` call; it never replaces built-in
instructions. The complete validation, adapter mapping, resume and disclosure
contract is in the
[authoritative unified-agent design](harness/modules/unified-agent/design.md#additional-system-prompt).

```go
options := agent.OpenOptions{
    Cwd:                    "/absolute/workspace",
    AdditionalSystemPrompt: "Follow the repository conventions.\nNever rewrite generated files.",
}
```

### External MCP servers

The authoritative developer contract is
[unified Agent MCP configuration](harness/modules/unified-agent/design.md#mcp-configuration).
This section is the user-facing usage summary.

`mcp.Server` is the one external MCP server configuration shared by the native
Codex and Claude SDKs and the adapter-neutral agent layer. All three entry points
accept `[]mcp.Server`. `agent.OpenOptions.MCPServers` is the complete final list
for one `Open` or `Resume` call. The component that interprets Runtime
configuration must compose Runtime defaults with the current Session declaration
before invoking the Core:

1. Match `Name` case-sensitively.
2. Keep servers found only in the Runtime defaults.
3. Replace a Runtime server as a whole when the Session declares the same name.
4. Append servers found only in the Session declaration.
5. Treat an empty Session list as no Session override.
6. Do not invent a tombstone, disable marker, or per-name deletion operation.

The Core receives only this final list and performs no second merge. Resume
must receive the full final list again; the unified SDK does not persist or restore
MCP configuration from a previous Session.

The shared configuration requires an explicit transport. The unified layer
accepts `mcp.TransportStdio` (`"stdio"`) and `mcp.TransportHTTP` (MCP Streamable
HTTP), and rejects `mcp.TransportSSE`. It has no `MCPConfigMode`,
`StrictMCPConfig`, `SDKMCPServers`, or ambient-clear switch. Native Claude still
exposes SSE, strict configuration, and application-hosted SDK MCP options through
the `claude` package.

Validation runs before native side effects and does not infer a transport:

- `Name` must be nonempty valid UTF-8, contain no dot, and be unique in the list.
- `Transport` must be explicitly `stdio` or `http`.
- `stdio` requires a nonempty `Command` and forbids `URL`, `Headers`,
  `EnvHeaders`, and `BearerTokenEnvVar`.
- `http` requires a nonempty `URL` and forbids `Command`, `Args`, `Env`, and
  `Cwd`.
- Names, text fields, arguments, map keys, and map values must be valid UTF-8.

```go
options := agent.OpenOptions{
    Cwd: "/absolute/workspace",
    MCPServers: []mcp.Server{
        {
            Name:      "local-tools",
            Transport: mcp.TransportStdio,
            Command:   "mcp-local",
            Args:      []string{"--stdio"},
            Env:       map[string]string{"LOCAL_TOOLS_MODE": "read-only"},
        },
        {
            Name:              "remote-tools",
            Transport:         mcp.TransportHTTP,
            URL:               "https://mcp.example.invalid/tools",
            Headers:           map[string]string{"X-Workspace": "sdk"},
            EnvHeaders:        map[string]string{"Authorization": "REMOTE_AUTHORIZATION"},
            BearerTokenEnvVar: "REMOTE_TOKEN",
        },
    },
}
if err := options.Validate(); err != nil {
    return err
}
```

Codex maps the final fields to `thread/start.config` or
`thread/resume.config`: `Command`, `Args`, `Env`, and `Cwd` for stdio; `URL`,
`http_headers`, `env_http_headers`, and `bearer_token_env_var` for HTTP. The
unified Claude adapter maps the common stdio fields and HTTP `URL`/`Headers`; it
rejects `Cwd`, `EnvHeaders`, and `BearerTokenEnvVar` before process launch.

Codex ambient MCP is field-level recursively merged with declared servers, so a
same-name ambient server can retain undeclared fields or cause a transport
conflict. A warm loaded thread may ignore new config on resume. The SDK does not
promise ambient replacement or live MCP tool-set changes; use an isolated
`CODEX_HOME` or outer execution environment when the server set must be
deterministic. Claude's same-name precedence against native ambient configuration
is target-CLI behavior that can vary by version and policy. Claude enterprise
policy may reject dynamic or strict MCP configuration; the unified adapter does
not override native policy. Isolate the Claude native configuration or outer
execution environment when the server set must be deterministic.

The SDK forwards `Env` and `Headers` without resolving them. `EnvHeaders` values
and `BearerTokenEnvVar` are environment-variable names, not secret values. The SDK
does not persist MCP configuration in Session state, place it in unified Events or
Meta, or log its values. MCP validation and adapter field-support errors identify
only the server position, field, provider, and reason, and do not echo MCP values.
An Open or Resume error can wrap a native peer as its cause, and that native error
may retain sensitive CLI diagnostics. Claude's `--mcp-config` is process argv.
Use native environment references where appropriate and protect process inspection
and native diagnostics. Prefer Codex `EnvHeaders`/`BearerTokenEnvVar` over literal
credentials. A successful Open or Resume does not prove that an MCP server
connected, authenticated, discovered tools, or executed them.

`go test ./agent -run '^ExampleCore$'` runs the adapter-neutral flow twice
with in-memory synthetic fake Cores. It consumes a message, answers an
interaction, waits for idle, closes, resumes, and stops without launching a real
CLI. This example validates the shared interface only; adapter behavior is
covered by the `agent/codex` and `agent/claude` integration tests.
`go test ./agent -run '^ExampleOpenOptions_mcpServers$'` validates an offline MCP declaration
for both transports without resolving a secret or connecting to a server.

Go **1.26** or newer is required. The root `mise.toml` is optional for local
tool management. Formatting and linting use the root Makefile:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
go mod download
make fmt
make lint
bash scripts/check-offline.sh
```

Native contract validation runs offline; real agent/model tests remain disabled.
External consumer CI retrieves the exact
pushed module and dependencies over the network, then compiles public imports,
examples and documentation snippets without invoking a real CLI or model.
Simulation catalog availability and source inspection do not establish successful
real recordings. Code integration tests use explicitly synthetic input.
The offline entry links the exact pinned testify module and its embedded fixed
corpus; absent required recordings fail the simulation gate. Material adoption
and catalog maintenance belong to the
[pinned test library](https://github.com/zoumo/agent-sdk-testify). The test-only pin is
`github.com/zoumo/agent-sdk-testify v0.1.3-0.20261111143023-90fa757261dd`
with checksum `h1:StU10BzY2cxU5XqcQiqmVFuKfUQkF1st4NDZW3UBnmw=`.
It is linked only by test and simulation hosts; production SDK packages do not
import it. The complete boundary is in
[Go library mode](harness/modules/agent-sdk-testify/library-mode.md).
