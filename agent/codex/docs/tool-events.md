---
type: Specification
title: Codex 工具事件映射
status: stable
description: "规定 Codex adapter 如何映射原生工具事件。"
tags: [codex, tools, unified-agent]
---

# Codex 工具事件映射

| Codex item / 条件 | `ToolName` | `Kind` | `Provider` |
|---|---|---|---|
| `commandExecution`，`source=unifiedExecStartup` | `exec_command` | `execute` | `agent` |
| `commandExecution`，`source=unifiedExecInteraction` | `write_stdin` | `execute` | `agent` |
| `commandExecution`，`source=agent`、`userShell`、缺失或未知 | `commandExecution`（fallback；没有唯一模型工具名） | `execute` | `agent` |
| `imageView` | `view_image` | `read` | `agent` |
| `fileChange` | `fileChange`（fallback） | `edit` | `agent` |
| `webSearch` | `webSearch`（fallback） | `search` | `agent` |
| `sleep` | `sleep`（fallback） | `other` | `agent` |
| `imageGeneration` | `imageGeneration`（fallback） | `other` | `agent` |
| `mcpToolCall` | `item.Tool` | `other` | `mcp:<server>`；server 为空时为 `mcp` |
| `dynamicToolCall` | `namespace + tool` | `other` | `dynamic` |
| `functionCallOutput` | `namespace + name` | `other` | `dynamic` |
| `collabAgentToolCall` | `string(item.Tool)` | `other` | `agent` |

`dynamicToolCall` 和 `functionCallOutput` 的 namespace 与名称直接拼接，不插入
`.`；namespace 为空时分别使用 `tool` 或 `name`。这是 ACP 的 Codex flatten
规则，不是 SDK 自造格式。`commandExecution` 的 `source` 是 Codex 原生字段；
只有 `unifiedExecStartup` 和 `unifiedExecInteraction` 能确定模型工具名。
