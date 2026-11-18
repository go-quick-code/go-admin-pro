-- +goose Up
-- mall 会员域第二张（issue #174，docs/mall-mvp-spec.md §3.1/§7）：
-- member_level 会员等级。等级纯手工维护（MVP 明确不做经验值体系），
-- experience 是纯维护的阈值列；name 租户内唯一、level 排序值
-- UNIQUE(tenant_id, level)。种子一行「普通会员 level=1 experience=0」
--（internal/seed，表为空时幂等写入）。
--
-- 分层：租户级数据，登记 TenantTables；唯一键冲突由 service 层翻译 409。

CREATE TABLE `member_level` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `name` varchar(64) NOT NULL COMMENT '等级名称（租户内唯一）',
  `level` int NOT NULL COMMENT '等级值（排序，租户内唯一，小值低级）',
  `experience` int NOT NULL DEFAULT '0' COMMENT '经验阈值（纯维护列，MVP 无经验值体系）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_member_level_level` (`tenant_id`,`level`),
  UNIQUE KEY `uk_member_level_name` (`tenant_id`,`name`),
  KEY `idx_member_level_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='会员等级（租户级，level/name 租户内唯一）';

-- +goose Down
DROP TABLE IF EXISTS `member_level`;
