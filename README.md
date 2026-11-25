# agent-sdk-go

Go clients for the Codex app-server, Claude Code CLI, and Pi RPC. The SDKs use the
`github.com/zoumo/agent-sdk-go` module and require Go 1.26 or newer.

| If you need | Use | Get started |
|---|---|---|
| Codex protocol types, turn operations, or saved thread queries | `github.com/zoumo/agent-sdk-go/client/codex` | [Codex SDK](client/codex/README.md) |
| Claude protocol types, CLI options, or saved session queries | `github.com/zoumo/agent-sdk-go/client/claude` | [Claude SDK](client/claude/README.md) |
| Pi native RPC commands, responses, and events | `github.com/zoumo/agent-sdk-go/client/pi` | [Pi SDK](client/pi/README.md) |
| Replay a capture, record a fixture, or install the Replay CLI | `github.com/zoumo/agent-sdk-go/replay` | [Replay](replay/README.md) |
| The same live session API for Codex, Claude, and Pi | `github.com/zoumo/agent-sdk-go/agent` and one adapter per Core | [Unified Agent and examples](agent/README.md) |

The native SDKs have separate `client/codex/types` and `client/claude/types` packages. Use the
native SDK for protocol-specific controls and saved history even when your
application uses `agent` for live sessions. See the [dependency rules](docs/architecture.md).

| If you are | Read |
|---|---|
| Moving from the earlier standalone modules | [Migration guide](docs/migration.md) |
| Testing an SDK change | [Validation guide](docs/testing.md) |
| Running an SDK against a recorded case | [Simulation guide](tests/simulation/README.md) |
| Looking up contracts or terms | [Documentation index](docs/index.md), [glossary](GLOSSARY.md) |
| Preparing a release | [Release checks](docs/testing.md#release-preparation), [licenses and attribution](NOTICE) |
