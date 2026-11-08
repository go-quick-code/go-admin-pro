-- +goose Up
-- ERP 库存调拨与盘点（issue #93，docs/erp-mvp-spec.md §3.5/§4.6/§4.8）：
-- 一次建齐调拨单（erp_stock_move/_item）与盘点单（erp_stock_check/_item）。
--
-- 调拨（前缀 DB，biz_type stock_move）：主表 from_warehouse_id/to_warehouse_id
-- （审核时两仓必须不同，同仓 409）；审核在同一事务内出仓 count 取负、入仓
-- count 取正，两笔流水同额成对；反审核各生成反向流水（_CANCEL，原流水保留）。
-- 多仓库调拨按仓库 ID 升序先取行锁（spec §4.2），防交叉调拨死锁。
--
-- 盘点（前缀 PD，biz_type stock_check）：明细 actual_count 为实际清点数（≥0）；
-- base_count 是创建时的账面数快照，仅记录、审核不参与计算——审核时逐行按
-- 「差异 = actual_count − 当时账面数」经唯一入账入口调整（差正盘盈入、差负
-- 盘亏出），审核后账面 = 实际数；反审核按已入账流水净额反向。
--
-- 单据状态口径沿用 00030（芋道 erp_audit_status：10 未审核 / 20 已审核）；
-- 只有审核动作落账，已审核不可改删（spec §4.1）。单号由 erp_sequence 事务内
-- 原子发放（spec §4.8），客户端提交不采纳（DTO 无单号字段）。
-- 数量列 decimal(24,3)（internal/pkg/qty 毫单位定点，ADR 0017）；实体底座
-- id/created_at/updated_at/deleted_at/creator/updater + tenant_id（ADR 0002）。
-- 租户隔离靠租户插件（四表登记 TenantTables）。外键语义关联，不设物理外键。

CREATE TABLE `erp_stock_move` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：DB+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `from_warehouse_id` bigint NOT NULL COMMENT '调出仓库 ID',
  `to_warehouse_id` bigint NOT NULL COMMENT '调入仓库 ID（审核时必须与调出仓库不同，同仓 409）',
  `move_time` datetime(3) NOT NULL COMMENT '调拨时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（芋道 erp_audit_status 口径；只有审核落账）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_stock_move_no` (`tenant_id`,`no`),
  KEY `idx_erp_stock_move_from_warehouse` (`tenant_id`,`from_warehouse_id`),
  KEY `idx_erp_stock_move_to_warehouse` (`tenant_id`,`to_warehouse_id`),
  KEY `idx_erp_stock_move_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_stock_move_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_stock_move_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_move_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `stock_move_id` bigint NOT NULL COMMENT '库存调拨单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '调拨数量（qty 毫单位定点，必须 > 0；出仓扣减 SQL 带 count >= ? 双保险）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_stock_move_item_doc` (`tenant_id`,`stock_move_id`),
  KEY `idx_erp_stock_move_item_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_check` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：PD+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `warehouse_id` bigint NOT NULL COMMENT '盘点仓库 ID',
  `check_time` datetime(3) NOT NULL COMMENT '盘点时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（芋道 erp_audit_status 口径；只有审核落账）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_stock_check_no` (`tenant_id`,`no`),
  KEY `idx_erp_stock_check_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_stock_check_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_stock_check_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_stock_check_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_check_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `stock_check_id` bigint NOT NULL COMMENT '库存盘点单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `actual_count` decimal(24,3) NOT NULL COMMENT '实际清点数（qty 毫单位定点，必须 ≥ 0）',
  `base_count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '创建时账面数快照（仅记录，审核以当时账面为准，不参与差异计算）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_stock_check_item_doc` (`tenant_id`,`stock_check_id`),
  KEY `idx_erp_stock_check_item_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_stock_check_item`;
DROP TABLE IF EXISTS `erp_stock_check`;
DROP TABLE IF EXISTS `erp_stock_move_item`;
DROP TABLE IF EXISTS `erp_stock_move`;
