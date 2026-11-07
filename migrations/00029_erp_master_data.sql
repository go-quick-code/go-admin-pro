-- +goose Up
-- ERP 主数据六表（issue #88，docs/erp-mvp-spec.md §3.1/§4.10）：一次建齐
-- 分类/单位/产品/仓库/客户/供应商，本票只实现产品域三张（分类/单位/产品）的
-- 代码，仓库/客户/供应商由 #89 承接（只补代码不补表）。
--
-- 数据权限地基（spec §4.10，ADR-0004）：ERP 主数据的 owner = 创建人，恒有值
--（ERP 无公海），owner_user_id/owner_dept_id 承载数据范围谓词（与 CRM 同构，
-- 列模式见 00011 crm_contract）。仓库/客户/供应商同样带 owner 列，#89 落代码。
--
-- 状态口径（对齐芋道 ERP CommonStatusEnum）：0 启用（开启）/ 1 停用（禁用），
-- 分类/单位/产品三表同口径（与 CRM 产品的商品上下架 1 上架/0 下架刻意不同，勿混用）。
-- 数量列 decimal(24,3)（internal/pkg/qty 毫单位，ADR 0017）；金额列 decimal(12,2)
--（internal/pkg/money）；实体底座 id/created_at/updated_at/deleted_at/creator/updater
-- + tenant_id（ADR 0002）。租户隔离靠租户插件（登记 TenantTables，
-- internal/pkg/database/tenant_tables.go），跨租户业务 ID 一律 404。
-- 外键语义关联，不设物理外键（与 00004/00009 同风格）。

CREATE TABLE `erp_product_category` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `parent_id` bigint NOT NULL DEFAULT '0' COMMENT '父分类 ID，0 为根（无限层级树，前端组树）',
  `name` varchar(64) NOT NULL COMMENT '分类名称',
  `code` varchar(64) DEFAULT NULL COMMENT '分类编码',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序，越小越靠前',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_product_category_tenant_parent` (`tenant_id`,`parent_id`),
  KEY `idx_erp_product_category_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_product_category_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_product_category_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_product_unit` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(64) NOT NULL COMMENT '单位名称（仅名称字典，无换算率）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_product_unit_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_product_unit_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_product_unit_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_product` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(128) NOT NULL COMMENT '产品名称',
  `bar_code` varchar(64) DEFAULT NULL COMMENT '商品条码',
  `category_id` bigint NOT NULL COMMENT '产品分类 ID（分类被产品引用禁删，spec §4.9）',
  `unit_id` bigint NOT NULL COMMENT '产品单位 ID（单位被产品引用禁删，spec §4.9）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `standard` varchar(128) DEFAULT NULL COMMENT '规格',
  `expiry_day` int DEFAULT NULL COMMENT '保质期天数（纯展示，无逻辑，NULL=未填写）',
  `weight` decimal(12,3) DEFAULT NULL COMMENT '重量（kg，纯展示，qty 定点承载，NULL=未填写）',
  `purchase_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '采购参考价（元，无逻辑，默认 0.00）',
  `sale_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '销售参考价（元，无逻辑，默认 0.00）',
  `min_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '最低参考价（元，无逻辑，默认 0.00）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_product_category` (`tenant_id`,`category_id`),
  KEY `idx_erp_product_unit` (`tenant_id`,`unit_id`),
  KEY `idx_erp_product_bar_code` (`tenant_id`,`bar_code`),
  KEY `idx_erp_product_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_product_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_product_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_warehouse` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(64) NOT NULL COMMENT '仓库名称',
  `address` varchar(255) DEFAULT NULL COMMENT '仓库地址',
  `principal` varchar(64) DEFAULT NULL COMMENT '负责人（纯展示，不做独立权限，spec §4.10）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序，越小越靠前',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_warehouse_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_warehouse_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_warehouse_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_customer` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(128) NOT NULL COMMENT '客户名称',
  `contact` varchar(64) DEFAULT NULL COMMENT '联系人',
  `mobile` varchar(32) DEFAULT NULL COMMENT '手机号码',
  `telephone` varchar(32) DEFAULT NULL COMMENT '联系电话',
  `email` varchar(128) DEFAULT NULL COMMENT '电子邮箱',
  `fax` varchar(64) DEFAULT NULL COMMENT '传真',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序，越小越靠前',
  `tax_no` varchar(64) DEFAULT NULL COMMENT '税号（纯展示）',
  `tax_percent` decimal(5,2) DEFAULT NULL COMMENT '税率（%，纯展示，不做税额计算）',
  `bank_name` varchar(128) DEFAULT NULL COMMENT '开户行',
  `bank_account` varchar(64) DEFAULT NULL COMMENT '开户账号',
  `bank_address` varchar(255) DEFAULT NULL COMMENT '开户行地址',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_customer_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_customer_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_customer_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `erp_supplier` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(128) NOT NULL COMMENT '供应商名称',
  `contact` varchar(64) DEFAULT NULL COMMENT '联系人',
  `mobile` varchar(32) DEFAULT NULL COMMENT '手机号码',
  `telephone` varchar(32) DEFAULT NULL COMMENT '联系电话',
  `email` varchar(128) DEFAULT NULL COMMENT '电子邮箱',
  `fax` varchar(64) DEFAULT NULL COMMENT '传真',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序，越小越靠前',
  `tax_no` varchar(64) DEFAULT NULL COMMENT '税号（纯展示）',
  `tax_percent` decimal(5,2) DEFAULT NULL COMMENT '税率（%，纯展示，不做税额计算）',
  `bank_name` varchar(128) DEFAULT NULL COMMENT '开户行',
  `bank_account` varchar(64) DEFAULT NULL COMMENT '开户账号',
  `bank_address` varchar(255) DEFAULT NULL COMMENT '开户行地址',
  `owner_user_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人用户 ID（= 创建人，owner 恒有值，spec §4.10）',
  `owner_dept_id` bigint NOT NULL DEFAULT '0' COMMENT '负责人部门快照（数据范围谓词列，ADR 0004）',
  PRIMARY KEY (`id`),
  KEY `idx_erp_supplier_owner_user` (`tenant_id`,`owner_user_id`),
  KEY `idx_erp_supplier_owner_dept` (`tenant_id`,`owner_dept_id`),
  KEY `idx_erp_supplier_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `erp_supplier`;
DROP TABLE IF EXISTS `erp_customer`;
DROP TABLE IF EXISTS `erp_warehouse`;
DROP TABLE IF EXISTS `erp_product`;
DROP TABLE IF EXISTS `erp_product_unit`;
DROP TABLE IF EXISTS `erp_product_category`;
