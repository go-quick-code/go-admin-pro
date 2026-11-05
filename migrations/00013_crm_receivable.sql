-- +goose Up
-- 回款计划与回款记录（issue #18，docs/crm-mvp-spec.md §3 回款节/§4.3 金额不变量/§4.5 删除约束）：
-- 财务按合同拆回款计划（一期一笔应收），实际到账登记回款记录冲抵计划。
-- 两张表沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。
-- 计划不存状态与已收金额：列表用 SUM 子查询派生「待回款/部分回款/已回款/逾期」（spec §3）；
-- 计划不单独编号，period_no 标识合同内的期次；记录编号 HK+yyyyMMdd+4 位序号，
-- 由 crm_sequence 原子发放（与 KH/SJ/HT 同款），租户内唯一。
-- 数据权限（spec §4.1）：计划/记录 join 合同后套用合同的 owner_* 谓词。

CREATE TABLE `crm_receivable_plan` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `contract_id` bigint NOT NULL COMMENT '所属合同 ID（外键语义关联，不设物理外键）',
  `period_no` int NOT NULL COMMENT '期次（合同内第几期，正整数；计划不单独编号）',
  `amount` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '计划金额（元）：SUM(计划金额) ≤ 合同金额（spec §4.3，service 层校验）',
  `due_date` date NOT NULL COMMENT '到期日（派生逾期与逾期筛选的基准）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  KEY `idx_crm_receivable_plan_contract_id` (`contract_id`),
  KEY `idx_crm_receivable_plan_tenant_contract` (`tenant_id`,`contract_id`),
  KEY `idx_crm_receivable_plan_due_date` (`due_date`),
  KEY `idx_crm_receivable_plan_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `crm_receivable_record` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `plan_id` bigint NOT NULL COMMENT '所属回款计划 ID',
  `contract_id` bigint NOT NULL COMMENT '所属合同 ID（由计划带出，列表按合同筛选与数据权限用）',
  `record_no` varchar(32) NOT NULL COMMENT '回款记录编号：HK+yyyyMMdd+4位序号，租户内唯一（crm_sequence 原子发放）',
  `amount` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '回款金额（元）：SUM(记录金额) ≤ 计划金额（spec §4.3，service 层校验）',
  `receipt_date` date NOT NULL COMMENT '到账日期',
  `method` varchar(32) DEFAULT NULL COMMENT '回款方式（如 银行转账/现金/支票）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_receivable_record_tenant_no` (`tenant_id`,`record_no`),
  KEY `idx_crm_receivable_record_plan_id` (`plan_id`),
  KEY `idx_crm_receivable_record_contract_id` (`contract_id`),
  KEY `idx_crm_receivable_record_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `crm_receivable_record`;
DROP TABLE IF EXISTS `crm_receivable_plan`;
