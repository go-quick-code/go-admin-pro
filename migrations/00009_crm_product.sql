-- +goose Up
-- 产品与产品分类（issue #11，docs/crm-mvp-spec.md §3 产品节）：销售维护产品分类树和产品，
-- 供商机/合同行项目引用（#15/#16）。两张表合一个迁移：分类与产品同纵切交付。
-- 表结构沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。
-- 产品是租户级共享资源：无 owner_user_id / owner_dept_id 列，不做数据范围（codegen ownerScoped=false 变体）。
-- 状态口径（对齐芋道）：分类用 CommonStatus（0 启用 / 1 停用，规格 §3 原文）；
-- 产品用商品上下架（1 上架 / 0 下架，芋道 ProductSpuStatusEnum），两表语义刻意不同，勿混用。

CREATE TABLE `crm_product_category` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `parent_id` bigint NOT NULL DEFAULT '0' COMMENT '父分类 ID，0 为根（树结构，codegen 不生成树逻辑，service 手写）',
  `name` varchar(64) NOT NULL COMMENT '分类名称',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序，越小越靠前',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（芋道 CommonStatus 口径，规格 §3）',
  PRIMARY KEY (`id`),
  KEY `idx_crm_product_category_tenant_parent` (`tenant_id`,`parent_id`),
  KEY `idx_crm_product_category_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `crm_product` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `category_id` bigint NOT NULL COMMENT '产品分类 ID（外键语义关联，不设物理外键，与 00004/00005 同风格）',
  `name` varchar(128) NOT NULL COMMENT '产品名称',
  `code` varchar(64) NOT NULL COMMENT '产品编码：用户填写，租户内唯一（软删行不释放编码，service 预检 + 唯一键兜底）',
  `unit` varchar(32) DEFAULT NULL COMMENT '单位（字典 crm_product_unit，种子随 #20 字典轮补齐）',
  `price` decimal(12,2) NOT NULL COMMENT '价格（元，internal/pkg/money 十进制字符串出入）',
  `status` tinyint NOT NULL DEFAULT '1' COMMENT '状态：1 上架 / 0 下架（芋道商品上下架口径）',
  `description` varchar(500) DEFAULT NULL COMMENT '描述',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_product_tenant_code` (`tenant_id`,`code`),
  KEY `idx_crm_product_tenant_category` (`tenant_id`,`category_id`),
  KEY `idx_crm_product_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的两张表。00009 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_product`;
DROP TABLE IF EXISTS `crm_product_category`;
