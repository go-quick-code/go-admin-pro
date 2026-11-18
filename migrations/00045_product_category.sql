-- +goose Up
-- mall 商品域第一张（issue #176，docs/mall-mvp-spec.md §3.2/§7）：
-- product_category 商品分类，两级树（parent_id 0=根；叶子才可挂 SPU——
-- SPU 表随 T4 建）。同级 name 唯一：UNIQUE(tenant_id, parent_id, name)
--（跨父级同名允许——「手机」既是根分类也是「数码」的子分类是合法业态）。
-- sort 同级排序、status 0 启用 / 1 停用（对齐芋道 CommonStatusEnum）。
--
-- 分层：租户级数据，登记 TenantTables；唯一键不含 deleted_at（照
-- member_level 先例：软删行保持占位，同父同名不可重建）。父级合法性
--（两级树/父必须是根级）与删除守卫（有子级禁删；被 SPU 引用禁删的引用
-- 计数随 T4 接线）在 service 层收口。

CREATE TABLE `product_category` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `parent_id` bigint NOT NULL DEFAULT '0' COMMENT '父分类 ID（0=根；两级树，父必须是根级）',
  `name` varchar(64) NOT NULL COMMENT '分类名称（同级唯一）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '同级排序（小值在前）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_category_name` (`tenant_id`,`parent_id`,`name`),
  KEY `idx_product_category_parent` (`tenant_id`,`parent_id`),
  KEY `idx_product_category_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品分类（租户级，两级树，同级 name 唯一）';

-- +goose Down
DROP TABLE IF EXISTS `product_category`;
