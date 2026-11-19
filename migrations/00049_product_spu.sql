-- +goose Up
-- mall 商品域第五张（issue #177，docs/mall-mvp-spec.md §3.2/§7）：
-- product_spu 商品（标准产品单元），category_id/brand_id 模块内引用
--（product_category/product_brand.id，经租户插件隔离后跨租户业务 ID 不可见；
-- 不设外键，照全仓先例，归属校验在 service 层）。name 租户内唯一：
-- UNIQUE(tenant_id, name)。spec_type 单规格/多规格（SKU 随 00050）；status
-- 0 下架 / 1 上架（对齐芋道 ProductSpuStatusEnum——只有上架可被购买，
-- 购买边界随 T5 生效）；market_price Money decimal(12,2)。
--
-- slider_pic_urls/detail 是 C 端展示数据（轮播图 JSON 数组、图文详情），
-- 管理端直填，MVP 不做装修。删除是软删：唯一键不含 deleted_at（照
-- member_level 先例：软删行保持占位，同名不可重建）；删除守卫「被订单
-- 引用禁删」随 T6 trade_order 落地接线（本票落 repo.CountOrdersBySPU
-- 预留恒 0 的谓词调用点），删除事务级联软删其 SKU（00050）。

CREATE TABLE `product_spu` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `category_id` bigint NOT NULL COMMENT '归属分类 ID（product_category.id，叶子分类）',
  `brand_id` bigint NOT NULL DEFAULT '0' COMMENT '品牌 ID（product_brand.id，0=未关联品牌）',
  `name` varchar(128) NOT NULL COMMENT '商品名称（租户内唯一）',
  `pic_url` varchar(255) DEFAULT NULL COMMENT '主图 URL',
  `slider_pic_urls` json NOT NULL COMMENT '轮播图 URL 数组（JSON，无轮播存 []）',
  `detail` text NULL COMMENT '图文详情（富文本/HTML，C 端展示）',
  `spec_type` tinyint NOT NULL DEFAULT '0' COMMENT '规格类型：0 单规格 / 1 多规格',
  `market_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '市场价格（Money，元）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 下架 / 1 上架（只有上架可被购买）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '排序（小值在前）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_spu_name` (`tenant_id`,`name`),
  KEY `idx_product_spu_category` (`tenant_id`,`category_id`),
  KEY `idx_product_spu_brand` (`tenant_id`,`brand_id`),
  KEY `idx_product_spu_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商品 SPU（租户级，name 租户内唯一）';

-- +goose Down
DROP TABLE IF EXISTS `product_spu`;
