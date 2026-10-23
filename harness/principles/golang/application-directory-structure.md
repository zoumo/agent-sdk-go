---
type: Development Standard
title: Go 应用项目目录结构
description: 规定服务端程序和命令行应用的目录结构。
status: stable
---

# Go 应用项目目录结构

服务端程序和命令行应用遵循以下规则：

- `cmd/` 的子目录放可编译成二进制的程序。每个直接子目录对应一个独立二进制，包含 `package main` 和 `main` 入口。
- `pkg/` 的子目录放模块。每个直接子目录对应一个职责明确的模块，模块内部可以继续划分子包。
- `cmd/<二进制名>/main.go` 负责程序入口、进程初始化与依赖组装。
- 命令树、flag、命令执行与终端输出代码放入同目录的 `subcommand/` 包。代码按资源和操作组织，不放入 `pkg/`。
- 可复用业务模块放入 `pkg/`，禁止依赖 `cmd/`。
- `subcommand/` 可以依赖业务模块。业务规则与生命周期编排仍归业务模块。

## 目录示例

以下路径相对于 Go 模块根目录。二进制和模块名称按实际职责确定：

```text
project/
├── go.mod
├── go.sum
├── cmd/
│   ├── server/        # 可编译为 server 二进制
│   └── cli/           # 可编译为 cli 二进制
│       ├── main.go    # 程序入口
│       └── subcommand/
│           ├── root.go
│           └── resource.go
└── pkg/
    ├── modulea/       # 模块 A
    └── moduleb/       # 模块 B
```

这里的模块指职责明确的代码模块，不要求每个子目录都有独立的 go.mod。示例名称不作为固定目录要求。
