-- +goose Up
-- ERP 采购三单（issue #91，docs/erp-mvp-spec.md §3.3/§4.1/§4.4/§4.8）：采购订单
--（CG）/采购入库（RK）/采购退货（RT）三主三明。链路与约束（spec §4.4）：
--   - 入库必挂已审核订单（order_id 必填）；退货必挂已审核入库（in_id 必填），
--     仓库取自入库单；无订单的收发走其它入库/出库单（对芋道的收紧，差异记清单）。
--   - 占用校验（未审核单也占量）：订单（按产品聚合）剩余可入 =
--     count −（未审核+已审核入库数量）−（未审核+已审核退货数量），超量 409；
--     删除/反审核未审核与已审核单自然释放占用。
--   - 进度回写：明细 in_count/return_count 只统计已审核单，审核/反审核重算。
--   - 反审核禁令（409）：订单存在已审核入库/退货 → 订单不可反审核；
--     入库存在已审核退货 → 入库不可反审核；入库反审核经唯一入账入口
--     ApplyStockChange 新增 _CANCEL 反向流水回滚库存（原流水保留）。
--   - 库存变动只走 service 层唯一入账入口（spec §4.2，ADR 0016），本迁移只建账。
--
-- 金额列 decimal(12,2)（internal/pkg/money）、数量列 decimal(24,3)
--（internal/pkg/qty 毫单位，ADR 0017）；实体底座 id/created_at/updated_at/
-- deleted_at/creator/updater + tenant_id（ADR 0002），租户隔离靠租户插件
--（六表登记 TenantTables）。外键语义关联，不设物理外键（与 00029/00030 同风格）。
-- 订单编号列按 spec §3.3 字面为 order_no；入库/退货主表 spec 未列编号列名，
-- 沿 00030 杂项单据的 no。单号由 erp_sequence 事务内原子发放（00030），客户端提交不采纳。
-- payment_price/refund_price 为核销回写列（DEFAULT 0），本票只落列，
-- 收付款核销在财务票（#94）实现回写。
-- 状态口径沿用 00030：10 未审核 / 20 已审核（芋道 erp_audit_status，
-- 任务既有处置 3：ERP 单据统一 10/20，只有审核动作落账）。

CREATE TABLE `erp_purchase_order` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `order_no` varchar(32) NOT NULL COMMENT '订单号：CG+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `supplier_id` bigint NOT NULL COMMENT '供应商 ID',
  `order_time` datetime(3) NOT NULL COMMENT '下单时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（沿用 00030 口径；订单审核不动库存）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_purchase_order_order_no` (`tenant_id`,`order_no`),
  KEY `idx_erp_purchase_order_tenant_supplier` (`tenant_id`,`supplier_id`),
  KEY `idx_erp_purchase_order_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_purchase_order_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_purchase_order_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_purchase_order_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `purchase_order_id` bigint NOT NULL COMMENT '采购订单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '采购数量（qty 毫单位定点，必须 > 0）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单价（元）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  `in_count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '已入库数量回写（只计已审核入库单，审核/反审核重算，spec §4.4）',
  `return_count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '已退货数量回写（只计已审核退货单，经入库单关联到本订单，spec §4.4）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_purchase_order_items_doc` (`tenant_id`,`purchase_order_id`),
  KEY `idx_erp_purchase_order_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_purchase_in` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：RK+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `supplier_id` bigint NOT NULL COMMENT '供应商 ID（必须与挂单订单的供应商一致）',
  `order_id` bigint NOT NULL COMMENT '采购订单 ID（必填，必须是已审核订单，spec §4.4 对芋道的收紧）',
  `warehouse_id` bigint NOT NULL COMMENT '入库仓库 ID（必填）',
  `in_time` datetime(3) NOT NULL COMMENT '入库时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核经唯一入账入口增库存并回写订单进度）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单据金额 = 明细行金额合计（服务端计算冗余，spec §3.3）',
  `payment_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已核销付款回写（DEFAULT 0；本票只落列，核销在 #94）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_purchase_in_no` (`tenant_id`,`no`),
  KEY `idx_erp_purchase_in_tenant_order` (`tenant_id`,`order_id`),
  KEY `idx_erp_purchase_in_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_purchase_in_tenant_supplier` (`tenant_id`,`supplier_id`),
  KEY `idx_erp_purchase_in_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_purchase_in_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_purchase_in_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_purchase_in_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `purchase_in_id` bigint NOT NULL COMMENT '采购入库单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '入库数量（qty 毫单位定点，必须 > 0；占订单余量）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单价（元，默认取订单行单价）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_purchase_in_items_doc` (`tenant_id`,`purchase_in_id`),
  KEY `idx_erp_purchase_in_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_purchase_return` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '单号：RT+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `supplier_id` bigint NOT NULL COMMENT '供应商 ID（必须与挂单入库单的供应商一致）',
  `in_id` bigint NOT NULL COMMENT '采购入库单 ID（必填，必须是已审核入库单，spec §4.4）',
  `warehouse_id` bigint NOT NULL COMMENT '退货仓库 ID（取自入库单，服务端回填，入参不收）',
  `return_time` datetime(3) NOT NULL COMMENT '退货时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核经唯一入账入口减库存并回写订单进度）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单据金额 = 明细行金额合计（服务端计算冗余，spec §3.3）',
  `refund_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已核销退款回写（DEFAULT 0；本票只落列，核销在 #94）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_purchase_return_no` (`tenant_id`,`no`),
  KEY `idx_erp_purchase_return_tenant_in` (`tenant_id`,`in_id`),
  KEY `idx_erp_purchase_return_tenant_supplier` (`tenant_id`,`supplier_id`),
  KEY `idx_erp_purchase_return_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_purchase_return_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_purchase_return_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_purchase_return_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `purchase_return_id` bigint NOT NULL COMMENT '采购退货单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '退货数量（qty 毫单位定点，必须 > 0；占入库单余量并计入订单退货占用）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '单价（元）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_purchase_return_items_doc` (`tenant_id`,`purchase_return_id`),
  KEY `idx_erp_purchase_return_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_purchase_return_items`;
DROP TABLE IF EXISTS `erp_purchase_return`;
DROP TABLE IF EXISTS `erp_purchase_in_items`;
DROP TABLE IF EXISTS `erp_purchase_in`;
DROP TABLE IF EXISTS `erp_purchase_order_items`;
DROP TABLE IF EXISTS `erp_purchase_order`;
