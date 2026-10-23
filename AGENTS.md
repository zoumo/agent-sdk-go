# Agent guide

Read the standards relevant to the task before changing code:

- SDK, protocol, module or dependency changes: [architecture](harness/architecture.md).
- Tests, test helpers or generators: [testing](harness/testing.md).
- Commands, tools, examples or fixture paths: [code layout](harness/code-layout.md); for recording evidence also read the [fixture index](docs/recordings.md).

Use the mise-managed compiler pinned in `mise.toml`. Preserve native protocol
contracts and recording provenance. Default validation is offline; this guide
does not authorize real agent execution or new recordings.

## Agent Harness

开始工作前，读取并遵循 [harness 工作规则](harness/AGENTS.md)。
项目设计从 [harness 入口](harness/index.md)读取。
