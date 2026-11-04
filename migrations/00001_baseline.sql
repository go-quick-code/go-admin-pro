-- +goose Up
-- 基线：固化切换 goose 前 GORM AutoMigrate 建出的 17 张表结构与索引。
-- 后续表结构变更一律新增迁移文件，不改本文件。
-- IF NOT EXISTS 仅用于 DDL 中途失败后的重跑；全新库的建表结果不变。

CREATE TABLE IF NOT EXISTS `system_tenant` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `name` varchar(100) NOT NULL,
  `contact_name` varchar(64) DEFAULT NULL,
  `contact_mobile` varchar(32) DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '0',
  `domain` varchar(200) DEFAULT NULL,
  `expire_time` datetime(3) DEFAULT NULL,
  `account_count` bigint NOT NULL DEFAULT '0',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_tenant_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_user` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `username` varchar(64) NOT NULL,
  `password` varchar(128) NOT NULL,
  `nickname` varchar(64) NOT NULL,
  `remark` varchar(500) DEFAULT NULL,
  `dept_id` bigint DEFAULT NULL,
  `mobile` varchar(32) DEFAULT NULL,
  `email` varchar(128) DEFAULT NULL,
  `gender` tinyint NOT NULL DEFAULT '0',
  `avatar` varchar(512) DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '0',
  `login_ip` varchar(64) DEFAULT NULL,
  `login_date` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_user_deleted_at` (`deleted_at`),
  KEY `idx_system_user_tenant_id` (`tenant_id`),
  KEY `idx_system_user_dept_id` (`dept_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_role` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(64) NOT NULL,
  `code` varchar(64) NOT NULL,
  `sort` bigint NOT NULL DEFAULT '0',
  `data_scope` tinyint NOT NULL DEFAULT '1',
  `status` tinyint NOT NULL DEFAULT '0',
  `type` tinyint NOT NULL DEFAULT '2',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_role_deleted_at` (`deleted_at`),
  KEY `idx_system_role_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_menu` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `name` varchar(64) NOT NULL,
  `permission` varchar(128) DEFAULT NULL,
  `type` tinyint NOT NULL DEFAULT '2',
  `sort` bigint NOT NULL DEFAULT '0',
  `parent_id` bigint NOT NULL DEFAULT '0',
  `path` varchar(200) DEFAULT NULL,
  `component` varchar(200) DEFAULT NULL,
  `component_name` varchar(100) DEFAULT NULL,
  `icon` varchar(100) DEFAULT NULL,
  `visible` tinyint(1) NOT NULL DEFAULT '1',
  `status` tinyint NOT NULL DEFAULT '0',
  `keep_alive` tinyint(1) NOT NULL DEFAULT '1',
  PRIMARY KEY (`id`),
  KEY `idx_system_menu_deleted_at` (`deleted_at`),
  KEY `idx_system_menu_parent_id` (`parent_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_dept` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(64) NOT NULL,
  `parent_id` bigint NOT NULL DEFAULT '0',
  `sort` bigint NOT NULL DEFAULT '0',
  `leader_user_id` bigint NOT NULL DEFAULT '0',
  `phone` varchar(32) DEFAULT NULL,
  `email` varchar(128) DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '0',
  PRIMARY KEY (`id`),
  KEY `idx_system_dept_deleted_at` (`deleted_at`),
  KEY `idx_system_dept_tenant_id` (`tenant_id`),
  KEY `idx_system_dept_parent_id` (`parent_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_post` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(64) NOT NULL,
  `code` varchar(64) NOT NULL,
  `sort` bigint NOT NULL DEFAULT '0',
  `status` tinyint NOT NULL DEFAULT '0',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_post_deleted_at` (`deleted_at`),
  KEY `idx_system_post_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_user_role` (
  `user_id` bigint NOT NULL,
  `role_id` bigint NOT NULL,
  PRIMARY KEY (`user_id`,`role_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_user_post` (
  `user_id` bigint NOT NULL,
  `post_id` bigint NOT NULL,
  PRIMARY KEY (`user_id`,`post_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_role_menu` (
  `role_id` bigint NOT NULL,
  `menu_id` bigint NOT NULL,
  PRIMARY KEY (`role_id`,`menu_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_dict_type` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(100) NOT NULL,
  `type` varchar(100) NOT NULL,
  `status` tinyint NOT NULL DEFAULT '0',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_dict_type_deleted_at` (`deleted_at`),
  KEY `idx_system_dict_type_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_dict_data` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `dict_type` varchar(100) NOT NULL,
  `label` varchar(100) NOT NULL,
  `value` varchar(100) NOT NULL,
  `sort` bigint NOT NULL DEFAULT '0',
  `status` tinyint NOT NULL DEFAULT '0',
  `color_type` varchar(32) DEFAULT NULL,
  `css_class` varchar(100) DEFAULT NULL,
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_dict_data_deleted_at` (`deleted_at`),
  KEY `idx_system_dict_data_tenant_id` (`tenant_id`),
  KEY `idx_system_dict_data_dict_type` (`dict_type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_login_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint DEFAULT NULL,
  `username` varchar(64) DEFAULT NULL,
  `login_ip` varchar(64) DEFAULT NULL,
  `user_agent` varchar(512) DEFAULT NULL,
  `login_type` tinyint NOT NULL DEFAULT '1',
  `result` tinyint NOT NULL DEFAULT '0',
  `message` varchar(500) DEFAULT NULL,
  `login_time` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_login_log_deleted_at` (`deleted_at`),
  KEY `idx_system_login_log_tenant_id` (`tenant_id`),
  KEY `idx_system_login_log_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `system_operate_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint DEFAULT NULL,
  `username` varchar(64) DEFAULT NULL,
  `module` varchar(100) DEFAULT NULL,
  `name` varchar(100) DEFAULT NULL,
  `type` tinyint NOT NULL DEFAULT '0',
  `request_method` varchar(16) DEFAULT NULL,
  `request_url` varchar(500) DEFAULT NULL,
  `request_params` text,
  `status` tinyint NOT NULL DEFAULT '0',
  `error_msg` text,
  `operate_ip` varchar(64) DEFAULT NULL,
  `user_agent` varchar(512) DEFAULT NULL,
  `duration` bigint DEFAULT NULL,
  `operate_time` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_system_operate_log_deleted_at` (`deleted_at`),
  KEY `idx_system_operate_log_tenant_id` (`tenant_id`),
  KEY `idx_system_operate_log_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `infra_file` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(255) NOT NULL,
  `path` varchar(500) NOT NULL,
  `url` varchar(500) NOT NULL,
  `size` bigint DEFAULT NULL,
  `type` varchar(100) DEFAULT NULL,
  `provider` varchar(32) NOT NULL DEFAULT 'local',
  PRIMARY KEY (`id`),
  KEY `idx_infra_file_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `infra_config` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `category` varchar(64) NOT NULL DEFAULT 'default',
  `name` varchar(100) NOT NULL,
  `config_key` varchar(200) NOT NULL,
  `value` text,
  `type` varchar(32) NOT NULL DEFAULT 'string',
  `visible` tinyint(1) NOT NULL DEFAULT '1',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_infra_config_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `infra_job` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(100) NOT NULL,
  `handler_name` varchar(100) NOT NULL,
  `handler_param` varchar(500) DEFAULT NULL,
  `cron_expression` varchar(100) NOT NULL,
  `retry_count` bigint NOT NULL DEFAULT '0',
  `status` tinyint NOT NULL DEFAULT '1',
  `remark` varchar(500) DEFAULT NULL,
  `last_run_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_infra_job_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `infra_job_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `job_id` bigint NOT NULL,
  `handler_name` varchar(100) DEFAULT NULL,
  `handler_param` varchar(500) DEFAULT NULL,
  `cron_expression` varchar(100) DEFAULT NULL,
  `begin_time` datetime(3) DEFAULT NULL,
  `end_time` datetime(3) DEFAULT NULL,
  `duration` bigint DEFAULT NULL,
  `result` tinyint NOT NULL DEFAULT '0',
  `message` text,
  PRIMARY KEY (`id`),
  KEY `idx_infra_job_log_tenant_id` (`tenant_id`),
  KEY `idx_infra_job_log_job_id` (`job_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `infra_job_log`;
DROP TABLE IF EXISTS `infra_job`;
DROP TABLE IF EXISTS `infra_config`;
DROP TABLE IF EXISTS `infra_file`;
DROP TABLE IF EXISTS `system_operate_log`;
DROP TABLE IF EXISTS `system_login_log`;
DROP TABLE IF EXISTS `system_dict_data`;
DROP TABLE IF EXISTS `system_dict_type`;
DROP TABLE IF EXISTS `system_role_menu`;
DROP TABLE IF EXISTS `system_user_post`;
DROP TABLE IF EXISTS `system_user_role`;
DROP TABLE IF EXISTS `system_post`;
DROP TABLE IF EXISTS `system_dept`;
DROP TABLE IF EXISTS `system_menu`;
DROP TABLE IF EXISTS `system_role`;
DROP TABLE IF EXISTS `system_user`;
DROP TABLE IF EXISTS `system_tenant`;
