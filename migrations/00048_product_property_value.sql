-- +goose Up
-- mall 商品域第四张（issue #176，docs/mall-mvp-spec.md §3.2/§7）：
-- product_property_value 属性值（「颜色→红色」），property_id 归属属性名、
-- name 属性值；UNIQUE(tenant_id, property_id, name)——同属性下值唯一，
-- 不同属性可有同名值（「版本→标准」与「规格→标准」并存）。property_id
-- 是模块内引用（product_property.id），经租户插件隔离后跨租户业务 ID
-- 不可见；不设外键（照全仓先例，归属校验在 service 层）。
--
-- 分层：租户级数据，登记 TenantTables；唯一键不含 deleted_at（软删行
-- 保持占位），唯一键冲突由 service 层翻译 409。

CREATE TABLE `product_property_value` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `property_id` bigint NOT NULL COMMENT '归属属性名 ID（product_property.id）',
  `name` varchar(64) NOT NULL COMMENT '属性值（属性内唯一，如 红色）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_property_value_name` (`tenant_id`,`property_id`,`name`),
  KEY `idx_product_property_value_property` (`tenant_id`,`property_id`),
  KEY `idx_product_property_value_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品属性值（租户级，property_id+name 唯一）';

-- +goose Down
DROP TABLE IF EXISTS `product_property_value`;
