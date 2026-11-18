-- +goose Up
-- mall 会员域第五张（issue #174，docs/mall-mvp-spec.md §3.1/§7）：
-- member_point_record 积分流水。表结构一次建全（T1），唯一入账入口
--（事务内行锁读余额 + 非负 SQL 双保险 + 快照结存，照 pay wallet 流水先例）
-- 的 service 逻辑随 T2 落地——T1 无任何写入路径，余额可由流水重放验证。
--
-- biz_type 三类（spec §3.1）：order_pay 支付入账 / order_cancel 取消回扣 /
-- admin_adjust 管理员调整；point 正入负出；balance_after 是变动后余额快照。
--
-- 分层：租户级数据，登记 TenantTables。append-only（积分事实不提供删除，
-- 照 pay_wallet_transaction §4.10 口径）。

CREATE TABLE `member_point_record` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL COMMENT '归属会员 ID（member_user.id）',
  `biz_type` varchar(32) NOT NULL COMMENT '业务类型：order_pay 支付入账 / order_cancel 取消回扣 / admin_adjust 管理员调整',
  `biz_id` bigint NOT NULL DEFAULT '0' COMMENT '业务单据 ID（订单 ID 或 0；admin_adjust 恒 0）',
  `point` int NOT NULL COMMENT '变动积分（正入负出）',
  `balance_after` int NOT NULL COMMENT '变动后余额快照（可重放代数和一致）',
  `remark` varchar(500) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_member_point_record_user` (`tenant_id`,`user_id`),
  KEY `idx_member_point_record_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='积分流水（租户级，append-only，唯一入账入口写入）';

-- +goose Down
DROP TABLE IF EXISTS `member_point_record`;
