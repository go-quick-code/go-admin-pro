# 使用 goose 做版本化迁移，替代 AutoMigrate

**Status**: accepted

所有表结构变更通过 goose SQL 迁移管理，启动时只校验版本，不再用 GORM AutoMigrate 建表；种子数据在迁移完成后单独执行。原因：17 张表要加 `creator/updater` 并 backfill，CRM 还会持续加表，AutoMigrate 无法表达数据迁移与回滚。代价：每次加表要写迁移文件，开发时多一步；换取可审计、可回滚、生产可安全升级。

**运维注意**：MySQL 的 DDL 非事务性。`00001_baseline.sql` 在一个文件里建 17 张表，中途失败会留下半套表，需手工清理后重跑；后续迁移尽量一个迁移只做一个 DDL，或写成可重入语句。迁移命令默认 30 分钟超时，数据迁移/backfill 用 `-timeout` 放宽或设为 0 关闭。已有结构但没有 goose 记录的库，核对结构后用 `migrate stamp <version>` 标记，不要直接重跑基线。
