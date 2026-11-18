-- +goose Up
-- mall 会员域第三张（issue #174，docs/mall-mvp-spec.md §3.1/§7）：
-- member_address 会员收货地址。省市区存文本（不联动 system_area id，
-- 边界记录在 spec §3.1/§7.8——行政区划 id 联动留后续票）。
-- T1 只提供管理端「按会员查地址」的只读入口；C 端自助维护随 T2/T5。
--
-- 分层：租户级数据，登记 TenantTables；user_id 归属过滤在 service 层强制
--（跨会员 ID 一律不可见，不泄露存在性）。

CREATE TABLE `member_address` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL COMMENT '归属会员 ID（member_user.id）',
  `receiver_name` varchar(64) NOT NULL COMMENT '收货人姓名',
  `receiver_mobile` varchar(20) NOT NULL COMMENT '收货人手机号',
  `province` varchar(64) NOT NULL COMMENT '省（文本，不联动 system_area）',
  `city` varchar(64) NOT NULL COMMENT '市（文本）',
  `district` varchar(64) NOT NULL COMMENT '区/县（文本）',
  `detail` varchar(255) NOT NULL COMMENT '详细地址（街道/门牌）',
  `is_default` tinyint NOT NULL DEFAULT '0' COMMENT '默认地址：0 否 / 1 是',
  PRIMARY KEY (`id`),
  KEY `idx_member_address_user` (`tenant_id`,`user_id`),
  KEY `idx_member_address_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='会员收货地址（租户级，按会员查询）';

-- +goose Down
DROP TABLE IF EXISTS `member_address`;
