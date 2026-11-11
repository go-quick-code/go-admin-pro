-- +goose Up
-- 钱包域第一部分（issue #132，docs/pay-mvp-spec.md §3.4/§4.3/§4.9/§7）：
-- pay_wallet 余额账 + pay_wallet_transaction 流水账（4 张钱包表中的前 2 张；
-- 充值两表留给 00038）。
--
-- 分层（ADR 0021 范围，照 00035 先例）：
--   - 两表都是租户级数据，登记 TenantTables，读写经租户插件自动限定；
--     入账原语走显式租户参数的裸 SQL（同 erp_stock，保证「锁行 → 读写」
--     与租户条件在同一语句口径上）。
--   - pay_wallet 无冻结列（v1 无冻结语义，spec §3.4 差异清单）。
--   - 余额/统计列 decimal(12,2)，Go 侧经 money.Money（int64 分）读写，
--     全程不经过 float64（CODING_STANDARDS）。
--
-- 唯一入账入口（spec §4.3）：所有余额变动只走 service 层一个入账函数——
-- 事务内 SELECT ... FOR UPDATE 锁钱包行 → 校验 → 条件 UPDATE（出向
-- `AND balance + ? >= 0` SQL 双保险，对齐 ERP 负库存禁令）→ 插流水
-- （快照变动后 balance）。任何代码不得直改 balance；钱包懒创建由
-- UNIQUE(tenant_id, user_id, user_type) 兜底并发（冲突重读）。
--
-- 流水 append-only（spec §4.10：资金事实不提供删除）：biz_type 六类
--（1 充值/2 充值退款/3 支付/4 支付退款/5 余额调整（预留）/6 转账），
-- price 正入负出，no 为 LS 前缀单号（pay_sequence 事务内发放，spec §4.9）。
--
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater（ADR 0002）。

CREATE TABLE `pay_wallet` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `user_id` bigint NOT NULL COMMENT '归属用户 ID（后台用户或会员，配合 user_type）',
  `user_type` tinyint NOT NULL COMMENT '归属类型：1 会员（预留）/ 2 后台用户',
  `balance` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '余额（唯一入账入口维护，任何代码不得直改）',
  `total_recharge` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '累计充值（biz=1 累计 / biz=2 调减）',
  `total_expense` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '累计支出（biz=3 累计 / biz=4 调减）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_wallet_user` (`tenant_id`,`user_id`,`user_type`),
  KEY `idx_pay_wallet_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='钱包（租户级，懒创建，唯一入账入口维护余额）';

CREATE TABLE `pay_wallet_transaction` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL COMMENT '租户 ID（租户插件隔离，入唯一键）',
  `no` varchar(32) NOT NULL COMMENT '流水单号（LS 前缀 + yyyyMMdd + 至少 4 位序号，pay_sequence 事务内发放）',
  `wallet_id` bigint NOT NULL COMMENT '钱包 ID（pay_wallet.id）',
  `biz_type` tinyint NOT NULL COMMENT '业务类型：1 充值/2 充值退款/3 支付/4 支付退款/5 余额调整（预留无入口）/6 转账',
  `biz_id` bigint NOT NULL DEFAULT '0' COMMENT '业务单据 ID（对应单据表主键，暂无业务流时为 0）',
  `title` varchar(100) NOT NULL COMMENT '流水标题（流水抽屉展示）',
  `price` decimal(12,2) NOT NULL COMMENT '变动金额（正入负出）',
  `balance` decimal(12,2) NOT NULL COMMENT '变动后余额快照（可重放代数和一致）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pay_wallet_transaction_no` (`tenant_id`,`no`),
  KEY `idx_pay_wallet_transaction_wallet` (`wallet_id`),
  KEY `idx_pay_wallet_transaction_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='钱包流水（租户级，append-only，正入负出 + 结存快照）';

-- +goose Down
DROP TABLE IF EXISTS `pay_wallet_transaction`;
DROP TABLE IF EXISTS `pay_wallet`;
