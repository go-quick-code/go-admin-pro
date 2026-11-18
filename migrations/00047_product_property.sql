-- +goose Up
-- mall 商品域第三张（issue #176，docs/mall-mvp-spec.md §3.2/§7）：
-- product_property 属性名（「颜色」「版本」），name 租户内唯一；属性值在
-- product_property_value（00048）。MVP 规格只有 name 一列业务字段（无
-- status——芋道满配版才有，本仓规格 §3.2 未取）；供 T4 多规格 SKU 的
-- properties JSON 引用（{propertyId,propertyName,valueId,valueName}）。
--
-- 分层：租户级数据，登记 TenantTables；唯一键不含 deleted_at（软删行
-- 保持占位），唯一键冲突由 service 层翻译 409。

CREATE TABLE `product_property` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `name` varchar(64) NOT NULL COMMENT '属性名（租户内唯一，如 颜色）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_property_name` (`tenant_id`,`name`),
  KEY `idx_product_property_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品属性名（租户级，name 租户内唯一）';

-- +goose Down
DROP TABLE IF EXISTS `product_property`;
