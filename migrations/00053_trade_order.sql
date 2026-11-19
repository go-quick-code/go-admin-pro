-- +goose Up
-- mall 交易域第三张（issue #179，docs/mall-mvp-spec.md §3.3/§7）：
-- trade_order 订单主表。字段全量按 spec §3.3 一次建齐（T7 支付联动、T9
-- 超时关单只补代码不补表）：
--   - status 状态机五态（对齐芋道数值）：0 待支付 / 10 已支付 / 20 已完成 /
--     30 已取消 / 40 已关闭；迁移合法性纯函数见 service/trade_rules.go。
--   - no 订单号 SO 前缀（mall_sequence 事务内发放），UNIQUE(tenant_id, no)
--     不含 deleted_at（订单不可删，软删域单号占位口径同 member mobile）。
--   - 金额四列（Money decimal(12,2)）：item_money = Σ 订单项 subtotal；
--     pay_money = item_money − discount_money + postage（服务端定，客户端
--     金额不采纳）；discount_money/postage 预留恒 0（spec §7 裁决 8，照
--     pay fee_rate 先例）。
--   - 收货快照四列：下单时从 member_address 快照（地址日后改删不影响
--     历史订单）；receiver_area 为 province/city/district 空格拼接文本。
--   - type 预留（0 正常）；pay_* 三列随 T7 支付联动回写（先建后用）；
--     cancel_*/close_* 随取消（本票）与超时关单（T9）回写。

CREATE TABLE `trade_order` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `no` varchar(64) NOT NULL COMMENT '订单号（SO 前缀 + yyyyMMdd + 序号，mall_sequence 发放，服务端定）',
  `user_id` bigint NOT NULL COMMENT '下单会员 ID（member_user.id，越权谓词）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 待支付 / 10 已支付 / 20 已完成 / 30 已取消 / 40 已关闭',
  `type` tinyint NOT NULL DEFAULT '0' COMMENT '订单类型（预留：0 正常）',
  `user_remark` varchar(200) DEFAULT NULL COMMENT '买家备注（创建订单随单提交）',
  `admin_remark` varchar(500) DEFAULT NULL COMMENT '管理员备注（T3 管理端）',
  `pay_order_id` bigint NOT NULL DEFAULT '0' COMMENT '支付订单 ID（pay_order.id，T7 支付联动回写）',
  `pay_order_no` varchar(64) NOT NULL DEFAULT '' COMMENT '支付订单号（T7 回写）',
  `pay_channel_code` varchar(32) NOT NULL DEFAULT '' COMMENT '支付渠道编码（T7 回写）',
  `pay_time` datetime(3) DEFAULT NULL COMMENT '支付时间（T7 回写）',
  `cancel_time` datetime(3) DEFAULT NULL COMMENT '取消时间（本票取消回写）',
  `cancel_reason` varchar(200) DEFAULT NULL COMMENT '取消原因',
  `close_time` datetime(3) DEFAULT NULL COMMENT '关单时间（T9 超时关单回写）',
  `close_reason` varchar(200) DEFAULT NULL COMMENT '关单原因（T9 回写）',
  `total_quantity` int NOT NULL DEFAULT '0' COMMENT '订单总件数（Σ 订单项 count，服务端定）',
  `item_money` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '商品金额（Σ 订单项 subtotal，Money 元）',
  `discount_money` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '优惠金额（预留恒 0，spec §7 裁决 8）',
  `postage` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '邮费（预留恒 0，spec §7 裁决 8）',
  `pay_money` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '实付金额 = item_money − discount_money + postage（服务端定）',
  `receiver_name` varchar(64) NOT NULL COMMENT '收货人姓名（下单时快照）',
  `receiver_mobile` varchar(20) NOT NULL COMMENT '收货人电话（下单时快照）',
  `receiver_area` varchar(255) NOT NULL DEFAULT '' COMMENT '收货地区（province city district 空格拼接快照）',
  `receiver_detail` varchar(255) NOT NULL DEFAULT '' COMMENT '详细地址（下单时快照）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_trade_order_no` (`tenant_id`,`no`),
  KEY `idx_trade_order_user` (`tenant_id`,`user_id`,`status`),
  KEY `idx_trade_order_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='商城订单（租户级，状态机五态，金额服务端定）';

-- +goose Down
DROP TABLE IF EXISTS `trade_order`;
