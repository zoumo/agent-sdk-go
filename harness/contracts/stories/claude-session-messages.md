---
type: Specification
title: Claude 独立消息查询验收
status: stable
---

# Claude 独立消息查询验收

- 用户目标：不运行或恢复 Claude，独立读取显式本地范围的已保存主会话消息。
- 公开路径：原生 package-level GetSessionMessages；使用隔离 ConfigDir、项目和合成 JSONL。
- 内容验收：分支主叶、parallel 工具调用及结果兄弟、stray writer/已回答限制、压缩摘要及 logical parent 不回溯、meta/sidechain/team 过滤、大整数及 unknown Raw 均保留原生语义。
- 切片验收：可见消息选定后应用非负 limit/offset，默认返回有界；有效空、页尾空与失败可区分。
- 失败验收：损坏、重复、cycle、missing-parent、未知布局/类型、发现/字节/行/索引/返回量上限、取消、missing、inaccessible、歧义、路径隔离均可识别；失败不返回部分消息。
- 隔离验收：runtime 不可执行仍可查询，只读 fixture 哈希和权限不变；值 detached，不产生 live Event、回答权限或成员关系。已有 Session 的正常通知继续独立交付。
- 完整性验收：对读取期间 append/rewrite/replacement 作必要的内部故障注入，报告检测覆盖边界，不承诺跨页原子快照。

具体合同见[Claude 独立本地消息查询](../claude-session-messages.md)。
