-- +goose Up
-- 报销单与明细（issue #54，docs/oa-mvp-spec.md §3.6/§4.9）：
--   oa_expense_claim  报销单表头：报销人、标题、事由、表头金额（= 明细合计，
--     服务端计算，请求里的手填金额忽略）、状态机（0 草稿/1 待审/2 通过/
--     3 驳回/4 撤回）、驳回原因、bpm 实例关联（提交时写入，草稿为 NULL，#55 承接）。
--   oa_expense_item   明细行：类别（系统字典 oa_expense_category）、金额
--     （> 0，decimal(12,2) 分单位）、发生日期、备注、排序；所属报销单 claim_id。
-- 金额统一走 internal/pkg/money（int64 分 + decimal(12,2)），禁止浮点；
-- 合计校验/状态守卫/类别匹配为纯函数（internal/module/oa/service/expense_rule.go）。
-- 附件不建列：infra_file（biz_type='oa_expense'、biz_id=报销单 ID）。
-- 草稿保存整体替换明细行集，表头金额与明细合计在同一事务内写入。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater + tenant_id
-- （ADR 0002）。两表全部纳入租户插件（internal/pkg/database/tenant_tables.go）。

CREATE TABLE `oa_expense_claim` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '报销人（system_user，租户内）',
  `title` varchar(200) NOT NULL COMMENT '报销标题',
  `total_amount` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '表头金额（= 明细合计，服务端计算；请求里的手填金额忽略，spec §4.9）',
  `reason` varchar(500) NOT NULL COMMENT '报销事由',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 草稿 / 1 待审 / 2 通过 / 3 驳回 / 4 撤回（spec §4.9 状态机；通过即终态）',
  `reject_reason` varchar(200) DEFAULT NULL COMMENT '驳回原因（bpm 回调写入，#55）',
  `approval_instance_id` bigint DEFAULT NULL COMMENT 'bpm 流程实例 ID（bpm_process_instance.id，提交时写入；草稿为 NULL，#55）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_expense_claim_tenant_user_status` (`tenant_id`,`user_id`,`status`),
  KEY `idx_oa_expense_claim_instance` (`approval_instance_id`),
  KEY `idx_oa_expense_claim_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_expense_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `claim_id` bigint NOT NULL COMMENT '所属报销单 ID（oa_expense_claim）',
  `category` varchar(50) NOT NULL COMMENT '费用类别（系统字典 oa_expense_category 的启用值）',
  `amount` decimal(12,2) NOT NULL COMMENT '金额（分单位 decimal(12,2)；每条必须 > 0，spec §4.9）',
  `expense_date` date NOT NULL COMMENT '发生日期（YYYY-MM-DD）',
  `remark` varchar(200) DEFAULT NULL COMMENT '备注',
  `sort` int NOT NULL DEFAULT '0' COMMENT '显示顺序（小者在前；保存时按请求顺序归一化为 0..n-1）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_expense_item_tenant_claim` (`tenant_id`,`claim_id`,`sort`),
  KEY `idx_oa_expense_item_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_expense_item`;
DROP TABLE IF EXISTS `oa_expense_claim`;
