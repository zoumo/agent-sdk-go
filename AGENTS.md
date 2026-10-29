# Agent guide

Read the standards relevant to the task:

- SDK, protocol, module, dependency or path changes: [development standards](harness/architecture.md).
- Tests, helpers or generators: [testing standards](harness/testing.md).
- Recording evidence: [recording and fixture provenance](tools/record-user-story/README.md#fixtures-仿真录制与测试输入).

Use the mise-managed compiler pinned in `mise.toml`. Preserve native protocol
contracts and recording provenance. Default validation is offline; this guide
does not authorize real agent execution or new recordings.

开始工作前读取 [harness 工作规则](harness/AGENTS.md)，设计从 [harness 入口](harness/index.md)读取。
