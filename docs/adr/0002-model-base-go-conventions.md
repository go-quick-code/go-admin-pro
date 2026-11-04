# 模型底座采用 Go/GORM 约定，补 creator/updater

**Status**: accepted

所有持久化模型共享 `created_at`、`updated_at`、`deleted_at` 时间字段，并新增 `creator`、`updater` 审计字段；不采用芋道的 `create_time` / `update_time` / `deleted bit` 命名。原因：兼容契约为 C1（概念等价，不做 DDL 兼容），模仿芋道字段名没有收益，且会与 GORM 的软删除机制冲突；但 `creator`/`updater` 是审计与「仅本人」数据范围的刚需。现在 17 张表一次性迁移成本最低，后续新增表沿用同一底座。代价：未来若要做芋道数据导入，需要字段映射层。

**实施状态**：`creator/updater` 与关联表 `tenant_id` 由后续迁移（#4）补上；`00001_baseline.sql` 仍按当前 17 张表的结构固化。
