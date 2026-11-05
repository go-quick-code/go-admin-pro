-- +goose Up
-- 商机与产品行项目（issue #15，docs/crm-mvp-spec.md §3 销售节/§4.4 状态机）：
-- 销售创建商机、挂产品行项目、推进阶段、标记赢单/输单。
-- 商机沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002），
-- owner-scoped 正体：owner_user_id/owner_dept_id 快照列承载数据范围谓词（ADR 0004，spec §4.1）。
-- 行项目是关联表：复合主键 + tenant_id，不设软删除与审计列（ADR 0002），同一商机同一产品只一行；
-- 行项目变更随商机整体保存（先删后插同事务），操作日志不走 owner_history 惯例。

CREATE TABLE `crm_opportunity` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `opportunity_no` varchar(32) NOT NULL COMMENT '商机编号：SJ+yyyyMMdd+4位序号，租户内唯一（crm_sequence 原子发放）',
  `customer_id` bigint NOT NULL COMMENT '所属客户 ID（外键语义关联，不设物理外键，与 00004/00005 同风格）',
  `name` varchar(128) NOT NULL COMMENT '商机名称',
  `stage` tinyint NOT NULL DEFAULT '1' COMMENT '阶段：1 初步 / 2 需求 / 3 报价 / 4 谈判 / 5 赢单 / 6 输单（5/6 终态，spec §4.4）',
  `expected_amount` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '预计成交金额（元，internal/pkg/money 十进制字符串出入；与行项目合计无强制不变量）',
  `expected_close_date` date DEFAULT NULL COMMENT '预计成交日期',
  `owner_user_id` bigint NOT NULL COMMENT '负责人用户 ID（商机必归属，无公海形态）',
  `owner_dept_id` bigint NOT NULL COMMENT '负责人部门快照，随负责人写入同步（ADR 0004）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_opportunity_tenant_no` (`tenant_id`,`opportunity_no`),
  KEY `idx_crm_opportunity_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_crm_opportunity_deleted_at` (`deleted_at`),
  KEY `idx_crm_opportunity_owner_user_id` (`owner_user_id`),
  KEY `idx_crm_opportunity_owner_dept_id` (`owner_dept_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 行项目关联表：amount = quantity × price 由服务端计算落库（客户端不提交金额，
-- #16 合同行项目同规则复用）；quantity 正整数、price 非负（service 层校验）。
CREATE TABLE `crm_opportunity_product` (
  `opportunity_id` bigint NOT NULL COMMENT '商机 ID（复合主键之一）',
  `product_id` bigint NOT NULL COMMENT '产品 ID（复合主键之一，被引用后产品禁删，service 引用校验）',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `quantity` int NOT NULL COMMENT '数量，正整数',
  `price` decimal(12,2) NOT NULL COMMENT '单价（元）',
  `amount` decimal(12,2) NOT NULL COMMENT '金额 = 数量 × 单价（服务端计算）',
  PRIMARY KEY (`opportunity_id`,`product_id`),
  KEY `idx_crm_opportunity_product_tenant_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 商机维度跟进（spec §3 跟进行：biz 维度二选一）：只新增 opportunity_id 可空列，
-- customer_id 维持 NOT NULL（挂商机时写 0 表示「无客户」，不动 00007 的既有列定义）；
-- 可见性：挂客户跟随客户负责人，挂商机跟随商机负责人（spec §4.1 父谓词）。
ALTER TABLE `crm_follow_up`
  ADD COLUMN `opportunity_id` bigint NULL DEFAULT NULL COMMENT '所属商机 ID（与 customer_id 二选一；NULL 即未挂商机）' AFTER `customer_id`,
  ADD KEY `idx_crm_follow_up_tenant_opportunity` (`tenant_id`,`opportunity_id`);

-- +goose Down
-- 回滚：恢复 00007 的跟进表结构，删除本迁移新建的两张表。
DROP TABLE IF EXISTS `crm_opportunity_product`;
DROP TABLE IF EXISTS `crm_opportunity`;
ALTER TABLE `crm_follow_up`
  DROP KEY `idx_crm_follow_up_tenant_opportunity`,
  DROP COLUMN `opportunity_id`;
