-- +goose Up
-- mall 商品域第六张（issue #177，docs/mall-mvp-spec.md §3.2/§7）：
-- product_sku 商品规格（库存单位），spu_id 模块内引用（product_spu.id）。
-- properties 是规格 JSON 快照：[{propertyId,propertyName,valueId,valueName}]
--（单规格为空数组）——propertyId/valueId 引用 product_property/_value 主数据，
-- 名称在落库时由 service 层按主数据反查写入（快照口径：属性值日后改名/软删
-- 不影响已建 SKU 的展示与 T6 订单成交快照）；引用存在性与 value 归属在
-- service 层校验（纯函数见 product_spu_rules.go，repo 查询）。
--
-- price 售价 Money decimal(12,2)（非负，纯函数校验）；stock 库存 int
-- NOT NULL DEFAULT 0（T6 下单行锁扣减 UPDATE stock = stock - ? WHERE
-- stock >= ?）；first tinyint 标记 SPU 展示主 SKU（每 SPU 恰一条 first=1，
-- 纯函数锁定）。SPU 删除事务级联软删其 SKU（本表 spu_id 无孤儿）。

CREATE TABLE `product_sku` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `spu_id` bigint NOT NULL COMMENT '归属 SPU ID（product_spu.id）',
  `properties` json NOT NULL COMMENT '规格 JSON [{propertyId,propertyName,valueId,valueName}]，单规格为 []',
  `price` decimal(12,2) NOT NULL COMMENT '售价（Money，元，非负）',
  `market_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '划线价（Money，元，非负）',
  `pic_url` varchar(255) DEFAULT NULL COMMENT '规格图 URL（缺省回落 SPU 主图）',
  `stock` int NOT NULL DEFAULT '0' COMMENT '库存（非负整数；T6 下单行锁扣减）',
  `first` tinyint NOT NULL DEFAULT '0' COMMENT 'SPU 展示主 SKU：0 否 / 1 是（每 SPU 恰一条）',
  PRIMARY KEY (`id`),
  KEY `idx_product_sku_spu` (`tenant_id`,`spu_id`),
  KEY `idx_product_sku_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品 SKU（租户级，SPU 删除级联软删）';

-- +goose Down
DROP TABLE IF EXISTS `product_sku`;
