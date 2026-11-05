-- +goose Up
-- 跟进记录·客户维度（issue #12，docs/crm-mvp-spec.md §3 客户节/§4.2）：
-- 销售对客户的一次沟通记录；新增跟进刷新所属客户 last_follow_time（同事务，见 service 层）。
-- 表结构沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。
-- customer_id 是外键语义关联（不设物理外键，与 00004/00005 同风格）；跟进自身没有
-- owner_* 列，可见性完全跟随所属客户（§4.1：join 父记录后套用父谓词）。
-- 规格的 biz_type/biz_id 多态设计在商机/合同维度工单落地，本迁移只建客户维度列。
-- 删除/编辑权限口径：创建人本人 或 crm:follow-up:update/delete（creator 即底座列，关联用户 ID 字符串）。

CREATE TABLE `crm_follow_up` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `customer_id` bigint NOT NULL COMMENT '所属客户 ID（外键语义关联，客户删除后跟进随之不可见）',
  `type` tinyint NOT NULL DEFAULT '5' COMMENT '跟进类型：1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他（芋道字典语义）',
  `content` varchar(1000) NOT NULL COMMENT '跟进内容（必填）',
  `next_follow_time` datetime(3) DEFAULT NULL COMMENT '下次跟进时间，可空',
  PRIMARY KEY (`id`),
  KEY `idx_crm_follow_up_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_crm_follow_up_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的表。00007 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_follow_up`;
