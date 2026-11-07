-- +goose Up
-- 社交登录骨架（issue #76，docs/platform-mvp-spec.md §3.5–§3.6 / §4.4 / ADR 0011）：
-- 只做第三方 OAuth2 客户端（用微信开放平台/钉钉/企微登录已绑定的本地用户），
-- 不做自家授权服务器；未绑定账号返回 409 业务码 1901，不自动建号。
--
-- system_social_client 是平台级资源（类似 message_channel）：只有平台租户
-- （ID=1）可管理，不登记 TenantTables，仓储直接读（插件只作用于登记表）。
-- client_secret 按密钥约定（spec §4.11.1）DB 明文存储、API 查询一律脱敏为
-- ******，更新时提交 ****** 表示保留原值；脱敏在 service 层完成。
--
-- system_social_user 是租户级绑定关系：登记 TenantTables，跨租户业务 ID
-- 一律 404（spec §4.11.4）。唯一键 (tenant_id, provider, openid, deleted_at)
-- ——不同租户可绑定同一 openid 的不同本地用户；deleted_at 入键保证解绑
-- （软删）后同一 openid 可重新绑定。raw_info 截断 4096 字节；不存三方
-- access token（spec §4.4.9，ADR 0011）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `system_social_client` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `code` varchar(64) NOT NULL COMMENT '客户端编码（平台内唯一；软删不释放编码）',
  `name` varchar(100) NOT NULL COMMENT '客户端名称',
  `provider` varchar(32) NOT NULL COMMENT '社交平台：wechat_open / dingtalk / wecom',
  `client_id` varchar(200) NOT NULL COMMENT '三方 AppID',
  `client_secret` varchar(500) NOT NULL COMMENT '三方密钥；查询脱敏为 ******',
  `authorize_url` varchar(500) DEFAULT NULL COMMENT '授权端点；为空时用 provider 默认值（#77）',
  `token_url` varchar(500) DEFAULT NULL COMMENT 'token 端点；为空时用 provider 默认值（#77）',
  `userinfo_url` varchar(500) DEFAULT NULL COMMENT '用户信息端点；为空时用 provider 默认值（#77）',
  `scopes` varchar(200) DEFAULT NULL COMMENT '授权 scope，空格分隔',
  `redirect_uri` varchar(500) NOT NULL COMMENT '回调地址，须与回调端点一致',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_system_social_client_code` (`code`),
  KEY `idx_system_social_client_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `system_social_user` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL COMMENT '绑定的本地用户（system_user.id）',
  `provider` varchar(32) NOT NULL COMMENT '社交平台：wechat_open / dingtalk / wecom',
  `openid` varchar(128) NOT NULL COMMENT '三方用户唯一标识',
  `unionid` varchar(128) DEFAULT NULL COMMENT '三方开放平台 UnionID，可空',
  `nickname` varchar(100) DEFAULT NULL COMMENT '三方昵称',
  `avatar` varchar(500) DEFAULT NULL COMMENT '三方头像',
  `raw_info` text COMMENT '三方原始信息 JSON（截断 4096 字节）；不回显',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用',
  `last_login_at` datetime(3) DEFAULT NULL COMMENT '最近一次社交登录时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_system_social_user_binding` (`tenant_id`,`provider`,`openid`,`deleted_at`),
  KEY `idx_system_social_user_tenant_user` (`tenant_id`,`user_id`),
  KEY `idx_system_social_user_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `system_social_user`;
DROP TABLE IF EXISTS `system_social_client`;
