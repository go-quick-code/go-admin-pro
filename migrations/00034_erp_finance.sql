-- +goose Up
-- ERP 财务域（issue #94，docs/erp-mvp-spec.md §3.6、§4.7、§4.8）：结算账户
-- （erp_finance_account）+ 收款单/付款单两主两明，共五张表。
--
-- 结算账户（spec §3.6）：
--   - 纯信息 + 默认账户租户内唯一互斥（置默认时取消原默认）；无余额列。
--   - 余额 = 该账户已审核收付款净额，由 service 层实时汇总查询（纯展示，
--     不落库，spec §4.7）。
--   - default_flag 是生成列影子列：默认账户为 1、非默认为 NULL，配合
--     UNIQUE(tenant_id, default_flag) 在库层兜底唯一互斥（MySQL 唯一键允许多
--     NULL）。置默认/删除默认账户的应用层逻辑仍会先清原默认，生成列只防并发
--     写穿。
--
-- 收款单（SK）/付款单（FK）：
--   - 明细 biz_type 取单据表名字符串（spec §4.8「biz_type 与表名一致」口径，
--     与 #90 库存流水一致）：收款挂 sale_out（销售出库）/ sale_return（销售
--     退货）；付款镜像挂 purchase_in / purchase_return。只有已审核单据可挂
--     （未审核 409，spec §4.7）。
--   - 明细 price 是本次核销金额；主表不设 total_price（spec §3.6 未列，列表/
--     详情金额由明细合计服务端汇总，核销上限按单据维度裁决）。
--   - 核销只在审核时生效：审核回写被核销单据的 sale_out.receipt_price /
--     sale_return.refund_price / purchase_in.payment_price /
--     purchase_return.refund_price（#91/#92 已落列，本票回写）；反审核重算。
--   - 存在已审核收付款引用时，出库/退货/入库不可反审核（409，spec §4.1/§4.7）。
--
-- 状态 10 未审核 / 20 已审核（芋道 erp_audit_status 口径，spec §4.1）；金额
-- decimal(12,2)（money，ADR 0007）；BaseEntity 五字段 + tenant_id（ADR 0002）；
-- owner_user_id/owner_dept_id 承载 owner 数据范围谓词（owner = 创建人，
-- ADR 0004，spec §4.10）。外键语义关联不设物理外键（与 00029–00032 同风格）。

CREATE TABLE `erp_finance_account` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(64) NOT NULL COMMENT '账户名称',
  `no` varchar(32) DEFAULT NULL COMMENT '账户编号（展示文本，客户端提交，非单号）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（主数据统一口径）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '排序（0 合法）',
  `default_status` tinyint NOT NULL DEFAULT '0' COMMENT '是否默认账户：1 是 / 0 否（租户内唯一互斥，置默认取消原默认）',
  `default_flag` tinyint GENERATED ALWAYS AS (CASE WHEN `default_status` = 1 THEN 1 ELSE NULL END) STORED COMMENT '默认账户唯一互斥影子列（非默认恒 NULL，不参与唯一键冲突）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_finance_account_default` (`tenant_id`,`default_flag`),
  KEY `idx_erp_finance_account_name` (`tenant_id`,`name`),
  KEY `idx_erp_finance_account_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_finance_account_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_finance_account_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_finance_receipt` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '收款单号：SK+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `customer_id` bigint NOT NULL COMMENT 'ERP 客户 ID（明细挂单客户必须一致）',
  `account_id` bigint NOT NULL COMMENT '结算账户 ID（erp_finance_account）',
  `receipt_user_id` bigint NOT NULL DEFAULT '0' COMMENT '经手人用户 ID（纯记录字段，0 = 未记录）',
  `receipt_time` datetime(3) NOT NULL COMMENT '收款时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核时校验核销上限并回写）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_finance_receipt_no` (`tenant_id`,`no`),
  KEY `idx_erp_finance_receipt_customer` (`tenant_id`,`customer_id`),
  KEY `idx_erp_finance_receipt_account` (`tenant_id`,`account_id`),
  KEY `idx_erp_finance_receipt_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_finance_receipt_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_finance_receipt_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_finance_receipt_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `receipt_id` bigint NOT NULL COMMENT '收款单 ID（主表）',
  `biz_type` varchar(32) NOT NULL COMMENT '核销业务类型：sale_out 销售出库 / sale_return 销售退货（与单据表名一致）',
  `biz_id` bigint NOT NULL COMMENT '被核销单据 ID（必须已审核）',
  `price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '本次核销金额（必须 > 0；审核时按单据上限校验）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_finance_receipt_items_doc` (`tenant_id`,`receipt_id`),
  KEY `idx_erp_finance_receipt_items_biz` (`tenant_id`,`biz_type`,`biz_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_finance_payment` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '付款单号：FK+yyyyMMdd+4位序号，租户内唯一（erp_sequence 原子发放，客户端不采纳）',
  `supplier_id` bigint NOT NULL COMMENT 'ERP 供应商 ID（明细挂单供应商必须一致）',
  `account_id` bigint NOT NULL COMMENT '结算账户 ID（erp_finance_account）',
  `payment_user_id` bigint NOT NULL DEFAULT '0' COMMENT '经手人用户 ID（纯记录字段，0 = 未记录）',
  `payment_time` datetime(3) NOT NULL COMMENT '付款时间（业务时区）',
  `status` tinyint NOT NULL DEFAULT '10' COMMENT '审核状态：10 未审核 / 20 已审核（审核时校验核销上限并回写）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_erp_finance_payment_no` (`tenant_id`,`no`),
  KEY `idx_erp_finance_payment_supplier` (`tenant_id`,`supplier_id`),
  KEY `idx_erp_finance_payment_account` (`tenant_id`,`account_id`),
  KEY `idx_erp_finance_payment_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_finance_payment_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_finance_payment_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_finance_payment_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `payment_id` bigint NOT NULL COMMENT '付款单 ID（主表）',
  `biz_type` varchar(32) NOT NULL COMMENT '核销业务类型：purchase_in 采购入库 / purchase_return 采购退货（与单据表名一致）',
  `biz_id` bigint NOT NULL COMMENT '被核销单据 ID（必须已审核）',
  `price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '本次核销金额（必须 > 0；审核时按单据上限校验）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_finance_payment_items_doc` (`tenant_id`,`payment_id`),
  KEY `idx_erp_finance_payment_items_biz` (`tenant_id`,`biz_type`,`biz_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_finance_payment_items`;
DROP TABLE IF EXISTS `erp_finance_payment`;
DROP TABLE IF EXISTS `erp_finance_receipt_items`;
DROP TABLE IF EXISTS `erp_finance_receipt`;
DROP TABLE IF EXISTS `erp_finance_account`;
