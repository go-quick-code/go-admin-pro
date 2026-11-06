-- +goose Up
-- 公告、受众与已读（issue #48，docs/oa-mvp-spec.md §3.4/§4.7）：
--   oa_announcement         公告正文（content 为 bluemonday 清洗后的 HTML）。
--                           状态 0 草稿 / 1 发布 / 2 撤回（spec §4.7：
--                           草稿 → 发布 → 撤回 → 重新发布；发布后不可删除）。
--                           audience_type=0 全员 / 1 自定义受众。
--   oa_announcement_target  自定义受众（部门/角色），复合主键
--                           (tenant_id, announcement_id, target_type, target_id)，
--                           关联表不设软删除（spec §3 表结构总则）。
--   oa_announcement_read    已读记录（追加型表，只保留业务时间，无
--                           creator/updater/软删除）；唯一键保证打开详情
--                           的已读写入幂等。读取侧（已读标记/统计）由 #49 承接。
-- 附件不建列：infra_file（biz_type='oa_announcement'、biz_id=公告 ID）。
-- 三表全部纳入租户插件（internal/pkg/database/tenant_tables.go）。

CREATE TABLE `oa_announcement` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `title` varchar(200) NOT NULL COMMENT '公告标题',
  `content` mediumtext NOT NULL COMMENT '公告正文（入库前经 bluemonday 白名单清洗的 HTML）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 草稿 / 1 发布 / 2 撤回（spec §4.7）',
  `audience_type` tinyint NOT NULL DEFAULT '0' COMMENT '受众类型：0 全员 / 1 自定义（部门子树 ∪ 角色）',
  `pinned` tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否置顶：置顶公告排在员工列表最前',
  `published_at` datetime(3) DEFAULT NULL COMMENT '最近一次发布时间（草稿为 NULL，重新发布时刷新）',
  `withdrawn_at` datetime(3) DEFAULT NULL COMMENT '撤回时间（重新发布时清空）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_announcement_tenant_status_pinned` (`tenant_id`,`status`,`pinned`,`published_at`),
  KEY `idx_oa_announcement_tenant_pinned_id` (`tenant_id`,`pinned`,`id`),
  KEY `idx_oa_announcement_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_announcement_target` (
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `announcement_id` bigint NOT NULL COMMENT '公告 ID（oa_announcement.id）',
  `target_type` tinyint NOT NULL COMMENT '受众类型：1 部门（含子树）/ 2 角色',
  `target_id` bigint NOT NULL COMMENT '部门 ID 或角色 ID',
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`tenant_id`,`announcement_id`,`target_type`,`target_id`),
  KEY `idx_oa_announcement_target_lookup` (`tenant_id`,`target_type`,`target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_announcement_read` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL COMMENT '已读记录创建时间（业务时间，追加型表）',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `announcement_id` bigint NOT NULL COMMENT '公告 ID（oa_announcement.id）',
  `user_id` bigint NOT NULL COMMENT '阅读人用户 ID（system_user.id，租户内）',
  `read_at` datetime(3) NOT NULL COMMENT '首次打开详情的时间（幂等：已存在不覆盖）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_announcement_read_user` (`tenant_id`,`announcement_id`,`user_id`),
  KEY `idx_oa_announcement_read_user` (`tenant_id`,`user_id`,`id`),
  KEY `idx_oa_announcement_read_announcement` (`tenant_id`,`announcement_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_announcement_read`;
DROP TABLE IF EXISTS `oa_announcement_target`;
DROP TABLE IF EXISTS `oa_announcement`;
