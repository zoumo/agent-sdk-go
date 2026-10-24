# 本地工作项

本仓库使用本地 Markdown tracker，不向外部平台发布工作项。每项工作按依赖顺序写入 `.scratch/<feature>/issues/<NN>-<slug>.md`，一文件一 ticket，Blocked by 列出前置编号。`.scratch/` 不进入 SDK 发布产物。

状态为 ready-for-agent、in-progress、done；checks、thread ID、base/result commit 及未解决问题写在对应 ticket 的 Comments。使用技能时，读取实际文件，不把所有 ticket 合并为一个文件。任务规范和进度分别放在该 feature 的 spec.md 与 progress.md。

Session 重构使用 feature `session-refactor`。所有设计与规范仍以 harness 为准，操作记录不替代设计合同。
