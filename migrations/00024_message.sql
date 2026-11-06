-- +goose Up
-- 消息通道（issue #72，docs/platform-mvp-spec.md §3.2–§3.4 / §4.3 / ADR 0010）：
-- 渠道（provider 配置）与模板是平台级资源，只有平台租户（ID=1）可管理；
-- 发送日志是租户级 append-only 记录，走租户插件隔离。
--
-- 平台级表（message_channel、message_template）不登记 TenantTables：系统级
-- 读取由仓储层显式 tenant.Skip，避免被插件误加租户条件。实体表统一底座：
-- id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。
--
-- 密钥约定（spec §4.3.6 / §4.11）：config 以 JSON 明文存储（本轮不做 KMS），
-- 查询时把 password/secret/accessKeySecret 替换为 ******；更新时提交 ******
-- 表示保留原值。脱敏在 service 层完成，本表不存第二份密钥。
--
-- 发送日志（message_log）不设 deleted_at/updated_at/creator/updater：
-- 它是租户用量与审计依据，只能追加；手动重发更新原行（spec §4.3.7），
-- 管理员删除/清空为硬删除（spec §5.2）。

CREATE TABLE `message_channel` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `channel_type` varchar(16) NOT NULL COMMENT '渠道类型：sms / mail',
  `code` varchar(64) NOT NULL COMMENT '渠道编码（全局唯一；软删不释放编码）',
  `name` varchar(100) NOT NULL COMMENT '渠道名称',
  `provider` varchar(64) NOT NULL COMMENT 'provider 编码：log / smtp / aliyun_sms',
  `config` text COMMENT 'provider 配置 JSON；密钥字段查询时脱敏为 ******',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_message_channel_code` (`code`),
  KEY `idx_message_channel_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `message_template` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `channel_type` varchar(16) NOT NULL COMMENT '渠道类型：sms / mail',
  `code` varchar(64) NOT NULL COMMENT '模板编码（同一渠道类型内唯一）',
  `name` varchar(100) NOT NULL COMMENT '模板名称',
  `title` varchar(200) DEFAULT NULL COMMENT '邮件主题；短信为空',
  `content` text NOT NULL COMMENT '短信内容或邮件 HTML/纯文本；仅支持 {{name}} 占位',
  `params` varchar(1000) NOT NULL DEFAULT '[]' COMMENT 'JSON 数组，声明模板参数名',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_message_template_type_code` (`channel_type`,`code`),
  KEY `idx_message_template_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 发送日志（租户级 append-only）：一次发送/重发一行；重发更新原行的
-- status/error_message/provider_message_id/sent_at/retry_count，不新增行。
CREATE TABLE `message_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `tenant_id` bigint NOT NULL DEFAULT '1' COMMENT '租户 ID（租户插件隔离）',
  `channel_type` varchar(16) NOT NULL COMMENT '渠道类型：sms / mail',
  `channel_id` bigint NOT NULL COMMENT '渠道 ID（message_channel.id）',
  `template_code` varchar(64) NOT NULL COMMENT '模板编码',
  `recipient` varchar(255) NOT NULL COMMENT '手机号或邮箱',
  `title` varchar(200) DEFAULT NULL COMMENT '邮件主题',
  `content` text COMMENT '渲染后内容（截断 4000 字符）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 成功 / 1 失败',
  `error_message` varchar(1000) DEFAULT NULL COMMENT '失败原因（详情只落日志，不回显）',
  `provider_message_id` varchar(128) DEFAULT NULL COMMENT 'provider 返回的消息 ID',
  `retry_count` int NOT NULL DEFAULT '0' COMMENT '手动重发次数',
  `sent_at` datetime(3) DEFAULT NULL COMMENT '最近一次发送尝试时间',
  PRIMARY KEY (`id`),
  KEY `idx_message_log_tenant_created` (`tenant_id`,`created_at`),
  KEY `idx_message_log_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `message_log`;
DROP TABLE IF EXISTS `message_template`;
DROP TABLE IF EXISTS `message_channel`;
