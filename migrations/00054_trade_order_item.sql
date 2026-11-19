-- +goose Up
-- mall 交易域第四张（issue #179，docs/mall-mvp-spec.md §3.3/§7）：
-- trade_order_item 订单项。成交快照口径（spec §3.3）：spu_name/sku_pic/
-- sku_properties/price 下单时按商品现价与现值快照落库——商品日后改名/改价/
-- 改规格/软删不影响历史订单展示（sku_properties 为 SKU 规格 JSON 原样快照）。
-- subtotal = price × count（服务端定，纯函数试算见 trade_rules.go）；
-- spu_id 冗余入列承载 SPU 删除守卫的引用计数（repo.CountOrdersBySPU，
-- T4 预留谓词本票接线）。

CREATE TABLE `trade_order_item` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `order_id` bigint NOT NULL COMMENT '归属订单 ID（trade_order.id）',
  `user_id` bigint NOT NULL COMMENT '下单会员 ID（member_user.id，与主表一致）',
  `spu_id` bigint NOT NULL COMMENT '商品 SPU ID（product_spu.id，删除守卫引用计数）',
  `sku_id` bigint NOT NULL COMMENT '商品 SKU ID（product_sku.id）',
  `spu_name` varchar(128) NOT NULL COMMENT 'SPU 名称快照（成交时现值）',
  `sku_pic` varchar(255) DEFAULT NULL COMMENT 'SKU 图片快照（缺省回落 SPU 主图）',
  `sku_properties` json NOT NULL COMMENT 'SKU 规格 JSON 快照 [{propertyId,propertyName,valueId,valueName}]，单规格 []',
  `price` decimal(12,2) NOT NULL COMMENT '成交单价（Money 元，下单时 SKU 现价快照）',
  `count` int NOT NULL COMMENT '购买数量（正整数）',
  `subtotal` decimal(12,2) NOT NULL COMMENT '小计 = price × count（Money 元，服务端定）',
  PRIMARY KEY (`id`),
  KEY `idx_trade_order_item_order` (`tenant_id`,`order_id`),
  KEY `idx_trade_order_item_spu` (`tenant_id`,`spu_id`),
  KEY `idx_trade_order_item_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商城订单项（租户级，成交快照 append-once 随订单）';

-- +goose Down
DROP TABLE IF EXISTS `trade_order_item`;
