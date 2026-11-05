-- +goose Up
-- 审计字段：17 张表统一补 creator/updater（varchar(64)，默认 '0' 即系统账号，见 ADR 0002）；
-- 关联表补 tenant_id；infra 三张实体表补 deleted_at，与统一 BaseEntity 对齐。
-- ADD COLUMN ... NOT NULL DEFAULT 会就地回填存量行：creator/updater 落 '0'（系统账号）、
-- 关联表 tenant_id 先落默认租户 1（仅作为占位，随后按父表回填真实租户，
-- 见文件末尾的 UPDATE ... JOIN），升级不丢数据、不留 NULL。

ALTER TABLE `system_tenant`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_user`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_role`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_menu`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_dept`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_post`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_dict_type`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_dict_data`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_login_log`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_operate_log`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

-- 关联表：复合主键 + tenant_id，不设软删除，变更由操作日志记录。
ALTER TABLE `system_user_role`
  ADD COLUMN `tenant_id` bigint NOT NULL DEFAULT '1' AFTER `role_id`,
  ADD INDEX `idx_system_user_role_tenant_id` (`tenant_id`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `tenant_id`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_user_post`
  ADD COLUMN `tenant_id` bigint NOT NULL DEFAULT '1' AFTER `post_id`,
  ADD INDEX `idx_system_user_post_tenant_id` (`tenant_id`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `tenant_id`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `system_role_menu`
  ADD COLUMN `tenant_id` bigint NOT NULL DEFAULT '1' AFTER `menu_id`,
  ADD INDEX `idx_system_role_menu_tenant_id` (`tenant_id`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `tenant_id`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

-- infra 实体表：补 deleted_at 对齐统一底座，软删除语义与其他实体表一致。
ALTER TABLE `infra_file`
  ADD COLUMN `deleted_at` datetime(3) DEFAULT NULL AFTER `updated_at`,
  ADD INDEX `idx_infra_file_deleted_at` (`deleted_at`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `infra_config`
  ADD COLUMN `deleted_at` datetime(3) DEFAULT NULL AFTER `updated_at`,
  ADD INDEX `idx_infra_config_deleted_at` (`deleted_at`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

ALTER TABLE `infra_job`
  ADD COLUMN `deleted_at` datetime(3) DEFAULT NULL AFTER `updated_at`,
  ADD INDEX `idx_infra_job_deleted_at` (`deleted_at`),
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `deleted_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

-- infra_job_log 是追加型执行日志（不套用实体底座），只补审计字段。
ALTER TABLE `infra_job_log`
  ADD COLUMN `creator` varchar(64) NOT NULL DEFAULT '0' AFTER `created_at`,
  ADD COLUMN `updater` varchar(64) NOT NULL DEFAULT '0' AFTER `creator`;

-- 存量多租户数据回填（PR #39 审查）：ADD COLUMN 的默认值 1 只是占位；
-- 在已有多个租户的库上升级时，关联行的 tenant_id 必须跟随父表，否则
-- 租户 2 的用户角色/岗位与角色菜单会在 JOIN 时被过滤掉（权限丢失）。
-- 空库上三条 UPDATE 不命中任何行，结果不变。
UPDATE `system_user_role` ur
  JOIN `system_user` u ON u.`id` = ur.`user_id`
  SET ur.`tenant_id` = u.`tenant_id`;

UPDATE `system_user_post` up
  JOIN `system_user` u ON u.`id` = up.`user_id`
  SET up.`tenant_id` = u.`tenant_id`;

UPDATE `system_role_menu` rm
  JOIN `system_role` r ON r.`id` = rm.`role_id`
  SET rm.`tenant_id` = r.`tenant_id`;

-- +goose Down
-- 回滚审计字段：删除 Up 段新增的列与索引。

ALTER TABLE `system_tenant` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_user` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_role` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_menu` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_dept` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_post` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_dict_type` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_dict_data` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_login_log` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_operate_log` DROP COLUMN `updater`, DROP COLUMN `creator`;

ALTER TABLE `system_user_role`
  DROP INDEX `idx_system_user_role_tenant_id`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `tenant_id`;

ALTER TABLE `system_user_post`
  DROP INDEX `idx_system_user_post_tenant_id`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `tenant_id`;

ALTER TABLE `system_role_menu`
  DROP INDEX `idx_system_role_menu_tenant_id`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `tenant_id`;

ALTER TABLE `infra_file`
  DROP INDEX `idx_infra_file_deleted_at`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `deleted_at`;

ALTER TABLE `infra_config`
  DROP INDEX `idx_infra_config_deleted_at`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `deleted_at`;

ALTER TABLE `infra_job`
  DROP INDEX `idx_infra_job_deleted_at`,
  DROP COLUMN `updater`,
  DROP COLUMN `creator`,
  DROP COLUMN `deleted_at`;

ALTER TABLE `infra_job_log` DROP COLUMN `updater`, DROP COLUMN `creator`;
