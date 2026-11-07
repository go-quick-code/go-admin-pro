-- +goose Up
-- ERP 销售三单（issue #92，docs/erp-mvp-spec.md §3.4、§4.1/§4.5/§4.8）：
-- 销售订单（XS）/ 销售出库（CK）/ 销售退货（ST）三主三明，采购三单（#91，
-- 00031）的镜像。单号经 erp_sequence 事务内原子发放（00030 已建），库存变动
-- 只走 service 层唯一入账入口 ApplyStockChange（00030 确立），本迁移只落表。
--
-- 链路与占用（spec §4.5 镜像 §4.4）：
--   - 销售出库必挂已审核销售订单（order_id），销售退货必挂已审核销售出库
--     （out_id，仓库取自出库单）；无订单的收发走其它入库/出库单（对芋道收紧）。
--   - 占用校验（超量 409，未审核也占用，防超卖）：订单剩余可出 =
--     count −（未审核+已审核出库）−（未审核+已审核退货）；出库剩余可退 =
--     出库数量 −（未审核+已审核退货）。删除未审核单/反审核自然释放占用。
--   - 进度回写：明细 out_count/return_count 只统计已审核单，审批/反审核重算。
--   - 反审核禁令（409）：订单存在已审核出库/退货；出库存在已审核退货
--     （spec §4.1）。
--
-- 列名口径：销售订单主表编号列按 spec §3.4 字面落 order_no（对齐采购订单）；
-- 出库/退货主表 spec §3.4 未列编号列名，沿 #90 其它出入库单的 no（spec §3.5
-- 同样未列、#90 落 no）。receipt_price/refund_price 本票只落列（DEFAULT 0），
-- 收付款核销回写在 #94（spec §4.7）。
--
-- 状态 10 未审核 / 20 已审核（芋道 erp_audit_status 口径，spec §4.1）；
-- 数量 decimal(24,3)（qty 毫单位，ADR 0017）、金额 decimal(12,2)（money，ADR 0007）；
-- BaseEntity 五字段 + tenant_id（ADR 0002）；owner_user_id/owner_dept_id 承载
-- 单据 owner 数据范围谓词（owner = 创建人，ADR 0004，spec §4.10）。外键语义
-- 关联不设物理外键（与 00029/00030 同风格）。

CREATE TABLE `erp_sale_order` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `order_no` varchar(32) NOT NULL COMMENT '销售订单号：XS+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `customer_id` bigint NOT NULL COMMENT 'ERP 客户 ID（往来单位）',
  `sale_user_id` bigint NOT NULL DEFAULT '0' COMMENT '销售员用户 ID（纯记录字段，0 = 未记录，spec §3.4）',
  `order_time` datetime(3) NOT NULL COMMENT '下单时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（只有审核后出库/退货可挂）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_sale_order_no` (`tenant_id`,`order_no`),
  KEY `idx_erp_sale_order_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_erp_sale_order_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_sale_order_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_sale_order_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sale_order_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `sale_order_id` bigint NOT NULL COMMENT '销售订单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '订购数量（qty 毫单位定点，必须 > 0）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '销售单价（元）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端按 Money.MulQty 计算，客户端不采纳）',
  `out_count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '已出库数量回写（只计已审核销售出库，审批/反审核重算，spec §4.5）',
  `return_count` decimal(24,3) NOT NULL DEFAULT '0' COMMENT '已退货数量回写（只计已审核销售退货，审批/反审核重算，spec §4.5）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_sale_order_items_doc` (`tenant_id`,`sale_order_id`),
  KEY `idx_erp_sale_order_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sale_out` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '出库单号：CK+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `customer_id` bigint NOT NULL COMMENT 'ERP 客户 ID（必须与挂单订单的客户一致）',
  `order_id` bigint NOT NULL COMMENT '销售订单 ID（必填，必须已审核，spec §4.5）',
  `warehouse_id` bigint NOT NULL COMMENT '出库仓库 ID',
  `out_time` datetime(3) NOT NULL COMMENT '出库时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核经唯一入账入口减库存）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '明细合计冗余（服务端按行金额求和，客户端不采纳）',
  `receipt_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已收款金额回写（DEFAULT 0；本票只落列，核销回写在 #94，spec §4.7）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_sale_out_no` (`tenant_id`,`no`),
  KEY `idx_erp_sale_out_tenant_order` (`tenant_id`,`order_id`),
  KEY `idx_erp_sale_out_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_erp_sale_out_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_sale_out_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_sale_out_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_sale_out_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sale_out_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `sale_out_id` bigint NOT NULL COMMENT '销售出库单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '出库数量（qty 毫单位定点，必须 > 0；扣减经唯一入账入口，SQL 带 count >= ? 双保险）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '销售单价（元）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_sale_out_items_doc` (`tenant_id`,`sale_out_id`),
  KEY `idx_erp_sale_out_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sale_return` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '退货单号：ST+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `customer_id` bigint NOT NULL COMMENT 'ERP 客户 ID（必须与挂单出库单的客户一致）',
  `out_id` bigint NOT NULL COMMENT '销售出库单 ID（必填，必须已审核，spec §4.5）',
  `warehouse_id` bigint NOT NULL COMMENT '退回仓库 ID（取自挂单出库单，服务端派生）',
  `return_time` datetime(3) NOT NULL COMMENT '退货时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核经唯一入账入口回库存）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '明细合计冗余（服务端按行金额求和，客户端不采纳）',
  `refund_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已退款金额回写（DEFAULT 0；本票只落列，核销回写在 #94，spec §4.7）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_sale_return_no` (`tenant_id`,`no`),
  KEY `idx_erp_sale_return_tenant_out` (`tenant_id`,`out_id`),
  KEY `idx_erp_sale_return_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_erp_sale_return_tenant_warehouse` (`tenant_id`,`warehouse_id`),
  KEY `idx_erp_sale_return_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_sale_return_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_sale_return_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_sale_return_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `sale_return_id` bigint NOT NULL COMMENT '销售退货单 ID（主表）',
  `product_id` bigint NOT NULL COMMENT '产品 ID',
  `count` decimal(24,3) NOT NULL COMMENT '退货数量（qty 毫单位定点，必须 > 0；不可超挂单出库数量，占用校验 409）',
  `product_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '退货单价（元）',
  `total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '行金额 = 单价 × 数量（服务端计算，客户端不采纳）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_sale_return_items_doc` (`tenant_id`,`sale_return_id`),
  KEY `idx_erp_sale_return_items_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_sale_return_items`;
DROP TABLE IF EXISTS `erp_sale_return`;
DROP TABLE IF EXISTS `erp_sale_out_items`;
DROP TABLE IF EXISTS `erp_sale_out`;
DROP TABLE IF EXISTS `erp_sale_order_items`;
DROP TABLE IF EXISTS `erp_sale_order`;
