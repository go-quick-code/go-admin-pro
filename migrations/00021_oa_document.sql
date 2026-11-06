-- +goose Up
-- 文档目录与 ACL（issue #58，docs/oa-mvp-spec.md §3.8/§4.12）：
--   oa_document_folder      租户级目录树：parent_id 自引用（0 为顶层）、
--                           name、owner_id（目录管理权之一，另一个是
--                           oa:document:manage 权限码）、sort。
--   oa_document_folder_acl  目录显式 ACL（关联表，复合主键 + tenant_id，
--                           不设软删除）：target_type 0 全员 / 1 部门（含
--                           子树）/ 2 角色 / 3 用户；全员固定 target_id=0。
--   oa_document_file        文件归属与展示字段；文件本体在 infra_file
--                           （file_id）。本工单只建 schema 与模型，写入
--                           （上传/下载/删除）由 #59 承接。
--
-- 读权限判定（spec §4.12）：取「最近一个有显式 ACL 的祖先目录」（含自身），
-- 子目录 ACL 覆盖父目录而不是叠加；链上没有任何显式 ACL 时，只有 owner
-- 与持有 oa:document:manage 的用户可见（顶层目录创建时必须显式设置 ACL，
-- 因此正常数据不会出现无 ACL 顶层）。
--
-- 判定规则为纯函数（internal/module/oa/service/document_rule.go），仓储侧
-- 用单一递归 CTE 谓词复刻同一语义（repository/document.go）。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater +
-- tenant_id（ADR 0002）。三表全部纳入租户插件
--（internal/pkg/database/tenant_tables.go）。

CREATE TABLE `oa_document_folder` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `parent_id` bigint NOT NULL DEFAULT '0' COMMENT '父目录 ID（0 为顶层目录）',
  `name` varchar(100) NOT NULL COMMENT '目录名称（1–100 字）',
  `owner_id` bigint NOT NULL COMMENT '目录 owner（system_user，租户内）：owner 恒可见且可管理',
  `sort` int NOT NULL DEFAULT '0' COMMENT '同级显示顺序（小者在前）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_document_folder_tenant_parent` (`tenant_id`,`parent_id`,`sort`,`id`),
  KEY `idx_oa_document_folder_tenant_owner` (`tenant_id`,`owner_id`),
  KEY `idx_oa_document_folder_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_document_folder_acl` (
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `folder_id` bigint NOT NULL COMMENT '目录 ID（oa_document_folder.id）',
  `target_type` tinyint NOT NULL COMMENT 'ACL 主体：0 全员 / 1 部门（含子树）/ 2 角色 / 3 用户',
  `target_id` bigint NOT NULL DEFAULT '0' COMMENT '部门/角色/用户 ID（target_type=0 固定 0）',
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`tenant_id`,`folder_id`,`target_type`,`target_id`),
  KEY `idx_oa_document_folder_acl_lookup` (`tenant_id`,`target_type`,`target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_document_file` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `folder_id` bigint NOT NULL COMMENT '所属目录 ID（oa_document_folder.id）',
  `file_id` bigint NOT NULL COMMENT '文件本体 ID（infra_file.id）',
  `name` varchar(200) NOT NULL COMMENT '文件名（展示用）',
  `size` bigint NOT NULL DEFAULT '0' COMMENT '文件大小（字节）',
  `type` varchar(100) DEFAULT NULL COMMENT 'MIME 类型',
  `uploader_id` bigint NOT NULL COMMENT '上传人（system_user，租户内）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_document_file_tenant_file` (`tenant_id`,`file_id`),
  KEY `idx_oa_document_file_tenant_folder` (`tenant_id`,`folder_id`,`id`),
  KEY `idx_oa_document_file_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_document_file`;
DROP TABLE IF EXISTS `oa_document_folder_acl`;
DROP TABLE IF EXISTS `oa_document_folder`;
