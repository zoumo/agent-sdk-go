---
type: Development Standard
title: Go 项目技术选型
description: 规定 Go 项目的 CLI、配置读取、测试、HTTP 框架与客户端、结构化日志、日志滚动、lint 和可观测性选型。
status: stable
---

# Go 项目技术选型

选型只约束项目已有或明确要求新增的能力。
不要求同时引入全部依赖。

第三方选型须附官方文档或源码链接。
版本、配置和命令放在实现目录或仓库操作说明中。

## CLI 与 flag

选型：Cobra。

使用 Cobra 组织命令及管理 flag。

## 配置读取

选型：Viper。

Viper 读取配置，默认格式为 YAML。
绑定 Cobra flag，优先级为显式参数、配置文件、默认值。
使用独立 Viper 实例，避免全局状态相互影响。

## 测试

选型：testify。

尽可能使用 suite 组织测试。

## HTTP 框架

选型：Huma v2 + chi。

使用 chi 路由，配合 Huma 定义 HTTP 接口。
统一脚手架注册路由与请求、响应类型。
代码生成具体 schema 和 OpenAPI 文档。
避免手工维护重复的接口定义。

## HTTP 客户端

选型：Resty。

默认使用 Resty 发送 HTTP 请求。
超时、重试、重定向与流式响应须遵守接口合同。

## 结构化日志

选型：标准库 log/slog。

- 使用 slog 的结构化字段表达上下文，保持字段名和级别含义一致，避免把可查询字段拼入消息文本。
- 程序入口统一配置日志；可复用模块不自行更改全局输出目标或级别。
- 使用 JSONHandler 提供机器可读日志，TextHandler 用于本地阅读。日志与 CLI 业务输出分别处理，避免污染 stdout 上的命令结果。
- 日志禁止记录认证秘密。请求、响应与业务内容遵循明确的记录和脱敏策略。

## 日志滚动

选型：Lumberjack。

日志写入本地文件时，使用 Lumberjack 按文件大小滚动日志。
将 `lumberjack.Logger` 作为 slog Handler 的输出目标。
程序入口配置文件路径、大小上限、保留数量、保留天数和压缩选项。
同一组日志文件只允许一个进程写入。
程序退出时关闭 `lumberjack.Logger`。

## lint 与格式检查

选型：golangci-lint。

配置基线见 [golangci-lint 模板](templates/golangci-lint.yaml)，采用 v2 格式。
新项目可复制为 Go 模块根目录的 `.golangci.yaml` 并按需调整。
已有项目配置不自动替换。

- 使用 golangci-lint 统一执行检查，显式维护启用的规则、格式器和必要例外。
- 配置随源码管理，开发环境与自动检查使用同一份规则，不依赖个人全局配置。
- 忽略项限定到必要范围并说明原因；不能通过全局关闭规则掩盖局部问题。
- lint 不替代编译、测试、race 检查或真实外部系统验收。

## 可观测性

选型：OpenTelemetry Go。

- 统一采用 OpenTelemetry Go API/SDK，通过 context 传递追踪上下文；日志关联有效的 trace ID 和 span ID。
- 程序入口负责 Provider、Exporter 的初始化、配置和关闭；可复用模块只负责埋点，不自行设置全局 Provider。
- 指标标签使用可控的低基数字段；请求 ID、会话 ID 和任意用户内容不作为指标标签。
- 导出失败不改变业务操作的成功或失败结果。
- 遥测缓冲、导出与退出刷新须有明确容量和时间边界。
- 采集后端、Exporter、采样率、指标名称及部署配置由具体项目确定，不在通用选型中固定。
