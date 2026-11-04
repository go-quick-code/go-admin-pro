# 使用 goose 做版本化迁移，替代 AutoMigrate

**Status**: accepted

所有表结构变更通过 goose SQL 迁移管理，启动时只校验版本，不再用 GORM AutoMigrate 建表；种子数据在迁移完成后单独执行。原因：17 张表要加 `creator/updater` 并 backfill，CRM 还会持续加表，AutoMigrate 无法表达数据迁移与回滚。代价：每次加表要写迁移文件，开发时多一步；换取可审计、可回滚、生产可安全升级。
