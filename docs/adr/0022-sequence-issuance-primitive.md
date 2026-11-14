# 序号发放下沉 internal/pkg/sequence：并发不变量单点化

**Status**: accepted

全库唯一一段「错在并发下才显形」的 SQL——`INSERT … ON DUPLICATE KEY UPDATE` 后同事务回读 `last_value` 的序号发放——此前有四份拷贝：crm/erp/pay 各一份手写实现（逐字节相同、并发注释同构；erp/pay 注释自认「机制复制 crm/erp_sequence（不 import，spec §2）」），加上 codegen 模板生成物里的第四份（写死 `crm_sequence`）。决定下沉为 `internal/pkg/sequence.Next(ctx, db, table, tenantID, bizType, dateKey)` 单一实现（先例：ADR 0007 的 `internal/pkg/money`）。三张序列表保持各模块自有（`crm_sequence` 无 id/审计字段、唯一键即主键；`erp_sequence`/`pay_sequence` 带实体底座；DDL 分叉保持现状、不翻旧迁移），原语只参数化表名。各模块 Repository 的 `NextSequenceValue` 与 erp 内核 `Tx.NextNo`、pay `nextNoInTx` 保留为薄包装，全部调用点零改动。原语内部保留自开事务（调用方传入事务时走 GORM savepoint），行为逐字节不变：内层事务是对「调用者在 autocommit 上误用」的 fail-closed 第二道防线；「发放与单据落库同事务、回滚则序号不消耗」仍由调用方业务事务保证。格式层（前缀 + yyyyMMdd + 至少 4 位序号超 9999 扩位）与日期键留在模块：不是并发关键，格式错只影响展示，唯一性由 (tenant_id, biz_type, date_key) 计数行锁加序号保证。codegen 模板同票改为委托原语，golden 测试机械更新。并发不重号集成测试留在 pay（现成用例、打真实 MySQL、经 pay 包装覆盖原语 SQL），pkg 只加纯单测。

原因：这段 SQL 的接口约定（必须包在调用方事务里、租户值显式入参、裸 SQL 绕过租户插件、列名反引号转义与 MySQL 窗口函数 `LAST_VALUE` 的同形冲突）此前靠三段同构注释重复声明，修一处并发缺陷要改四处（含生成物）。模块间零 import（ADR 0015/0021）约束的是模块互不依赖，platform pkg 不在其中——旧注释「机制复制，不 import」的实质（模块自治）并不需要复制机制来达成。按 ADR 0018 的「第二个消费者再提升」规则衡量，消费者已是 3 模块 + codegen，早超线。通用提升规则随之定形：**同一并发不变量 ≥2 个消费者即下沉 internal/pkg，表与格式留模块**。

拒绝的替代：合并为一张共享序列表（要翻三个已上线迁移、动租户表登记、破坏「删模块连表删」的自包含）；原语直跑传入 db、去掉内层事务（少一层 savepoint，但 autocommit 误用从安全变静默重号，fail-open）；连 DateKey/FormatNo 一并下沉（diff 拉进 crm 四个格式函数与 seed，收益只是省几行纯展示代码）；删模块薄包装、调用点直调 pkg（侵入 crm 五处业务文件，模块 Repository 单一 DAL 入口的叙事被打破）；并发测试迁入 pkg 集成测试（要解决 pkg 级测试的表来源问题，churn 大于收益）。

相关：ADR 0007（internal/pkg 值类型先例）、ADR 0015（ERP 域边界）、ADR 0018（单据内核，「第二个消费者再提升」出处）、ADR 0021（pay 范围）、GLOSSARY「序号序列」「发号」「日期键」。
