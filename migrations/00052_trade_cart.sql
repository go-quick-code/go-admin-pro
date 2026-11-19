-- +goose Up
-- mall 交易域第二张（issue #179，docs/mall-mvp-spec.md §3.3/§7）：
-- trade_cart 购物车。UNIQUE(tenant_id, user_id, sku_id) 兜底「同 SKU 加购
-- 合并」的并发：加购走 upsert 原子递增（首行插入或已有行计数相加）。键不含
-- deleted_at，购物车行删除走物理删除（Unscoped）——购物车是即时会话数据，
-- 无审计诉求；软删行会在唯一键上占位，后续同 SKU 加购的 upsert 命中死行
-- 要复活要么分叉，硬删一劳永逸（历史行为由订单与积分流水承载）。
--
-- selected tinyint 结算选中位（试算与下单只结算 selected=1 的行）；
-- count int 加购数量（正整数，纯函数校验，spec §3.4）。spu_id 冗余入列：
-- 加购时校验 SKU 归属 SPU 一致（service 层），免下单联表反查。

CREATE TABLE `trade_cart` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `user_id` bigint NOT NULL COMMENT '归属会员 ID（member_user.id，入唯一键）',
  `spu_id` bigint NOT NULL COMMENT '商品 SPU ID（product_spu.id，与 sku_id 同 SPU）',
  `sku_id` bigint NOT NULL COMMENT '商品 SKU ID（product_sku.id，入唯一键）',
  `count` int NOT NULL COMMENT '加购数量（正整数）',
  `selected` tinyint NOT NULL DEFAULT '1' COMMENT '结算选中位：0 未选 / 1 选中（默认加购即选中）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_trade_cart_user_sku` (`tenant_id`,`user_id`,`sku_id`),
  KEY `idx_trade_cart_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='购物车（租户级，同 SKU 加购合并数量）';

-- +goose Down
DROP TABLE IF EXISTS `trade_cart`;
