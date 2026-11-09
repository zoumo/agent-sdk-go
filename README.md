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
| Use one live API for both | [Unified agent core](agent/doc.go), [offline example](agent/provider_example_test.go) |
| Understand the design | [Harness](harness/index.md), [shared terms](GLOSSARY.md) |
| Test, record or inspect evidence | [Offline validation](docs/testing.md), [SDK simulation](tests/simulation/drivers/README.md), [external test tool](https://github.com/zoumo/agent-sdk-testify) |
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
    if _, err := session.Submit(ctx, agent.Input{
        Content: []agent.ContentBlock{agent.TextBlock{Text: "Explain this change."}},
    }); err != nil {
        return errors.Join(err, session.Close(ctx), provider.Stop(ctx))
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

`go test ./agent -run '^ExampleProvider$'` runs the provider-neutral flow twice
with in-memory synthetic fake providers. It consumes a message, answers an
interaction, waits for idle, closes, resumes, and stops without launching a real
CLI. This example validates the shared interface only; adapter behavior is
covered by the `agent/codex` and `agent/claude` integration tests.

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
corpus; absent recordings fail the simulation gate. Material adoption and catalog
maintenance belong to the
[external tool](https://github.com/zoumo/agent-sdk-testify).
