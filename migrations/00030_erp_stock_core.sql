-- +goose Up
-- ERP 库存核心与其它入库/出库单（issue #90，docs/erp-mvp-spec.md §3.2/§3.5/§3.7、
-- §4.1–4.3/§4.8）：一次建齐库存账（erp_stock/erp_stock_record）、杂项出入库两类
-- 单据（erp_stock_in/_item、erp_stock_out/_item）与序列表（erp_sequence）。
-- 后续票的单据表（采购/销售/调拨/盘点/财务）复用本迁移确立的库存账与序列机制。
--
-- 库存账模型（ADR 0016 纯数量账 + 反审核反向流水）：
--   - erp_stock 只存数量余额（无金额列，成本核算在 backlog）；租户内
--     UNIQUE(产品×仓库)；余额只能经 service 层唯一入账入口更新（spec §4.2），
--     出库 UPDATE 带 count >= ? 条件（应用层 + SQL 双保险）。
--   - erp_stock_record 是 append-only 流水：count 正入负出、total_count 记
--     变动后结存快照，附 biz_type/biz_id/biz_item_id/biz_no 单据引用；反审核
--     新增 _CANCEL 反向流水（原流水保留）→ 余额 = 初始 + 流水代数和可重放。
--     biz_type 口径：与单据表名一致（spec §4.8）+ _CANCEL 对，即 stock_in /
--     stock_in_CANCEL / stock_out / stock_out_CANCEL（#91+ 扩采购/销售/调拨/
--     盘点对；芋道用数值枚举 +「(作废)」对，这里按 spec 取表名字符串）。
--
-- 单据状态口径（对齐芋道字典 erp_audit_status）：10 未审核 / 20 已审核；
-- 只有审核动作落账；已审核不可改不可删，反审核退回 10（spec §4.1）。
-- 明细表带 total_price（行金额 = 单价×数量，服务端按 Money.MulQty 计算）：
-- spec §3.5 明细只列 product_price（记录用），但 §4.2/验收标准要求行金额
-- 不变量可校验，故补行金额列（与 #91/#92 采购/销售明细同构），主表不设
-- total_price（spec §3.5 未列，杂项出入库金额纯记录、不参与结算）。
-- 序列表机制复制 crm_sequence（00004）：PK(tenant_id, biz_type, date_key) +
-- INSERT ... ON DUPLICATE KEY UPDATE 原子发放（spec §3.7/§4.8，不 import crm）。
--
-- 数量列 decimal(24,3)（internal/pkg/qty 毫单位，ADR 0017）；金额列 decimal(12,2)
--（internal/pkg/money）；实体底座 id/created_at/updated_at/deleted_at/creator/updater
-- + tenant_id（ADR 0002）。租户隔离靠租户插件（七表登记 TenantTables）。
-- 外键语义关联，不设物理外键（与 00029 同风格）。

CREATE TABLE `erp_stock` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `product_id` bigint NOT NULL COMMENT '产品 ID（产品×仓库租户内唯一）',
  `warehouse_id` bigint NOT NULL COMMENT '仓库 ID',
  `count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '库存余额（qty 毫单位定点；只经唯一入账入口更新，spec §4.2）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_stock_tenant_product_warehouse` (`tenant_id`,`product_id`,`warehouse_id`),
  KEY `idx_erp_stock_tenant_warehouse` (`tenant_id`,`warehouse_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_record` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `warehouse_id` bigint NOT NULL COMMENT '仓库 ID',
  `count` decimal(24,3) NOT NULL COMMENT '变动量：正入负出（qty 毫单位定点）',
  `total_count` decimal(24,3) NOT NULL COMMENT '变动后结存快照（可审计重放）',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型（与单据表名一致 + _CANCEL 对，如 stock_in/stock_in_CANCEL）',
  `biz_id` bigint NOT NULL COMMENT '业务单据 ID（主表 ID）',
  `biz_item_id` bigint NOT NULL DEFAULT '0' COMMENT '业务单据明细 ID（行级引用，0 表示单据级）',
  `biz_no` varchar(64) NOT NULL DEFAULT '' COMMENT '业务单据编号（冗余展示，如 QR202611070001）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_stock_record_tenant_biz` (`tenant_id`,`biz_type`,`biz_id`),
  KEY `idx_erp_stock_record_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_stock_record_tenant_product` (`tenant_id`,`product_id`),
  KEY `idx_erp_stock_record_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sequence` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，序列按租户独立发放）',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型（与单据表名一致，如 stock_in/stock_out）',
  `date_key` varchar(8) NOT NULL COMMENT '日期键 yyyyMMdd（业务时区，跨日自然重置）',
  `last_value` bigint NOT NULL DEFAULT '0' COMMENT '已发放的最大序号',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_sequence_key` (`tenant_id`,`biz_type`,`date_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_in` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：QR+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `warehouse_id` bigint NOT NULL COMMENT '入库仓库 ID',
  `in_time` datetime(3) NOT NULL COMMENT '入库时间（业务时区）',
  `type` tinyint NOT NULL DEFAULT '99' COMMENT '入库类型（字典 erp_stock_in_type：10 采购/20 退货/30 借入/40 归还/50 赠品/99 其它）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（芋道 erp_audit_status 口径；只有审核落账）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_stock_in_no` (`tenant_id`,`no`),
  KEY `idx_erp_stock_in_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_stock_in_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_stock_in_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_stock_in_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_in_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `stock_in_id` bigint NOT NULL COMMENT '其它入库单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '入库数量（qty 毫单位定点，必须 > 0）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单价（元，记录用，缺省 0.00）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_stock_in_item_doc` (`tenant_id`,`stock_in_id`),
  KEY `idx_erp_stock_in_item_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_out` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：QC+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `warehouse_id` bigint NOT NULL COMMENT '出库仓库 ID',
  `out_time` datetime(3) NOT NULL COMMENT '出库时间（业务时区）',
  `type` tinyint NOT NULL DEFAULT '99' COMMENT '出库类型（字典 erp_stock_out_type：10 报损/20 领用/30 借出/99 其它）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（芋道 erp_audit_status 口径；只有审核落账）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_stock_out_no` (`tenant_id`,`no`),
  KEY `idx_erp_stock_out_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_stock_out_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_stock_out_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_stock_out_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_stock_out_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `stock_out_id` bigint NOT NULL COMMENT '其它出库单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '出库数量（qty 毫单位定点，必须 > 0；扣减 SQL 带 count >= ? 双保险）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单价（元，记录用，缺省 0.00）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_stock_out_item_doc` (`tenant_id`,`stock_out_id`),
  KEY `idx_erp_stock_out_item_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_stock_out_item`;
DROP TABLE IF EXISTS `erp_stock_out`;
DROP TABLE IF EXISTS `erp_stock_in_item`;
DROP TABLE IF EXISTS `erp_stock_in`;
DROP TABLE IF EXISTS `erp_sequence`;
DROP TABLE IF EXISTS `erp_stock_record`;
DROP TABLE IF EXISTS `erp_stock`;
