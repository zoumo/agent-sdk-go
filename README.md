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
)
```

The native `codex` and `claude` packages remain independent, first-class SDKs.
The optional `agent` layer provides one live `Provider`/`Session` contract above
them. It is not a root client and does not replace native protocols, raw values,
or history queries. `agent/codex` and `agent/claude` are separate adapters and do
not import each other. Both native `types` packages and the `agent` core depend
only on the Go standard library. The old module import paths have no compatibility
forwarding packages.

| Start here | Documentation |
|---|---|
| Use an SDK | [Codex](codex/README.md), [Claude](claude/README.md) |
| Use one live API for both | [Unified agent core](agent/doc.go), [offline session example](agent/provider_example_test.go), [offline MCP example](agent/mcp_example_test.go) |
| Understand the design | [Harness](harness/index.md), [shared terms](GLOSSARY.md) |
| Test, record or inspect evidence | [Offline validation](docs/testing.md), [SDK simulation](tests/simulation/drivers/README.md), [pinned test library](https://github.com/zoumo/agent-sdk-testify) |
| Migrate or release | [Migration](docs/migration.md), [release checks](docs/testing.md#release-preparation) |
| Check licensing | [Licenses and native attribution](NOTICE) |

## Optional unified live layer

`agentcodex.New(agentcodex.Config{...})` and
`agentclaude.New(agentclaude.Config{...})` return concrete providers that implement
`agent.Provider`. Start the selected provider, then use `Open` or `Resume` to get
an `agent.Session`. `Cwd` must be absolute. A session exposes operations and one
event stream to one consumer:

```go
func consume(ctx context.Context, provider agent.Provider, cwd string) error {
    if _, err := provider.Start(ctx); err != nil {
        return err
    }
    session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
    if err != nil {
        return errors.Join(err, provider.Stop(ctx))
    }
    events, err := session.Events()
    if err != nil {
        return errors.Join(err, session.Close(ctx), provider.Stop(ctx))
    }
    receipt, err := session.StartOrSteer(ctx, agent.Input{
        Content: []agent.ContentBlock{agent.TextBlock{Text: "Explain this change."}},
    })
    if err != nil {
        return errors.Join(err, session.Close(ctx), provider.Stop(ctx))
    }
    if receipt.Result == agent.InputNotApplied {
        return errors.Join(
            errors.New("input was not applied; the caller still owns it"),
            session.Close(ctx),
            provider.Stop(ctx),
        )
    }
    idle := false
    for !idle {
        event, err := events.Next(ctx)
        if errors.Is(err, io.EOF) {
            return errors.Join(
                errors.New("event stream ended before idle"),
                session.Close(ctx),
                provider.Stop(ctx),
            )
        }
        if err != nil {
            return errors.Join(err, session.Close(ctx), provider.Stop(ctx))
        }
        switch payload := event.Payload.(type) {
        case agent.InteractionRequestedPayload:
            if payload.Request.Kind == agent.InteractionPermission {
                _, err := session.Answer(ctx, payload.Interaction, agent.PermissionAnswer{
                    Outcome:  agent.PermissionSelected,
                    OptionID: payload.Request.Options[0].OptionID,
                })
                if err != nil {
                    return errors.Join(err, session.Close(ctx), provider.Stop(ctx))
                }
            }
        case agent.StateUpdatePayload:
            idle = payload.State != nil && *payload.State == agent.StateIdle
        }
    }
    if err := session.Close(ctx); err != nil {
        return errors.Join(err, provider.Stop(ctx))
    }
    return provider.Stop(ctx)
}
```

Use `provider.Resume(ctx, agent.ResumeOptions{SessionID: savedID, OpenOptions:
options})` to restore an existing native session; the returned `agent.Session`
has the same API. Resume restores execution and never converts saved content into
live events. The provider must reject `Stop` while any session remains open.

History stays native: use the Codex thread/turn/item queries,
`(*agentcodex.Provider).NativeClient()` for same-connection read-only access, or
Claude's package-level session/message/subagent queries. There is deliberately no
unified history, `LoadSession`, replay, or root convenience client. MASS resource
identity, history import, persistence, and multi-session aggregation also remain
outside this module.

### External MCP servers

The authoritative developer contract is
[unified Agent MCP configuration](harness/modules/unified-agent/design.md#mcp-configuration).
This section is the user-facing usage summary.

`agent.OpenOptions.MCPServers` is the complete final external MCP list for one
`Open` or `Resume` call. The component that interprets Runtime configuration must
compose Runtime defaults with the current Session declaration before invoking the
provider:

1. Match `Name` case-sensitively.
2. Keep servers found only in the Runtime defaults.
3. Replace a Runtime server as a whole when the Session declares the same name.
4. Append servers found only in the Session declaration.
5. Treat an empty Session list as no Session override.
6. Do not invent a tombstone, disable marker, or per-name deletion operation.

The provider receives only this final list and performs no second merge. Resume
must receive the full final list again; the unified SDK does not persist or restore
MCP configuration from a previous Session.

The only unified transports are explicit `MCPTransportStdio` (`"stdio"`) and
`MCPTransportHTTP` (MCP Streamable HTTP). The unified layer has no SSE transport,
`MCPConfigMode`, `StrictMCPConfig`, `SDKMCPServers`, or ambient-clear switch.
Native Claude still exposes its SSE, strict, and application-hosted SDK MCP
options through the `claude` package.

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
    MCPServers: []agent.MCPServer{
        {
            Name:      "local-tools",
            Transport: agent.MCPTransportStdio,
            Command:   "mcp-local",
            Args:      []string{"--stdio"},
            Env:       map[string]string{"LOCAL_TOOLS_MODE": "read-only"},
        },
        {
            Name:              "remote-tools",
            Transport:         agent.MCPTransportHTTP,
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

`go test ./agent -run '^ExampleProvider$'` runs the provider-neutral flow twice
with in-memory synthetic fake providers. It consumes a message, answers an
interaction, waits for idle, closes, resumes, and stops without launching a real
CLI. This example validates the shared interface only; adapter behavior is
covered by the `agent/codex` and `agent/claude` integration tests.
`go test ./agent -run '^ExampleMCPServer$'` validates an offline MCP declaration
for both transports without resolving a secret or connecting to a server.

Go **1.26** or newer is required. The root `mise.toml` is optional for local
tool management. Formatting and linting use the root Makefile:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
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
`github.com/zoumo/agent-sdk-testify v0.0.0-20261109044915-b606272c88c1`
with checksum `h1:PGGn9abQro2aCssr/tElOCkSHEg6wwNpXEhD5pPn8Ac=`.
It is linked only by test and simulation hosts; production SDK packages do not
import it. The complete boundary is in
[Go library mode](harness/modules/agent-sdk-testify/library-mode.md).
