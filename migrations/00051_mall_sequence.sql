-- +goose Up
-- mall 交易域第一张（issue #179，docs/mall-mvp-spec.md §3.3/§7）：
-- mall_sequence 商城单号序列。机制复制 pay_sequence（internal/pkg/sequence
-- 单源实现，ADR 0022）：mall 订单号 SO 前缀在 (tenant_id, biz_type, date_key)
-- 计数行上原子发放，事务内发放与订单落库同事务（service/trade.go 收口）。
-- biz_type 与业务表名一致（trade_order），日期键 yyyyMMdd 业务时区跨日自然重置。

CREATE TABLE `mall_sequence` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，序列按租户独立发放）',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型（与业务表名一致：trade_order）',
  `date_key` varchar(8) NOT NULL COMMENT '日期键 yyyyMMdd（业务时区，跨日自然重置）',
  `last_value` bigint NOT NULL DEFAULT '0' COMMENT '已发放的最大序号',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_mall_sequence_key` (`tenant_id`,`biz_type`,`date_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商城单号序列（机制复制 pay_sequence，spec §3.3）';

-- +goose Down
DROP TABLE IF EXISTS `mall_sequence`;
