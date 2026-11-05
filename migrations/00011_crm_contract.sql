-- +goose Up
-- 合同与产品行项目（issue #16，docs/crm-mvp-spec.md §3 销售节/§4.4 状态机/§4.3 金额口径）：
-- 销售从商机或直接创建合同、添加产品行项目、提交审批；v1 审批由 ApprovalService 直通实现。
-- 合同沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002），
-- owner-scoped 正体：owner_user_id/owner_dept_id 快照列承载数据范围谓词（ADR 0004，spec §4.1）。
-- 行项目是关联表：复合主键 + tenant_id，不设软删除与审计列（ADR 0002），同一合同同一产品只一行；
-- 行项目变更随合同整体保存（先删后插同事务），与商机行项目（00010）同一套口径。

CREATE TABLE `crm_contract` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `contract_no` varchar(32) NOT NULL COMMENT '合同编号：HT+yyyyMMdd+4位序号，租户内唯一（crm_sequence 原子发放）',
  `customer_id` bigint NOT NULL COMMENT '所属客户 ID（外键语义关联，不设物理外键，与 00004 同风格）',
  `opportunity_id` bigint DEFAULT NULL COMMENT '来源商机 ID（可空：合同可不经商机直接创建）',
  `name` varchar(128) NOT NULL COMMENT '合同名称',
  `amount` decimal(12,2) NOT NULL DEFAULT '0.00' COMMENT '合同金额（元，internal/pkg/money 十进制字符串出入）：有行项目时恒等于 SUM(行项目金额)、由服务端计算落库（客户端提交值忽略），无行项目时手填且 ≥ 0（spec §4.3）',
  `sign_date` date DEFAULT NULL COMMENT '签订日期',
  `start_date` date DEFAULT NULL COMMENT '生效（起）日期',
  `end_date` date DEFAULT NULL COMMENT '失效（止）日期',
  `status` tinyint NOT NULL DEFAULT '1' COMMENT '状态：1 草稿 / 2 待审 / 3 已签 / 4 作废（spec §4.4：草稿→待审→已签，待审可驳回回草稿，草稿/待审/已签可作废，作废为终态）',
  `reject_reason` varchar(500) DEFAULT NULL COMMENT '最近一次驳回原因（待审驳回回草稿时写入，重新提交时清空）',
  `owner_user_id` bigint NOT NULL COMMENT '负责人用户 ID（合同必归属，无公海形态）',
  `owner_dept_id` bigint NOT NULL COMMENT '负责人部门快照，随负责人写入同步（ADR 0004）',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_crm_contract_tenant_no` (`tenant_id`,`contract_no`),
  KEY `idx_crm_contract_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_crm_contract_tenant_opportunity` (`tenant_id`,`opportunity_id`),
  KEY `idx_crm_contract_deleted_at` (`deleted_at`),
  KEY `idx_crm_contract_owner_user_id` (`owner_user_id`),
  KEY `idx_crm_contract_owner_dept_id` (`owner_dept_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 行项目关联表：amount = quantity × price 由服务端计算落库（客户端不提交金额，
-- money.Mul 128 位中间结果防溢出）；quantity 正整数、price 非负（service 层校验）。
-- 被引用后产品禁删（ProductRefCountChecker，合同侧实现随 #16 注册）。
CREATE TABLE `crm_contract_product` (
  `contract_id` bigint NOT NULL COMMENT '合同 ID（复合主键之一）',
  `product_id` bigint NOT NULL COMMENT '产品 ID（复合主键之一，被引用后产品禁删，service 引用校验）',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `quantity` int NOT NULL COMMENT '数量，正整数',
  `price` decimal(12,2) NOT NULL COMMENT '单价（元）',
  `amount` decimal(12,2) NOT NULL COMMENT '金额 = 数量 × 单价（服务端计算）',
  PRIMARY KEY (`contract_id`,`product_id`),
  KEY `idx_crm_contract_product_tenant_product` (`tenant_id`,`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 合同维度跟进（spec §3 跟进行：单条跟进只挂一个所属对象）：只新增 contract_id 可空列，
-- 不动 00007/00010 的既有列定义；可见性：挂合同跟随合同负责人（spec §4.1 父谓词），
-- 合同跟进与商机跟进一样刷新所属客户的 last_follow_time（spec §4.2）。
ALTER TABLE `crm_follow_up`
  ADD COLUMN `contract_id` bigint NULL DEFAULT NULL COMMENT '所属合同 ID（customer_id/opportunity_id/contract_id 三选一；NULL 即未挂合同）' AFTER `opportunity_id`,
  ADD KEY `idx_crm_follow_up_tenant_contract` (`tenant_id`,`contract_id`);

-- +goose Down
-- 回滚：恢复 00007/00010 的跟进表结构，删除本迁移新建的两张表。
DROP TABLE IF EXISTS `crm_contract_product`;
DROP TABLE IF EXISTS `crm_contract`;
ALTER TABLE `crm_follow_up`
  DROP KEY `idx_crm_follow_up_tenant_contract`,
  DROP COLUMN `contract_id`;
