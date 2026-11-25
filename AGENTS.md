# Agent guide

Read the standards relevant to the task:

- SDK, protocol, module, dependency or path changes: [development standards](docs/architecture.md).
- Tests, helpers or generators: [testing standards](docs/testing-standards.md).
- Recording evidence: [recording and fixture provenance](tests/simulation/README.md#fixtures-仿真录制与测试输入).

Use Go 1.26 or newer. `mise.toml` is an optional local convenience, not a
project requirement. Preserve native protocol contracts and recording
provenance. Default validation is offline; this guide does not authorize real
agent execution or new recordings.

Documentation changes follow the [documentation rules](docs/AGENTS.md). Find design contracts in the [documentation index](docs/index.md).
