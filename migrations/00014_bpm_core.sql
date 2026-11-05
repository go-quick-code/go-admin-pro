-- +goose Up
-- bpm 内核四张表（issue #44，docs/oa-mvp-spec.md §3.1，ADR 0008：版本化线性审批链）：
--   bpm_process_definition  流程定义（key + version 版本化，nodes JSON 节点数组，三态）；
--   bpm_process_instance    流程实例（创建时锁定 definition_id/version；#46 落运行逻辑）；
--   bpm_task                审批任务（候选池物化为每候选人一条任务；#46 落运行逻辑）；
--   bpm_approval_record     审批记录（追加型：无软删除与 creator/updater，只留业务时间）。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater + tenant_id（ADR 0002）。
-- 四张表全部纳入租户插件（internal/pkg/database/tenant_tables.go）。
-- key 为 MySQL 保留字，DDL 与 ORM 生成的 SQL 均以反引号包裹。

CREATE TABLE `bpm_process_definition` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `key` varchar(64) NOT NULL COMMENT '流程标识：业务内唯一，版本历史按 key 聚合（spec §3.1）',
  `version` int NOT NULL COMMENT '版本号：创建草稿时按同 key 已有版本（含软删）max+1 发放，落库后不可变',
  `name` varchar(64) NOT NULL COMMENT '流程名称',
  `business_type` varchar(64) NOT NULL COMMENT '业务类型（如 crm_contract_approval），发布时要求已注册 BusinessHandler（spec §4.4）',
  `nodes` json NOT NULL COMMENT '有序审批节点数组：[{name,assigneeType,assigneeValue,deptId}]，六种审批人规则（spec §4.2）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 草稿 / 1 发布 / 2 停用（spec §4.1）',
  `published_at` datetime(3) DEFAULT NULL COMMENT '发布时间；草稿为 NULL',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_bpm_definition_tenant_key_version` (`tenant_id`,`key`,`version`),
  KEY `idx_bpm_definition_tenant_status` (`tenant_id`,`status`),
  KEY `idx_bpm_definition_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `bpm_process_instance` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `definition_id` bigint NOT NULL COMMENT '定义 ID：创建实例时锁定（spec §4.1）',
  `definition_key` varchar(64) NOT NULL COMMENT '定义 key 快照',
  `definition_version` int NOT NULL COMMENT '定义版本快照：后续定义变化不影响在途实例（spec §4.1）',
  `business_type` varchar(64) NOT NULL COMMENT '业务类型（BusinessHandler 注册键，spec §4.4）',
  `business_id` bigint NOT NULL COMMENT '业务记录 ID（业务模块自持表单，bpm 只存快照，ADR 0008）',
  `initiator_id` bigint NOT NULL COMMENT '发起人用户 ID',
  `title` varchar(128) NOT NULL COMMENT '标题快照（如 合同审批: HT001）',
  `summary` varchar(500) DEFAULT NULL COMMENT '摘要快照',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 运行中 / 1 通过 / 2 驳回 / 3 撤回（spec §4.3）',
  `started_at` datetime(3) NOT NULL COMMENT '发起时间',
  `ended_at` datetime(3) DEFAULT NULL COMMENT '结束时间（通过/驳回/撤回时落）',
  PRIMARY KEY (`id`),
  KEY `idx_bpm_instance_tenant_biz` (`tenant_id`,`business_type`,`business_id`),
  KEY `idx_bpm_instance_tenant_initiator` (`tenant_id`,`initiator_id`),
  KEY `idx_bpm_instance_definition_id` (`definition_id`),
  KEY `idx_bpm_instance_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `bpm_task` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `instance_id` bigint NOT NULL COMMENT '所属流程实例 ID',
  `node_index` int NOT NULL COMMENT '节点序号（0 起，线性链顺序，spec §4.3）',
  `node_name` varchar(64) NOT NULL COMMENT '节点名称快照',
  `assignee_id` bigint NOT NULL COMMENT '办理人用户 ID（候选池物化为每候选人一条任务，ADR 0008）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 待办 / 1 通过 / 2 驳回 / 3 转办 / 4 取消（spec §3.1）',
  `handled_at` datetime(3) DEFAULT NULL COMMENT '办理时间（通过/驳回时落；转办/取消由记录表追溯）',
  PRIMARY KEY (`id`),
  KEY `idx_bpm_task_tenant_assignee_status` (`tenant_id`,`assignee_id`,`status`),
  KEY `idx_bpm_task_instance_node_status` (`instance_id`,`node_index`,`status`),
  KEY `idx_bpm_task_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `bpm_approval_record` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `created_at` datetime(3) NOT NULL COMMENT '记录时间（追加型表的唯一业务时间，spec §3.1）',
  `instance_id` bigint NOT NULL COMMENT '所属流程实例 ID',
  `task_id` bigint NOT NULL DEFAULT '0' COMMENT '关联任务 ID；撤回等实例级动作落 0',
  `node_index` int NOT NULL COMMENT '节点序号',
  `node_name` varchar(64) DEFAULT NULL COMMENT '节点名称快照',
  `operator_id` bigint NOT NULL COMMENT '操作人用户 ID（通过/驳回/转办=办理人；撤回=发起人）',
  `action` tinyint NOT NULL COMMENT '动作：1 通过 / 2 驳回 / 3 撤回 / 4 转办（spec §3.1）',
  `comment` varchar(500) DEFAULT NULL COMMENT '审批意见：驳回必填 1–500 字，其余可选（spec §4.3）',
  `transfer_to_user_id` bigint NOT NULL DEFAULT '0' COMMENT '转办目标用户 ID；仅转办记录使用',
  PRIMARY KEY (`id`),
  KEY `idx_bpm_record_tenant_instance` (`tenant_id`,`instance_id`),
  KEY `idx_bpm_record_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `bpm_approval_record`;
DROP TABLE IF EXISTS `bpm_task`;
DROP TABLE IF EXISTS `bpm_process_instance`;
DROP TABLE IF EXISTS `bpm_process_definition`;
