-- +goose Up
-- mall 会员域第一张（issue #174，docs/mall-mvp-spec.md §1/§3.1/§7）：
-- member_user 会员用户。对标芋道 ruoyi-vue-pro mall/member 的 member_user，
-- 兼容契约 C1 只对齐领域概念（mobile 注册 + bcrypt 密码 + 等级归属），
-- 不对齐 DDL。
--
-- 分层（ADR 0019）：
--   - 租户级数据，登记 TenantTables，读写经租户插件自动限定；
--     mobile 租户内唯一：UNIQUE(tenant_id, mobile)，唯一键不含 deleted_at
--     （照 crm_product code 先例）——会员业务身份不允许「软删后同号重建」
--     顶替旧号历史（C 端注册/登录按 mobile 定位，软删行保持占位）；
--     管理端无删除端点（停启用覆盖生命周期），service 层唯一冲突统一翻译 409。
--   - password_hash 为 bcrypt 哈希（照 system_user），明文密码不落库不回传。
--   - point_balance 是积分余额（int，不动 money）：T1 只建列，余额变动唯一
--     入账入口（member_point_record 流水）随 T2 落地；本表余额列恒 0 起步。
--   - status 0 启用 / 1 停用（数值对齐芋道 CommonStatusEnum；停用会员
--     C 端登录边界由 T2 校验）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `member_user` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `mobile` varchar(20) NOT NULL COMMENT '手机号（租户内唯一，C 端注册/登录身份）',
  `password_hash` varchar(100) NOT NULL DEFAULT '' COMMENT '密码哈希（bcrypt，照 system_user；管理端创建可不设密码留空串）',
  `nickname` varchar(64) NOT NULL DEFAULT '' COMMENT '昵称',
  `avatar_url` varchar(500) DEFAULT NULL COMMENT '头像 URL',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `level_id` bigint NOT NULL DEFAULT '0' COMMENT '等级 ID（member_level.id，0 = 未分配）',
  `point_balance` int NOT NULL DEFAULT '0' COMMENT '积分余额（唯一入账入口维护，任何代码不得直改）',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_member_user_mobile` (`tenant_id`,`mobile`),
  KEY `idx_member_user_level` (`level_id`),
  KEY `idx_member_user_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='会员用户（租户级，mobile 租户内唯一）';

-- +goose Down
DROP TABLE IF EXISTS `member_user`;
