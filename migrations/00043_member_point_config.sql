-- +goose Up
-- mall 会员域第四张（issue #174，docs/mall-mvp-spec.md §3.1/§7）：
-- member_point_config 积分配置（租户级单行）。point_per_yuan = 1 元实付
-- 入账 N 积分；支付成功按实付入账的联动随 T2/T4 落地（T1 只建表，管理端
-- 读写入口随 T2）。
--
-- 分层：租户级数据，登记 TenantTables。单行语义由 service 层兜底：
-- 读不到行时按默认值（1）执行、首次编辑落行；UNIQUE(tenant_id) 防并发双写。

CREATE TABLE `member_point_config` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户级单行配置）',
  `point_per_yuan` int NOT NULL DEFAULT '1' COMMENT '1 元实付入账 N 积分（支付成功联动读此配置）',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_member_point_config_tenant` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='积分配置（租户级单行，point_per_yuan）';

-- +goose Down
DROP TABLE IF EXISTS `member_point_config`;
