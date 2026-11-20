-- +goose Up
-- mall 商品域第二张（issue #176，docs/mall-mvp-spec.md §3.2/§7）：
-- product_brand 商品品牌，name 租户内唯一（撞键 409）；pic_url 品牌图、
-- status 0 启用 / 1 停用、remark 备注。SPU 引用（brand_id）随 T4 建 SPU
-- 表后接线，本票品牌删除无引用守卫（MVP 品牌可删，T4 收口引用约束）。
--
-- 分层：租户级数据，登记 TenantTables；唯一键不含 deleted_at（软删行
-- 保持占位），唯一键冲突由 service 层翻译 409。

CREATE TABLE `product_brand` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `name` varchar(64) NOT NULL COMMENT '品牌名称（租户内唯一）',
  `pic_url` varchar(255) DEFAULT NULL COMMENT '品牌图片 URL',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_brand_name` (`tenant_id`,`name`),
  KEY `idx_product_brand_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品品牌（租户级，name 租户内唯一）';

-- +goose Down
DROP TABLE IF EXISTS `product_brand`;
