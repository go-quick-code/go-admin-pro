-- +goose Up
-- 租户套餐（issue #69，docs/platform-mvp-spec.md §3.1/§4.1，ADR 0012）。
-- 平台级资源：定义一组可访问菜单与资源配额，租户通过 system_tenant.package_id 绑定。
-- menu_ids 只存目录/菜单 ID（按钮在绑定套餐时按父菜单展开），JSON 数组文本。
-- 配额 0 表示不限；平台租户（ID=1）不使用套餐。
-- 本迁移同时为存量租户（ID>1）的 super_admin 回填全部菜单：权限语义从
-- 「所有租户超管通配」改为「仅平台租户通配」后，存量租户升级不丢权。

CREATE TABLE `system_tenant_package` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `name` varchar(100) NOT NULL COMMENT '套餐名称，平台内唯一',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `menu_ids` text NOT NULL COMMENT '菜单 ID JSON 数组（目录/菜单；按钮绑定套餐时展开）',
  `max_users` bigint NOT NULL DEFAULT '0' COMMENT '最大用户数，0=不限',
  `max_storage_mb` bigint NOT NULL DEFAULT '0' COMMENT '存储上限 MB，0=不限',
  `max_sms_monthly` bigint NOT NULL DEFAULT '0' COMMENT '每月短信条数，0=不限',
  `max_mail_monthly` bigint NOT NULL DEFAULT '0' COMMENT '每月邮件封数，0=不限',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_system_tenant_package_name` (`name`),
  KEY `idx_system_tenant_package_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE `system_tenant`
  ADD COLUMN `package_id` bigint NOT NULL DEFAULT '0' AFTER `status`,
  ADD INDEX `idx_system_tenant_package_id` (`package_id`);

-- 存量租户回填：租户 ID>1 的 super_admin 绑定全部未删除菜单。
-- INSERT IGNORE 避免与已有 role_menu 行冲突；回填行随迁移 Up 幂等。
INSERT IGNORE INTO `system_role_menu` (`role_id`, `menu_id`, `tenant_id`, `creator`, `updater`)
SELECT r.`id`, m.`id`, r.`tenant_id`, '0', '0'
FROM `system_role` r
JOIN `system_menu` m ON m.`deleted_at` IS NULL
WHERE r.`code` = 'super_admin'
  AND r.`tenant_id` <> 1
  AND r.`deleted_at` IS NULL;

-- +goose Down
-- 回填的 role_menu 行不回滚：它们可能是租户原有的合法授权，删除会误伤。
DROP TABLE IF EXISTS `system_tenant_package`;
ALTER TABLE `system_tenant`
  DROP INDEX `idx_system_tenant_package_id`,
  DROP COLUMN `package_id`;
