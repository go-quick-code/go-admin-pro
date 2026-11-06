-- +goose Up
-- API 访问日志（issue #80，docs/platform-mvp-spec.md §3.7/§4.6/§5.5，ADR 0014）：
-- 在 Auth 之后、OperateLog 之前采集全部受保护请求（含 GET），异步批量落库。
-- 回答「谁在什么时候访问了什么」：操作日志只记写请求，这里补全读语义。
--
-- append-only 表（同 message_log 的取舍）：不设 updated_at/deleted_at/
-- creator/updater——访问日志是审计依据，只能追加；管理员删除/清空为硬删除。
-- 不记录请求体；query 按参数名脱敏（token/access_token/password/secret/
-- refresh_token → ***）并截断 1024，UA 截断 500；request_id 限 64 字符。
--
-- 租户级表：登记 TenantTables（internal/pkg/database/tenant_tables.go），
-- 查询/删除经租户插件隔离，跨租户业务 ID 一律 404；保留期清理是系统级
-- 动作（cron cleanAccessLogs 每日 03:00），显式 tenant.Skip 跨租户删除。

CREATE TABLE `infra_api_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL COMMENT '请求完成时间（采集时刻，非落库时刻）',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL DEFAULT '0' COMMENT '登录用户 ID（system_user.id）',
  `username` varchar(64) NOT NULL DEFAULT '' COMMENT '登录用户名（冗余，便于检索）',
  `request_method` varchar(16) NOT NULL COMMENT 'HTTP 方法',
  `request_path` varchar(255) NOT NULL COMMENT '路由模板 c.FullPath()；未匹配路由时为原始 path',
  `request_query` varchar(1024) DEFAULT NULL COMMENT '脱敏后的 query（token/password 等参数值替换为 ***）',
  `response_status` int NOT NULL DEFAULT '0' COMMENT '响应状态码',
  `duration_ms` bigint NOT NULL DEFAULT '0' COMMENT '耗时（毫秒）',
  `client_ip` varchar(64) NOT NULL DEFAULT '' COMMENT '客户端 IP',
  `user_agent` varchar(500) DEFAULT NULL COMMENT 'User-Agent（截断 500）',
  `request_id` varchar(64) NOT NULL DEFAULT '' COMMENT '请求 ID：优先读 X-Request-Id 头，否则生成 UUID',
  PRIMARY KEY (`id`),
  KEY `idx_infra_api_log_tenant_created` (`tenant_id`,`created_at`),
  KEY `idx_infra_api_log_request_path` (`request_path`),
  KEY `idx_infra_api_log_response_status` (`response_status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `infra_api_log`;
