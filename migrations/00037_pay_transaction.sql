-- +goose Up
-- 交易与回调域（issue #133，docs/pay-mvp-spec.md §3.2/§3.3/§7）：
-- pay_order / pay_order_extension / pay_refund / pay_transfer /
-- pay_notify_task / pay_notify_log，6 表一次建齐（refund/transfer/log 先建
-- 后用：#134 商户回调 job 写 log、#135 退款、#136 转账只补代码不补表）。
--
-- 分层（照 00035/00036 先例）：6 表全部租户级，登记 TenantTables，读写经
-- 租户插件自动限定（跨租户业务 ID 一律 404，spec §4.6）。幂等键（spec §4.2/
-- §4.4/§4.8）：订单 (tenant_id, app_id, merchant_order_id)、退款
-- (tenant_id, app_id, merchant_refund_id)、转账 (tenant_id, app_id,
-- merchant_transfer_id)——撞键返回原单不报错；跨应用/跨租户同号不冲突。
--
-- 编号（spec §4.9）：订单 no 为 ZF 前缀、退款 TK、转账 ZZ（pay_sequence
-- 事务内发放，客户端提交的 no 一律不采纳）；extension no = 订单号-发起序
--（ZF...-1）。单号租户内 UNIQUE（与 pay_wallet_transaction.no 同口径）。
--
-- 状态数值对齐芋道枚举：订单 0 待支付/10 成功/20 已退款/30 已关闭；
-- extension 0 处理中/10 成功/20 失败；退款 0 待处理/10 成功/20 失败；
-- 转账 0 等待/5 进行中/10 成功/20 关闭；回调任务 0 等待/10 成功/20 彻底失败/
-- 21 请求成功但结果失败/22 请求失败。
--
-- append-only（spec §4.10 资金事实）：交易单据不提供删除（软删列仅底座
-- 统一底座要求），管理页无删除按钮；pay_notify_log 是回调执行日志
--（append-only，response 截断 1KB）。
--
-- 金额一律 decimal(12,2)（money.Money 分定点读写，全程不经过 float64，
-- CODING_STANDARDS）；手续费两列保留、v1 恒 0（spec §1 明确不做）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `pay_order` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `app_id` bigint NOT NULL COMMENT '支付应用 ID（pay_app.id）',
  `no` varchar(32) NOT NULL COMMENT '支付订单号（ZF 前缀 + yyyyMMdd + 至少 4 位序号，pay_sequence 事务内发放，客户端提交不采纳）',
  `merchant_order_id` varchar(64) NOT NULL COMMENT '消费方单号（幂等键：UNIQUE(tenant_id, app_id, merchant_order_id)，撞键返回原单）',
  `user_id` bigint NOT NULL COMMENT '下单用户 ID（收银台/submit/mock 触发按本人校验）',
  `user_type` tinyint NOT NULL COMMENT '下单用户类型：1 会员（预留）/ 2 后台用户',
  `subject` varchar(100) NOT NULL COMMENT '商品标题',
  `body` varchar(500) DEFAULT NULL COMMENT '商品描述',
  `notify_url` varchar(500) DEFAULT NULL COMMENT '支付结果回调地址（下单时从应用 order_notify_url 快照，可被覆盖）',
  `price` decimal(12,2) NOT NULL COMMENT '实付金额（服务端定：消费方服务端传入并自负其责，收银台/submit 不收金额）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 待支付 / 10 成功 / 20 已退款 / 30 已关闭',
  `channel_code` varchar(32) NOT NULL DEFAULT '' COMMENT '最近一次发起的渠道码（下单为空，submit 时回写）',
  `channel_user_id` varchar(64) NOT NULL DEFAULT '' COMMENT '渠道用户标识（wallet 渠道为下单用户 ID）',
  `channel_order_no` varchar(64) NOT NULL DEFAULT '' COMMENT '渠道单号（渠道回调带回，mock 为 MOCK-<extension no>）',
  `channel_fee_rate` decimal(5,2) NOT NULL DEFAULT '0.00' COMMENT '渠道手续费率（字段保留，v1 恒 0）',
  `channel_fee_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '渠道手续费（字段保留，v1 恒 0）',
  `extension_id` bigint NOT NULL DEFAULT '0' COMMENT '最近一次发起的支付 extension ID（pay_order_extension.id）',
  `user_ip` varchar(64) DEFAULT NULL COMMENT '下单用户 IP',
  `expire_time` datetime(3) DEFAULT NULL COMMENT '订单过期时间（服务端设定，过期 submit 409；超时关单 job 置 30）',
  `success_time` datetime(3) DEFAULT NULL COMMENT '支付成功时间',
  `refund_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '已退款累计金额（部分退款累计 ≤ price，spec §4.4）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_order_merchant` (`tenant_id`,`app_id`,`merchant_order_id`),
  UNIQUE KEY `uk_pay_order_no` (`tenant_id`,`no`),
  KEY `idx_pay_order_app` (`app_id`),
  KEY `idx_pay_order_user` (`user_id`),
  KEY `idx_pay_order_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='支付订单（租户级，merchant_order_id 幂等，金额服务端定）';

CREATE TABLE `pay_order_extension` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(64) NOT NULL COMMENT '支付发起单号（订单号-发起序，如 ZF...-1；租户内 UNIQUE）',
  `order_id` bigint NOT NULL COMMENT '支付订单 ID（pay_order.id）',
  `channel_code` varchar(32) NOT NULL COMMENT '本次发起的渠道码',
  `user_ip` varchar(64) DEFAULT NULL COMMENT '发起用户 IP',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 处理中 / 10 成功 / 20 失败（一次发起尝试一条）',
  `channel_extras` text COMMENT '渠道扩展参数 JSON（预留）',
  `channel_error_code` varchar(64) DEFAULT NULL COMMENT '渠道错误码（失败分支）',
  `channel_error_msg` varchar(255) DEFAULT NULL COMMENT '渠道错误信息（失败分支）',
  `channel_notify_data` text COMMENT '末次回调原文（mock 模拟回调与真实渠道回调同口径落库）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_order_extension_no` (`tenant_id`,`no`),
  KEY `idx_pay_order_extension_order` (`order_id`),
  KEY `idx_pay_order_extension_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='支付订单 extension（租户级，一次发起尝试一条）';

CREATE TABLE `pay_refund` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '退款单号（TK 前缀，pay_sequence 事务内发放）',
  `app_id` bigint NOT NULL COMMENT '支付应用 ID',
  `order_id` bigint NOT NULL COMMENT '支付订单 ID',
  `order_no` varchar(32) NOT NULL COMMENT '原支付订单号快照',
  `merchant_order_id` varchar(64) NOT NULL COMMENT '原消费方单号快照',
  `merchant_refund_id` varchar(64) NOT NULL COMMENT '消费方退款单号（幂等键：UNIQUE(tenant_id, app_id, merchant_refund_id)，#135 使用）',
  `user_id` bigint NOT NULL COMMENT '下单用户 ID',
  `user_type` tinyint NOT NULL COMMENT '下单用户类型：1 会员 / 2 后台用户',
  `channel_code` varchar(32) NOT NULL COMMENT '退款渠道（原路退回）',
  `notify_url` varchar(500) DEFAULT NULL COMMENT '退款结果回调地址（下单时从应用 refund_notify_url 快照）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 待处理 / 10 成功 / 20 失败',
  `pay_price` decimal(12,2) NOT NULL COMMENT '原单实付快照',
  `refund_price` decimal(12,2) NOT NULL COMMENT '本次退款金额（累计 ≤ pay_price，spec §4.4）',
  `reason` varchar(255) DEFAULT NULL COMMENT '退款原因',
  `user_ip` varchar(64) DEFAULT NULL COMMENT '发起用户 IP',
  `channel_order_no` varchar(64) DEFAULT NULL COMMENT '渠道单号',
  `channel_refund_no` varchar(64) DEFAULT NULL COMMENT '渠道退款单号',
  `success_time` datetime(3) DEFAULT NULL COMMENT '退款成功时间',
  `channel_error_code` varchar(64) DEFAULT NULL COMMENT '渠道错误码（失败分支）',
  `channel_error_msg` varchar(255) DEFAULT NULL COMMENT '渠道错误信息（失败分支）',
  `channel_notify_data` text COMMENT '末次回调原文',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_refund_merchant` (`tenant_id`,`app_id`,`merchant_refund_id`),
  UNIQUE KEY `uk_pay_refund_no` (`tenant_id`,`no`),
  KEY `idx_pay_refund_order` (`order_id`),
  KEY `idx_pay_refund_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='退款单（租户级，#135 使用，先建后用）';

CREATE TABLE `pay_transfer` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `no` varchar(32) NOT NULL COMMENT '转账单号（ZZ 前缀，pay_sequence 事务内发放）',
  `app_id` bigint NOT NULL COMMENT '支付应用 ID',
  `merchant_transfer_id` varchar(64) NOT NULL COMMENT '消费方转账单号（幂等键：UNIQUE(tenant_id, app_id, merchant_transfer_id)，#136 使用）',
  `user_id` bigint NOT NULL DEFAULT '0' COMMENT '归属用户 ID（外部账户无归属时为 0，spec §3.2）',
  `user_type` tinyint NOT NULL DEFAULT '0' COMMENT '归属用户类型：0 无归属 / 1 会员 / 2 后台用户',
  `channel_code` varchar(32) NOT NULL COMMENT '转账渠道（wallet/mock）',
  `subject` varchar(100) NOT NULL COMMENT '转账标题',
  `price` decimal(12,2) NOT NULL COMMENT '转账金额',
  `user_account` varchar(64) DEFAULT NULL COMMENT '收款账号（wallet 渠道为目标用户标识）',
  `user_name` varchar(64) DEFAULT NULL COMMENT '收款人姓名',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 等待 / 5 进行中 / 10 成功 / 20 关闭',
  `success_time` datetime(3) DEFAULT NULL COMMENT '转账成功时间',
  `notify_url` varchar(500) DEFAULT NULL COMMENT '转账结果回调地址（下单时从应用 transfer_notify_url 快照）',
  `user_ip` varchar(64) DEFAULT NULL COMMENT '发起用户 IP',
  `channel_extras` text COMMENT '渠道扩展参数 JSON（预留）',
  `channel_transfer_no` varchar(64) DEFAULT NULL COMMENT '渠道转账单号',
  `channel_error_code` varchar(64) DEFAULT NULL COMMENT '渠道错误码（失败分支）',
  `channel_error_msg` varchar(255) DEFAULT NULL COMMENT '渠道错误信息（失败分支）',
  `channel_notify_data` text COMMENT '末次回调原文',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_transfer_merchant` (`tenant_id`,`app_id`,`merchant_transfer_id`),
  UNIQUE KEY `uk_pay_transfer_no` (`tenant_id`,`no`),
  KEY `idx_pay_transfer_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='转账单（租户级，#136 使用，先建后用）';

CREATE TABLE `pay_notify_task` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `app_id` bigint NOT NULL COMMENT '支付应用 ID',
  `type` tinyint NOT NULL COMMENT '通知类型：1 支付 / 2 退款 / 3 转账',
  `data_id` bigint NOT NULL COMMENT '对应单据 ID（type=1 为 pay_order.id）',
  `merchant_order_id` varchar(64) DEFAULT NULL COMMENT '消费方单号（type=1）',
  `merchant_refund_id` varchar(64) DEFAULT NULL COMMENT '消费方退款单号（type=2）',
  `merchant_transfer_id` varchar(64) DEFAULT NULL COMMENT '消费方转账单号（type=3）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 等待 / 10 成功 / 20 彻底失败 / 21 请求成功但结果失败 / 22 请求失败',
  `notify_url` varchar(500) NOT NULL COMMENT '商户回调地址（下单时快照）',
  `next_notify_time` datetime(3) DEFAULT NULL COMMENT '下次通知时间（重试序列 {15,15,30,180,1800,1800,1800,3600}，spec §4.5）',
  `last_execute_time` datetime(3) DEFAULT NULL COMMENT '上次执行时间',
  `notify_times` int NOT NULL DEFAULT '0' COMMENT '已通知次数（首次 + 8 重试共 9 次）',
  `max_notify_times` int NOT NULL DEFAULT '9' COMMENT '最大通知次数（=9，照芋道 NOTIFY_FREQUENCY）',
  PRIMARY KEY (`id`),
  KEY `idx_pay_notify_task_data` (`data_id`),
  KEY `idx_pay_notify_task_scan` (`status`,`next_notify_time`),
  KEY `idx_pay_notify_task_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商户回调任务（租户级，支付成功入账函数创建 type=1，job 重试 #134）';

CREATE TABLE `pay_notify_log` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `task_id` bigint NOT NULL COMMENT '回调任务 ID（pay_notify_task.id）',
  `notify_times` int NOT NULL COMMENT '第几次通知（append-only 逐次落行）',
  `response` varchar(1024) DEFAULT NULL COMMENT '响应摘要（截断 1KB，spec §3.3）',
  `status` tinyint NOT NULL COMMENT '本次结果：10 成功 / 21 请求成功但结果失败 / 22 请求失败',
  PRIMARY KEY (`id`),
  KEY `idx_pay_notify_log_task` (`task_id`),
  KEY `idx_pay_notify_log_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商户回调执行日志（租户级，append-only，#134 使用，先建后用）';

-- +goose Down
DROP TABLE IF EXISTS `pay_notify_log`;
DROP TABLE IF EXISTS `pay_notify_task`;
DROP TABLE IF EXISTS `pay_transfer`;
DROP TABLE IF EXISTS `pay_refund`;
DROP TABLE IF EXISTS `pay_order_extension`;
DROP TABLE IF EXISTS `pay_order`;
