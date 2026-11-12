-- +goose Up
-- 示例业务域（issue #137，docs/pay-mvp-spec.md §3.5/§4.8/§7）：
-- pay_demo_order / pay_demo_withdraw，2 表一次建齐。示例业务是 pay 模块内
-- 唯一消费方（ADR 0021）：示例订单演示「下单 → 收银台支付 → 退款」全链路，
-- 示例提现演示「申请 → 审批 → 转账 → 回调联动」全链路（转账单挂单应用经
-- pay.recharge-app-key 解析，商户转账单号 WD-<提现单 ID>，见 service 层决策）。
--
-- 分层（照 00037/00038 先例）：2 表全部租户级，登记 TenantTables，读写经
-- 租户插件自动限定（跨租户业务 ID 一律 404）。联动回写（demo 状态认领）走
-- 显式租户参数的裸 SQL（同 ClaimRechargePaid 口径——回调联动端点是公开路由，
-- 上下文无租户，租户经关联单据派生后显式传入）。
--
-- 状态数值对齐芋道枚举：示例订单 pay_status 0 未支付/1 已支付（同充值单
-- 0/1 两态）；示例提现 status 0 等待/10 成功/20 关闭（spec §3.5）；type
-- 1 钱包/2 支付宝（预留，v1 拒绝创建）。
--
-- 联动列（pay_refund_id/refund_price/refund_time 与 pay_transfer_id/
-- transfer_channel_code/transfer_time/transfer_error_msg）由 #134 商户回调
-- 链路（demo 回调端点 → SetDemoNotifyHandler）回写，不在发起时直写——
-- task → job → demo 端点是唯一权威链路（spec §4.5/§4.8）。
--
-- 金额一律 decimal(12,2)（money.Money 分定点读写，全程不经过 float64，
-- CODING_STANDARDS）。示例数据无删除入口（联动事实 append-only）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `pay_demo_order` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL COMMENT '下单用户 ID（自助端点强制 = 登录主体，spec §4.11）',
  `spu_id` bigint NOT NULL COMMENT '演示商品 ID（硬编码字典，不建表，spec §3.5）',
  `spu_name` varchar(100) NOT NULL COMMENT '商品名称（下单时从字典快照）',
  `price` decimal(12,2) NOT NULL COMMENT '商品金额（服务端定：字典价，请求不收金额，spec §4.2）',
  `pay_status` tinyint NOT NULL DEFAULT '0' COMMENT '支付状态：0 未支付 / 1 已支付（回调联动回写，同充值单 0/1 两态）',
  `pay_order_id` bigint NOT NULL DEFAULT '0' COMMENT '支付订单 ID（创建时同事务挂单，merchant_order_id=DEMO-<id>）',
  `pay_time` datetime(3) DEFAULT NULL COMMENT '支付成功时间（回调联动回写）',
  `pay_channel_code` varchar(32) NOT NULL DEFAULT '' COMMENT '支付渠道（回调联动回写）',
  `pay_refund_id` bigint NOT NULL DEFAULT '0' COMMENT '退款单 ID（type=2 回调联动回写，末次退款）',
  `refund_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已退款累计（type=2 回调联动逐次累加）',
  `refund_time` datetime(3) DEFAULT NULL COMMENT '末次退款成功时间（回调联动回写）',
  PRIMARY KEY (`id`),
  KEY `idx_pay_demo_order_user` (`user_id`),
  KEY `idx_pay_demo_order_pay_order` (`pay_order_id`),
  KEY `idx_pay_demo_order_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='示例订单（租户级，下单→收银台支付→退款全链路演示）';

CREATE TABLE `pay_demo_withdraw` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `user_id` bigint NOT NULL COMMENT '申请人 ID（自助端点强制 = 登录主体，spec §4.11）',
  `subject` varchar(100) NOT NULL COMMENT '提现标题',
  `price` decimal(12,2) NOT NULL COMMENT '提现金额（服务端校验 > 0）',
  `user_account` varchar(64) NOT NULL COMMENT '收款账号（type=1 钱包为申请人用户 ID 字符串，服务端写入）',
  `user_name` varchar(64) DEFAULT NULL COMMENT '收款人姓名（登录用户名快照）',
  `type` tinyint NOT NULL DEFAULT '1' COMMENT '提现类型：1 钱包 / 2 支付宝（预留，v1 拒绝创建）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 等待 / 10 成功 / 20 关闭（审批不建状态中转：审批后回调联动 10，spec §4.8）',
  `pay_transfer_id` bigint NOT NULL DEFAULT '0' COMMENT '转账单 ID（审批时创建转账单后回写；0 = 未发起转账）',
  `transfer_channel_code` varchar(32) NOT NULL DEFAULT '' COMMENT '转账渠道（type=3 回调联动回写）',
  `transfer_time` datetime(3) DEFAULT NULL COMMENT '转账成功时间（type=3 回调联动回写）',
  `transfer_error_msg` varchar(255) DEFAULT NULL COMMENT '转账失败信息（失败报文联动回写；v1 转账同步成功，防御性保留）',
  PRIMARY KEY (`id`),
  KEY `idx_pay_demo_withdraw_user` (`user_id`),
  KEY `idx_pay_demo_withdraw_transfer` (`pay_transfer_id`),
  KEY `idx_pay_demo_withdraw_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='示例提现（租户级，申请→审批→转账→回调联动全链路演示）';

-- +goose Down
DROP TABLE IF EXISTS `pay_demo_withdraw`;
DROP TABLE IF EXISTS `pay_demo_order`;
