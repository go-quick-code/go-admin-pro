-- +goose Up
-- 公海纵切（issue #13）：客户负责人历史表（docs/crm-mvp-spec.md §4.1：
-- 负责人变更写 crm_customer_owner_history；§4.2：释放/认领/转移均落历史）。
--
-- 追加型表（对 ADR 0002 底座的取舍）：历史行写入后不再变更、不软删除，
-- 审计追溯要求行随客户软删除仍然保留，因此不设 updated_at/deleted_at；
-- creator/updater 的语义由显式业务列 operator_user_id 承担，仅保留
-- created_at 一列时间（即操作时间）。客户表 00004 已有 owner_user_id
-- 可空列与部门快照列，本迁移不动客户表结构。

CREATE TABLE `crm_customer_owner_history` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL COMMENT '操作时间（追加型表，行写入后不再变更）',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `customer_id` bigint NOT NULL COMMENT '客户 ID，客户软删除后历史仍保留',
  `action` varchar(16) NOT NULL COMMENT '操作类型：create 创建 / claim 认领 / release 释放 / transfer 转移（recycle 回收由 #14 增加）',
  `from_owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '原负责人用户 ID，0 表示无（公海或首任）',
  `to_owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '新负责人用户 ID，0 表示无（释放入池）',
  `from_owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '原负责人部门快照',
  `to_owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '新负责人部门快照',
  `operator_user_id` bigint NOT NULL DEFAULT '0' COMMENT '操作人用户 ID',
  PRIMARY KEY (`id`),
  KEY `idx_crm_owner_history_tenant_customer` (`tenant_id`,`customer_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的历史表。00006 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_customer_owner_history`;
