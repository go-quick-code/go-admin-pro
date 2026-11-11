-- +goose Up
-- 充值与套餐（issue #136，docs/pay-mvp-spec.md §3.4/§4.7/§4.10/§7）：
-- pay_wallet_recharge 充值单 + pay_wallet_recharge_package 充值套餐（钱包域
-- 4 张表的后 2 张，00036 建前 2 张）。
--
-- 分层（ADR 0021，照 00036 先例）：两表都是租户级数据，登记 TenantTables，
-- 读写经租户插件自动限定；条件更新（充值支付认领/退款列回写）走显式租户参数
-- 的裸 SQL（同 pay_order 认领口径），保证「状态认领」与租户条件同语句。
--
-- 充值单（充值挂一笔支付订单，spec §4.7）：
--   - 套餐快照：pay_price/bonus_price 建单时从套餐拷贝，后改套餐不影响已建
--     单（spec 验收：改套餐不影响已建单）；package_id 可空 = 自定义充值
--     （自定义 pay_price、bonus_price 恒 0）。
--   - total_price = pay_price + bonus_price：支付成功后钱包入账额（biz=1，
--     total_recharge 累计）；充值退款扣回额（biz=2，校验扣后 ≥ 0，
--     total_recharge 调减）。
--   - pay_status 0 未支付 / 1 已支付（支付成功联动置 1；条件更新即入账认领，
--     重复回调 0 行幂等跳过）；pay_order_id 反查键（submit 禁钱包渠道校验，
--     idx 索引支撑）；pay_channel_code/pay_time 支付回执。
--   - refund_status 0 无 / 10 成功 / 20 失败；refund_total_price/refund_pay_price
--     /refund_bonus_price 三列与 refund_time 退款成功回写（pay_refund_id 挂
--     退款单）。充值单不提供删除（资金事实 append-only，spec §4.10）。
--
-- 充值套餐：name/pay_price/bonus_price/status 四业务列（spec §3.4）；
-- 被充值单引用禁删（409，引用计数不带 deleted_at 过滤——充值单不可删，
-- 引用永久，spec §4.10）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `pay_wallet_recharge` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `wallet_id` bigint NOT NULL COMMENT '钱包 ID（pay_wallet.id，本人发起）',
  `total_price` decimal(12,2) NOT NULL COMMENT '入账总额（= pay_price + bonus_price；退款扣回额同口径）',
  `pay_price` decimal(12,2) NOT NULL COMMENT '实付金额（快照：套餐档位或自定义，支付订单金额同源）',
  `bonus_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '赠送金额（套餐快照；自定义充值恒 0）',
  `package_id` bigint DEFAULT NULL COMMENT '充值套餐 ID（pay_wallet_recharge_package.id，NULL = 自定义充值）',
  `pay_status` tinyint NOT NULL DEFAULT '0' COMMENT '支付状态：0 未支付 / 1 已支付（钱包已入账，条件更新即认领）',
  `pay_order_id` bigint NOT NULL COMMENT '支付订单 ID（pay_order.id，同事务挂单；反查禁钱包渠道）',
  `pay_channel_code` varchar(32) NOT NULL DEFAULT '' COMMENT '支付渠道码（支付成功回执，快照）',
  `pay_time` datetime(3) DEFAULT NULL COMMENT '支付成功时间',
  `pay_refund_id` bigint NOT NULL DEFAULT '0' COMMENT '退款单 ID（pay_refund.id，退款成功回写）',
  `refund_status` tinyint NOT NULL DEFAULT '0' COMMENT '退款状态：0 无 / 10 成功 / 20 失败',
  `refund_total_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '退款扣回总额（pay + bonus 全额，成功回写）',
  `refund_pay_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '退款实付部分（成功回写）',
  `refund_bonus_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '退款赠送部分（成功回写）',
  `refund_time` datetime(3) DEFAULT NULL COMMENT '退款成功时间',
  PRIMARY KEY (`id`),
  KEY `idx_pay_wallet_recharge_wallet` (`wallet_id`),
  KEY `idx_pay_wallet_recharge_pay_order` (`pay_order_id`),
  KEY `idx_pay_wallet_recharge_package` (`package_id`),
  KEY `idx_pay_wallet_recharge_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='钱包充值单（租户级，挂支付订单，套餐快照）';

CREATE TABLE `pay_wallet_recharge_package` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离）',
  `name` varchar(100) NOT NULL COMMENT '套餐名称',
  `pay_price` decimal(12,2) NOT NULL COMMENT '实付档位（建充值单时快照进单）',
  `bonus_price` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '赠送档位（快照进单）',
  `status` tinyint NOT NULL COMMENT '状态：0 启用 / 1 停用（停用套餐不可再选，已建单不受影响）',
  PRIMARY KEY (`id`),
  KEY `idx_pay_wallet_recharge_package_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='钱包充值套餐（租户级，档位快照进充值单）';

-- +goose Down
DROP TABLE IF EXISTS `pay_wallet_recharge_package`;
DROP TABLE IF EXISTS `pay_wallet_recharge`;
