-- +goose Up
-- 敏感词（issue #81，docs/platform-mvp-spec.md §3.8/§4.7/§5.6）：
-- 租户级词库（单词 1–50 字符、去空白后非空，单租户上限 5000 条），服务层用
-- rune trie 匹配（ASCII 不区分大小写），按租户缓存、CRUD 后失效；接入点
-- OA 公告（标题+正文）与 CRM 跟进（内容），命中 400 且不落库。
--
-- 唯一键 (tenant_id, word, deleted_at)：deleted_at 入键，软删后同一词可
-- 重新添加（与 system_social_user 同一取舍）；租户隔离靠租户插件
--（登记 TenantTables，internal/pkg/database/tenant_tables.go），
-- 跨租户业务 ID 一律 404（spec §4.11.4）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `system_sensitive_word` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `word` varchar(100) NOT NULL COMMENT '敏感词（去首尾空白后 1–50 字符）',
  `category` varchar(50) DEFAULT NULL COMMENT '分类（如 涉政/广告，自由标注）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（停用词不参与匹配）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_system_sensitive_word_tenant_word` (`tenant_id`,`word`,`deleted_at`),
  KEY `idx_system_sensitive_word_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `system_sensitive_word`;
