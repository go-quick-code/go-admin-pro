-- +goose Up
-- 支付应用与渠道域（issue #131，docs/pay-mvp-spec.md §3.1/§4.1/§4.9/§7）：
-- pay 模块第一张 tracer bullet，4 表一次建齐。
--
-- 分层（ADR 0010 先例，ADR 0021 范围）：
--   - pay_channel 是平台级表（凭证/启停全平台共享）：不登记 TenantTables，
--     登记进 PlatformTables；14 行种子常驻（mock/wallet 启用、其余停用），
--     只启停不禁删——本迁移不写 DELETE 语义，service 层也不提供删除端点。
--   - pay_app / pay_app_channel / pay_sequence 是租户级表，登记 TenantTables，
--     读写经租户插件自动限定（跨租户业务 ID 一律 404）。
--
-- 凭证三段式（照 message_channel 先例，spec §4.1）：config 以 JSON 明文存储
--（v1 不做加密，ADR 0021 显式接受），查询时密钥字段替换为 ******、更新时
-- 提交 ****** 表示保留原值；脱敏在 service 层完成，本表不存第二份密钥。
--
-- 序列表（spec §4.9，机制复制 erp_sequence）：事务内 INSERT ... ON DUPLICATE
-- KEY UPDATE 原子递增后同事务回读；biz_type 与业务表名一致
--（pay_order/pay_refund/pay_transfer/pay_wallet_transaction）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `pay_channel` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `code` varchar(32) NOT NULL COMMENT '渠道编码（14 个常量之一，UNIQUE，软删不释放）',
  `name` varchar(100) NOT NULL COMMENT '渠道名称',
  `status` tinyint NOT NULL DEFAULT '1' COMMENT '状态：0 启用 / 1 停用（缺省停用，种子逐行显式赋值）',
  `fee_rate` decimal(5,2) NOT NULL DEFAULT '0.00' COMMENT '手续费率（纯记录，v1 恒 0，spec §1 明确不做手续费计算）',
  `config` text COMMENT '渠道凭证 JSON（明文落库；查询脱敏 ******、更新占位符合并，照 message_channel 三段式）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_channel_code` (`code`),
  KEY `idx_pay_channel_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='支付渠道（平台级：14 行种子常驻，禁删只启停）';

CREATE TABLE `pay_app` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `app_key` varchar(64) NOT NULL COMMENT '应用标识（租户内 UNIQUE，消费方下单凭据）',
  `name` varchar(100) NOT NULL COMMENT '应用名称',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `order_notify_url` varchar(500) DEFAULT NULL COMMENT '支付结果回调地址（下单时快照，可被覆盖）',
  `refund_notify_url` varchar(500) DEFAULT NULL COMMENT '退款结果回调地址',
  `transfer_notify_url` varchar(500) DEFAULT NULL COMMENT '转账结果回调地址',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_app_tenant_key` (`tenant_id`,`app_key`),
  KEY `idx_pay_app_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='支付应用（租户级）';

CREATE TABLE `pay_app_channel` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `app_id` bigint NOT NULL COMMENT '应用 ID（pay_app.id）',
  `channel_code` varchar(32) NOT NULL COMMENT '渠道编码（绑定码 ∈ 14 渠道码常量 ∧ 平台渠道启用才可绑，spec §4.1）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_app_channel` (`tenant_id`,`app_id`,`channel_code`),
  KEY `idx_pay_app_channel_app` (`app_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='应用渠道绑定（租户级，纯绑定关系可增删，全量替换）';

CREATE TABLE `pay_sequence` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，序列按租户独立发放）',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型（与业务表名一致：pay_order/pay_refund/pay_transfer/pay_wallet_transaction）',
  `date_key` varchar(8) NOT NULL COMMENT '日期键 yyyyMMdd（业务时区，跨日自然重置）',
  `last_value` bigint NOT NULL DEFAULT '0' COMMENT '已发放的最大序号',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_sequence_key` (`tenant_id`,`biz_type`,`date_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='支付单号序列（机制复制 erp_sequence，spec §4.9）';

-- +goose Down
DROP TABLE IF EXISTS `pay_sequence`;
DROP TABLE IF EXISTS `pay_app_channel`;
DROP TABLE IF EXISTS `pay_app`;
DROP TABLE IF EXISTS `pay_channel`;
