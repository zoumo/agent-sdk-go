# Agent guide

Read the standards relevant to the task before changing code:

- SDK, protocol, module or dependency changes: [architecture](docs/standards/architecture.md).
- Tests, test helpers or generators: [testing](docs/standards/testing.md).
- Commands, tools, examples or fixture paths: [code layout](docs/standards/code-layout.md); for recording evidence also read the [fixture index](docs/recordings.md).

Use the mise-managed compiler pinned in `mise.toml`. Preserve native protocol
contracts and recording provenance. Default validation is offline; this guide
does not authorize real agent execution or new recordings.
