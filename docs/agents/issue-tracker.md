# GitHub 工作项

本仓库使用 [zoumo/agent-sdk-go 的 GitHub Issues](https://github.com/zoumo/agent-sdk-go/issues) 跟踪后续工作，不再以本地 Markdown 目录作为 tracker。所有设计与规范仍以 `harness/` 为准，issue 中的实施记录不替代设计合同。

## 规格、票据与执行

- `to-spec` 将已确认的范围写为一个规格 issue；`to-tickets` 按依赖顺序创建独立 ticket，每张票链接规格，并在平台支持时设为其子 issue。不要把所有 ticket 合并为一个 issue。
- 每张票包含交付行为、验收条件和阻塞关系。平台支持且当前连接可用时使用原生 blocking 关系；否则在正文的 `Blocked by` 列出 `#编号`，无阻塞写 `None`。只开始阻塞项均已完成的票。
- 状态沿用 `ready-for-agent`、`in-progress`、`done`。使用仓库已有的同名标签；标签不存在时在正文记录 `Status`，不要假定标签或项目字段已配置，也不要未经授权创建标签。完成票在验收后记为 `done` 并关闭；不因子票完成自动关闭规格 issue。
- `implement` 进入独立上下文前，读取对应 issue 的完整正文、评论和前置结果。checks、thread ID、实际 base/result commit、证据限制及未解决问题记录在对应 issue 的评论中。更新或发布工作项须遵守当前任务授权和连接权限。

## 历史记录与本地材料

历史本地票据不自动迁移到 GitHub，也不补造 issue 编号。已提交的票据可从删除前的 Git 历史恢复；清理时尚未提交的规格、票据和验证日志另有经完整性核验的仓库外备份。备份位置及校验值由清理交付记录提供，不是公开仓库资产。

项目根目录 `.scratch/` 已移除，并由根 `.gitignore` 排除。后续规格与票据使用 GitHub Issues；临时证据不应重新成为本地 tracker，也不得未经检查发布私有材料。需要迁移历史票据或公开备份内容时，先明确迁移范围与授权。
