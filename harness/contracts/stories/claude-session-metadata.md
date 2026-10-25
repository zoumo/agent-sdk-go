---
type: Specification
title: Claude 独立发现与 metadata 验收
status: stable
---

# Claude 独立发现与 metadata 验收

- 共同目标：无需 Session，从声明范围发现身份、检查 metadata，再选择读取保存内容。
- 原生公开路径：ListSessions → GetSessionInfo → GetSessionMessages，隔离临时 ConfigDir/project、合成只读 JSONL，无 runtime 和真实模型。
- 内容：可用时间、title/preview、unknown/null/大整数、无已知 metadata 的空/未知记录保留；Running unknown；值 detached，文件哈希和权限不变。
- 范围：显式项目过滤与 ConfigDir 全项目、无 worktree expansion、同布局 programmatic、已知子代理容器排除、symlink/未知布局及路径隔离可识别。
- 切片：降序 lastModified，tie 的明确 SDK 规则，非负 offset/limit 与有界默认页，超页尾成功空；重复身份保留并报歧义。
- 覆盖：完整空、missing、inaccessible、corrupt、partial、unknown、资源超限、取消可辨识；部分身份可伴 error，不静默成功。
- 采样：首尾预览不认证中段或完整链，单源 info 不强制全历史读取，首尾边界及截断尾行覆盖；源变化检测另用必要内部故障注入补充。
- 并存：历史查询不增加 live 消费者、协议写入、成员或回答权限；原 live 通知继续交付，原审批句柄仍按原权限使用。

具体值和边界见[metadata 合同](../claude-session-metadata.md)。
