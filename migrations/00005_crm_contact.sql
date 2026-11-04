-- +goose Up
-- 联系人（issue #9，docs/crm-mvp-spec.md §3 联系人节）：客户内部的具体对接人。
-- 表结构沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。
-- customer_id 是外键语义关联（不设物理外键，与 00004 同风格）；联系人自身没有
-- owner_* 列，可见性完全跟随所属客户（§4.1：join 父记录后套用父谓词）。

CREATE TABLE `crm_contact` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `customer_id` bigint NOT NULL COMMENT '所属客户 ID（外键语义关联，客户删除后联系人随之不可见）',
  `name` varchar(128) NOT NULL COMMENT '联系人姓名',
  `position` varchar(64) DEFAULT NULL COMMENT '职位',
  `mobile` varchar(32) DEFAULT NULL COMMENT '手机号：11 位大陆手机号，可空',
  `email` varchar(128) DEFAULT NULL COMMENT '电子邮箱，可空',
  `gender` tinyint NOT NULL DEFAULT '0' COMMENT '性别：0 未知 / 1 男 / 2 女（对齐字典 sys_user_sex）',
  `decision_role` varchar(64) DEFAULT NULL COMMENT '决策角色',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  KEY `idx_crm_contact_tenant_customer` (`tenant_id`,`customer_id`),
  KEY `idx_crm_contact_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的表。00005 未引入其他结构或数据变更。
DROP TABLE IF EXISTS `crm_contact`;
