-- +goose Up
-- CRM 第一条纵切（issue #8）：客户表与编号序列表（docs/crm-mvp-spec.md §3 客户节）。
-- 表结构沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。

CREATE TABLE `crm_customer` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `customer_no` varchar(32) NOT NULL COMMENT '客户编号：KH+yyyyMMdd+4位序号，租户内唯一',
  `name` varchar(128) NOT NULL COMMENT '客户名称',
  `industry` varchar(64) DEFAULT NULL COMMENT '行业',
  `level` varchar(64) DEFAULT NULL COMMENT '客户级别',
  `source` varchar(64) DEFAULT NULL COMMENT '来源',
  `phone` varchar(32) DEFAULT NULL COMMENT '联系电话',
  `website` varchar(128) DEFAULT NULL COMMENT '网址',
  `address` varchar(255) DEFAULT NULL COMMENT '地址',
  `status` tinyint NOT NULL DEFAULT '1' COMMENT '状态：1 潜在 / 2 活跃 / 3 流失',
  `owner_user_id` bigint DEFAULT NULL COMMENT '负责人用户 ID，NULL 即公海客户',
  `owner_dept_id` bigint DEFAULT NULL COMMENT '负责人部门快照，随负责人同步（ADR 0004）',
  `pool_enter_time` datetime(3) DEFAULT NULL COMMENT '进入公海时间',
  `last_follow_time` datetime(3) DEFAULT NULL COMMENT '最后跟进时间',
  `release_reason` varchar(255) DEFAULT NULL COMMENT '释放原因',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_customer_tenant_no` (`tenant_id`,`customer_no`),
  KEY `idx_crm_customer_deleted_at` (`deleted_at`),
  KEY `idx_crm_customer_owner_user_id` (`owner_user_id`),
  KEY `idx_crm_customer_owner_dept_id` (`owner_dept_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 编号序列：计数支撑表，只有业务字段，不套用实体底座（规格 §3）。
-- 唯一键即主键，原子递增依赖其行锁；tenant_id 显式入键，租户间序号互不干扰。
CREATE TABLE `crm_sequence` (
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型，customer = 客户编号',
  `date_key` varchar(8) NOT NULL COMMENT '日期键 yyyyMMdd',
  `last_value` bigint NOT NULL DEFAULT '0' COMMENT '该日已发放的最大序号',
  PRIMARY KEY (`tenant_id`,`biz_type`,`date_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的两张表。00004 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_sequence`;
DROP TABLE IF EXISTS `crm_customer`;
