---
type: Decision
title: 结构化 Prompt 的内容块与原生映射
status: stable
x-agent-sdk-go-decision-status: accepted
x-agent-sdk-go-decision-date: "2026-11-23"
description: "记录 StartOrSteer 内容块的 MCP 来源、原生映射和资源链接默认行为。"
tags: [input, content-blocks, unified-agent]
---

# 结构化 Prompt 的内容块与原生映射

SDK 的 `ContentBlock` 取自 MCP 的五分支内容模型。`EmbeddedResource` 携带内嵌正文，`ResourceLink` 携带 URI 和资源描述。SDK 用这些类型表达有序 Prompt，由各适配器决定 `StartOrSteer` 的原生映射。[MCP `ContentBlock` schema](https://modelcontextprotocol.io/specification/2025-11-25/schema#contentblock)

## 资源链接默认作为文本

`resource_link` 省略 `mimeType` 时，两个适配器都在原位置把原始 `uri` 编码为文本块。URI 的协议和主机名不参与默认映射。例如，`file:///notes` 会作为字面文本进入 Prompt。

声明 `mimeType` 时，适配器按类型和 URI 选择下表中的映射。带主机名的 HTTP(S) 链接也可用 `text/html`（可带参数）显式选择文本。附件映射需要调用方声明类型，SDK 在编码时不读文件、不请求 URL，也不根据扩展名推断类型。原生端决定是否读取收到的附件引用。

## 内容块支持范围

| JSON `type` | Go 分支及内容 | Codex | Claude |
| --- | --- | --- | --- |
| `text` | `Text *ContentBlockText`，UTF-8 文本 | 原生文本块 | 单独一个 `text` 块时使用原生文本字符串。含多个块时使用原生文本块。 |
| `image` | `Image *ContentBlockImage`，Base64 数据、MIME 类型和可选来源 URI | `image/*` 的 Base64 data URI | JPEG、PNG、GIF、WebP 的原生 Base64 图片 |
| `audio` | `Audio *ContentBlockAudio`，Base64 数据和 MIME 类型 | `audio/*` 的 Base64 data URI | `unsupported` |
| `resource` | `EmbeddedResource *ContentBlockEmbeddedResource`，内嵌 `Resource` 含来源 URI、可选 MIME 类型及 `Text` 或 `Blob` | `unsupported` | 省略 MIME 类型或声明 `text/plain` 的 `Text` 映射为纯文本文档。`application/pdf` 的 `Blob` 映射为 Base64 PDF 文档。 |
| `resource_link` | `ResourceLink *ContentBlockResourceLink`，URI、名称及可选 `mimeType`、`title`、`description`、`size` 和 `icons` | 支持文本映射。带图片 MIME 类型且有主机名的 HTTP(S) 链接映射为原生图片 URL。 | 支持文本映射。带受支持的图片 MIME 类型或 `application/pdf` 且有主机名的 HTTP(S) 链接映射为原生附件 URL。 |

`image` 和 `resource` 的正文来自块内数据，`uri` 标识来源。Claude 的 PDF 链接使用 `title` 作为文档标题，省略时使用 `name`，并将 `description` 映射为文档 `context`。

`resource_link.icons` 保留在内容块中供客户端展示。每个图标包含 `src` URI，可选 `mimeType`、`sizes` 和 `theme`，其中 `theme` 取值为 `light` 或 `dark`。其余链接元数据由调用方保留。

[Go 类型](../../agent/input.go) · [Codex 编码](../../agent/codex/input.go) · [Claude 编码](../../agent/claude/input.go) · [输入合同](../modules/unified-agent/design.md#输入与内容块)
