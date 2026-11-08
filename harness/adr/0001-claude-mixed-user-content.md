---
type: Decision
title: Claude mixed user content 明确不支持
status: accepted
date: 2026-11-08
---

# Claude mixed user content 明确不支持

## 背景

统一事件的消息 payload 本身支持内容数组：

```go
type MessagePayload struct {
    MessageID string
    Content   []ContentBlock
    Phase     string
}
```

`UserMessage`、`AgentMessage` 和 `AgentThinking` 都使用这个结构，因此
`Content` 可以包含多个块。问题不在统一 payload，而在 Claude adapter 如何把
一个原生 user frame 映射为具有稳定 item 身份的 live 事件。

Claude 官方 TypeScript 0.3.302 声明中的 `sdk.d.ts:6342` 显示
`SDKUserMessage.message` 是 `MessageParam`；其 `content` 允许字符串或包含
`text`、`tool_result` 等块的数组。协议类型因此允许 `text` 与 `tool_result`
出现在同一个 user content 数组中。类型来源与版本固定记录见
`claude/docs/type-sources.md`。

此前 adapter 为该形状构造了逐 text segment 的拆分：每个 text segment 生成一个
`user.message`，所有 segment 复用同一 message identity，并附加
`blockIndexes` 和 `segmentIndex`。这需要 adapter 发明协议类型允许、但没有原生
稳定身份的 segment 语义，也与 MASS 要求的 item 身份不一致。

## 证据

2026-11-08 对 `~/.claude/projects` 下 68 个 JSONL 文件做了只读扫描，共发现
307 条 `type=user` 记录。其中 196 条 content 是数组：

- 112 条仅含 `text`；
- 84 条仅含 `tool_result`；
- 0 条同时包含 `text` 和 `tool_result`；
- 0 条包含多个 `text` 块；
- 0 条在 user content 顶层包含 `tool_use`。

这是对当前真实 Claude 输出的观察，不是协议保证。以后若真实 CLI 产生 mixed
frame，仍应把它视为新的兼容性证据，而不是把本结论解释为“协议禁止”。

## 决策

Claude adapter 不实现 `text + tool_result` mixed user content：

- 在产生任何事件前检测同一 user content 数组是否同时含 `text` 与
  `tool_result`；
- 发现 mixed 时只生成一个 `session.error`，reason 为
  `user_message_mixed_content_unsupported`；
- 不输出任何部分 `user.message` 或 `agent.tool_result`；
- 不增加 `segmentIndex`、`blockIndexes` 或新的 segment 身份；
- 纯 `text` 数组仍作为一个 `UserMessage` 的 `Content []ContentBlock` 输出；
- 纯 `tool_result` 数组仍按工具结果输出；
- 其他不支持的块继续按明确 gap 处理。

## 后果

当前支持的 user content 形状可以由一个原生 frame 直接映射，不需要猜测身份。
未来若出现 mixed frame，调用方会收到明确缺口，并保留原生 raw 供诊断；不会得到
顺序正确但身份可疑的部分事实。

若未来真实输出出现该形状，应基于新的原生证据重新评估本 ADR。届时需要证明
segment 的原生身份，而不是把同一个 message ID 重复用于多个 item。

## 未采用方案

1. 每个 text segment 生成一个 `user.message`，用 `segmentIndex` 区分。没有原生
   segment 身份，且多个 item 共用 message identity。
2. 把整个 mixed frame 合并成一个 `user.message` 再加 tool result。无法保持原生
   block 顺序，也无法为 text 和工具结果建立稳定 item 边界。
3. 为协议允许但当前不出现的形状实现完整支持。增加身份和排序逻辑，但没有真实
   输入触发，无法验证其正确性。

## 相关合同

- [统一 Agent 设计](../modules/unified-agent/design.md)
- Claude adapter 包文档：`agent/claude/doc.go`
- Claude 类型来源：`claude/docs/type-sources.md`
