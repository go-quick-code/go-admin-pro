-- +goose Up
-- 考勤基础（issue #60，docs/oa-mvp-spec.md §3.9/§4.10）：班次、考勤组、组员、
-- 打卡、结果五张表。结果表（oa_attendance_result）本工单只建 schema，
-- 写入与派生由 #61（考勤结果与校正）承接。
-- 语义约定（spec §4.10）：
--   - 班次一天一个时段，end_time > start_time，不支持跨天；时间为 time-of-day
--     （HH:MM，24 小时制字符串，定宽可字典序比较），不含日期成分。
--   - 考勤组绑定一个班次；组员按用户物化，一个用户只属于一个组——唯一键
--     (tenant_id, user_id)（软删行同样占用键，重进组走 upsert 复活改绑）。
--   - 打卡同一天允许多次（不覆盖），结果取最早上班与最晚下班；打卡日期按
--     app.timezone 业务时区取当天。
--   - 请假半天枚举与 #50 约定对齐（HalfAM/HalfPM），leave_days=0.5 记半天。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater + tenant_id（ADR 0002）。
-- 全部纳入租户插件（internal/pkg/database/tenant_tables.go），跨租户 ID 一律 404。

-- 班次（管理端 CRUD+启停，权限码 oa:attendance:manage）。
CREATE TABLE `oa_attendance_shift` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(64) NOT NULL COMMENT '班次名称（1–64 字，租户内不强制唯一）',
  `start_time` varchar(5) NOT NULL COMMENT '上班时间，time-of-day HH:MM（24 小时制；不支持跨天）',
  `end_time` varchar(5) NOT NULL COMMENT '下班时间，HH:MM；必须晚于上班时间（同日内）',
  `grace_minutes` int NOT NULL DEFAULT '0' COMMENT '迟到/早退宽限分钟数（0–1440，spec §4.10）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用',
  `sort` int NOT NULL DEFAULT '0' COMMENT '展示排序（0–9999，小者在前）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_att_shift_tenant` (`tenant_id`),
  KEY `idx_oa_att_shift_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 考勤组（绑定一个班次；启停影响组员能否打卡：组停用打卡 400）。
CREATE TABLE `oa_attendance_group` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(64) NOT NULL COMMENT '考勤组名称（1–64 字）',
  `shift_id` bigint NOT NULL COMMENT '绑定班次 ID（oa_attendance_shift.id，删除班次前须先解绑）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（停用后组员打卡 400）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_att_group_tenant` (`tenant_id`),
  KEY `idx_oa_att_group_shift` (`shift_id`),
  KEY `idx_oa_att_group_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 组员（按用户物化；一个用户只属于一个组：唯一键 (tenant_id, user_id)。
-- 软删行同样占用唯一键——移出后再次入组经 upsert 复活改绑，不产生第二行）。
CREATE TABLE `oa_attendance_group_user` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `group_id` bigint NOT NULL COMMENT '考勤组 ID',
  `user_id` bigint NOT NULL COMMENT '组员用户 ID（system_user.id）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_att_group_user` (`tenant_id`,`user_id`),
  KEY `idx_oa_att_group_user_group` (`group_id`),
  KEY `idx_oa_att_group_user_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 打卡记录（Web 来源；同一天允许多次，全部保留、不覆盖；spec §4.10）。
CREATE TABLE `oa_attendance_punch` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '打卡用户 ID',
  `punch_date` date NOT NULL COMMENT '打卡日期（按 app.timezone 业务时区取当天，spec §4.10）',
  `punch_time` datetime(3) NOT NULL COMMENT '打卡时刻',
  `type` tinyint NOT NULL COMMENT '类型：0 上班 / 1 下班',
  `source` tinyint NOT NULL DEFAULT '0' COMMENT '来源：0 Web（v1 只有 Web 打卡）',
  `ip` varchar(64) NOT NULL DEFAULT '' COMMENT '打卡来源 IP（c.ClientIP，超长截断）',
  `user_agent` varchar(512) DEFAULT NULL COMMENT 'User-Agent（超长截断到 512）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_att_punch_user_date` (`tenant_id`,`user_id`,`punch_date`),
  KEY `idx_oa_att_punch_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 考勤结果（issue #60 只建 schema；派生/校正写入由 #61 承接，spec §3.9/§4.10）。
-- 唯一键 (tenant_id, user_id, work_date)：一人一天一条结果。
-- 派生优先级（#61 实现）：非工作日 → 休息；leave_days >= 1 → 请假；缺上班/下班卡 →
-- 缺卡；迟到且早退 → 迟到且早退；仅迟到 → 迟到；仅早退 → 早退；否则正常。
-- override_* 为管理员校正位：校正结果优先于派生结果，不覆盖原始打卡。
CREATE TABLE `oa_attendance_result` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '用户 ID',
  `work_date` date NOT NULL COMMENT '考勤日期（YYYY-MM-DD）',
  `shift_id` bigint DEFAULT NULL COMMENT '当日适用班次 ID（无组/组未绑班次为 NULL）',
  `in_time` datetime(3) DEFAULT NULL COMMENT '最早上班打卡时刻（无上班卡为 NULL）',
  `out_time` datetime(3) DEFAULT NULL COMMENT '最晚下班打卡时刻（无下班卡为 NULL）',
  `late_minutes` int NOT NULL DEFAULT '0' COMMENT '迟到分钟数（超过宽限的部分）',
  `early_minutes` int NOT NULL DEFAULT '0' COMMENT '早退分钟数（超过宽限的部分）',
  `missing_in` tinyint NOT NULL DEFAULT '0' COMMENT '缺上班卡：0 否 / 1 是',
  `missing_out` tinyint NOT NULL DEFAULT '0' COMMENT '缺下班卡：0 否 / 1 是',
  `leave_days` decimal(3,1) NOT NULL DEFAULT '0.0' COMMENT '请假天数（0.5=半天，与 #50 HalfAM/HalfPM 槽位对齐；>=1 整天请假）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '派生结果状态（取值语义由 #61 定义：正常/迟到/早退/缺卡/请假/休息…）',
  `override_status` tinyint DEFAULT NULL COMMENT '管理员校正状态（NULL=未校正；校正优先于派生）',
  `override_remark` varchar(200) DEFAULT NULL COMMENT '校正备注（1–200 字）',
  `corrected_by` bigint DEFAULT NULL COMMENT '校正人用户 ID',
  `corrected_at` datetime(3) DEFAULT NULL COMMENT '校正时间',
  `computed_at` datetime(3) DEFAULT NULL COMMENT '最近一次派生计算时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_att_result` (`tenant_id`,`user_id`,`work_date`),
  KEY `idx_oa_att_result_work_date` (`work_date`),
  KEY `idx_oa_att_result_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_attendance_result`;
DROP TABLE IF EXISTS `oa_attendance_punch`;
DROP TABLE IF EXISTS `oa_attendance_group_user`;
DROP TABLE IF EXISTS `oa_attendance_group`;
DROP TABLE IF EXISTS `oa_attendance_shift`;
