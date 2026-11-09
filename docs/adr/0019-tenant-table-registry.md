# 租户表登记：schema 为真源，启动期强制全表分类

**Status**: accepted

迁移建出的每张表必须且只能登记为两类之一：`TenantTables`（租户隔离）或 `PlatformTables`（平台级，逐表写明理由）。应用启动时（`app.New`，迁移版本校验之后、安装租户插件之前）用 `information_schema` 做精确分区校验：库中每张 BASE TABLE 必须出现在两张清单之一；`TenantTables` 的表必须有 `tenant_id` 列，`PlatformTables` 的表必须没有；差异一次性汇总后拒绝启动。租户插件仍按表名匹配（`oa/repository/announcement.go` 的 `.Table("oa_announcement")` 等无 schema 查询依赖它，不能改成按模型字段判定）；`createCallback` 对「已登记表 + 模型缺少可写 `TenantID` 字段」fail-closed，堵住插入落到列默认租户 1 的洞。

原因：此前表清单分散在迁移、`TenantTables`、模型 `TableName()` 与 `EXPECTED_TABLES` 四处，漏登记一张表不会编译失败、也不会启动失败，只会让该表的读写静默跨租户。schema 本身是唯一不会被遗忘的真源，启动期比对把「静默泄漏」变成「拒绝启动」。代价：新增表多一个显式分类动作；新增平台级表必须写理由；每次启动多两次元数据查询（毫秒级，应用账号对自身 schema 的常规权限即可）。`EXPECTED_TABLES` 保留为独立的 CI 金丝雀，不并入本不变量。

拒绝的替代：从库中实时派生注册表（行为随 schema 漂移、sqlite 单测无法使用、也区分不出「业务表漏了 `tenant_id`」）；按模型是否有 `TenantID` 字段决定隔离（无 schema 的 `.Table()` 查询会漏）；codegen 或 `TenantEntity` 标记内嵌（93 个模型的机械改动与生成步骤，收益只是省一次手工登记；等第二个业务套件出现再议）。

相关：ADR 0004（数据权限）、ADR 0012（租户套餐）、`docs/platform-mvp-spec.md` §4.6、`docs/agents/verification.md`。
