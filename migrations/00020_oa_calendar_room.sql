-- +goose Up
-- 日程、会议室与预约（issue #56，docs/oa-mvp-spec.md §3.7/§4.11）：
--   oa_calendar_event  日程：创建人、标题、描述、起止时间、全天、可见性
--     （0 私有/1 部门/2 公开）、创建时部门快照 dept_id、来源（0 手动/
--     1 会议室预约）与来源 ID、状态（0 有效/1 取消，软取消）。
--   oa_meeting_room    会议室：名称、位置、容纳人数、状态、排序。
--   oa_room_booking    会议室预约：会议室、预约人、标题、起止时间、状态
--     （0 有效/1 取消）、生成的日程 ID、提醒时间（仅发一次）。
-- 本工单（#56）只落地三张表的 schema 与日程的完整功能；会议室/预约的
-- 写入与冲突校验由 #57 承接（00020 的建表范围一次给全，避免后续 DDL）。
-- 可见性判定为纯函数（internal/module/oa/service/calendar_rule.go），仓储
-- 侧只用单条谓词（status=0 AND (creator_id=本人 OR (visibility=1 AND
-- dept_id IN 本人部门子树) OR visibility=2)）。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater +
-- tenant_id（ADR 0002）。三表全部纳入租户插件
--（internal/pkg/database/tenant_tables.go）。

CREATE TABLE `oa_calendar_event` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `creator_id` bigint NOT NULL COMMENT '创建人（system_user，租户内）',
  `title` varchar(200) NOT NULL COMMENT '日程标题',
  `description` varchar(1000) DEFAULT NULL COMMENT '日程描述',
  `start_time` datetime(3) NOT NULL COMMENT '开始时间（业务时区，库内为 UTC 时刻）',
  `end_time` datetime(3) NOT NULL COMMENT '结束时间（必须晚于开始时间）',
  `all_day` tinyint NOT NULL DEFAULT '0' COMMENT '全天：0 否 / 1 是',
  `visibility` tinyint NOT NULL DEFAULT '0' COMMENT '可见性：0 私有（仅创建人）/ 1 部门（创建人部门快照及子树）/ 2 公开（全租户，创建需 oa:calendar:public）',
  `dept_id` bigint NOT NULL DEFAULT '0' COMMENT '创建人创建时的部门快照（部门可见性判定依据；调部门后历史日程不变）',
  `source` tinyint NOT NULL DEFAULT '0' COMMENT '来源：0 手动 / 1 会议室预约（#57）',
  `source_id` bigint DEFAULT NULL COMMENT '来源业务 ID（source=1 时为 oa_room_booking.id，#57）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 有效 / 1 取消（软取消，取消后不再展示）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_calendar_event_tenant_time` (`tenant_id`,`start_time`,`end_time`),
  KEY `idx_oa_calendar_event_tenant_creator` (`tenant_id`,`creator_id`,`status`),
  KEY `idx_oa_calendar_event_tenant_dept` (`tenant_id`,`dept_id`,`status`),
  KEY `idx_oa_calendar_event_source` (`source`,`source_id`),
  KEY `idx_oa_calendar_event_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_meeting_room` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(100) NOT NULL COMMENT '会议室名称',
  `location` varchar(200) DEFAULT NULL COMMENT '位置',
  `capacity` int NOT NULL DEFAULT '0' COMMENT '容纳人数',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `sort` int NOT NULL DEFAULT '0' COMMENT '显示顺序（小者在前）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_meeting_room_tenant_status` (`tenant_id`,`status`,`sort`),
  KEY `idx_oa_meeting_room_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_room_booking` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `room_id` bigint NOT NULL COMMENT '会议室 ID（oa_meeting_room）',
  `user_id` bigint NOT NULL COMMENT '预约人（system_user，租户内）',
  `title` varchar(200) NOT NULL COMMENT '预约标题',
  `start_time` datetime(3) NOT NULL COMMENT '开始时间',
  `end_time` datetime(3) NOT NULL COMMENT '结束时间（必须晚于开始时间）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 有效 / 1 取消（预约只能取消，不能删除）',
  `calendar_event_id` bigint DEFAULT NULL COMMENT '预约成功同事务生成的日程 ID（oa_calendar_event）',
  `reminded_at` datetime(3) DEFAULT NULL COMMENT '会议提醒发送时间（保证只发一次）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_room_booking_tenant_room_time` (`tenant_id`,`room_id`,`start_time`,`end_time`),
  KEY `idx_oa_room_booking_tenant_user` (`tenant_id`,`user_id`,`status`),
  KEY `idx_oa_room_booking_calendar_event` (`calendar_event_id`),
  KEY `idx_oa_room_booking_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_room_booking`;
DROP TABLE IF EXISTS `oa_meeting_room`;
DROP TABLE IF EXISTS `oa_calendar_event`;
