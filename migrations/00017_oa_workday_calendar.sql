-- +goose Up
-- 工作日历（issue #50，docs/oa-mvp-spec.md §3.3，请假 #52 与考勤 #61 共用）：
--   oa_workday_calendar  租户级日期三态显式记录。
-- 语义：无显式记录时周一至周五默认工作日、周六周日默认休息日；
-- date 有记录时以记录为准，type=0 可表达周末调休上班、type=2 表达节假日。
-- 核心判定逻辑为纯函数（internal/module/oa/service/workday_rule.go），
-- 本表只是显式记录的持久化；查询编排见 oa/service/workday.go。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater + tenant_id（ADR 0002）。
-- 纳入租户插件（internal/pkg/database/tenant_tables.go），唯一键 (tenant_id, date)。

CREATE TABLE `oa_workday_calendar` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `date` date NOT NULL COMMENT '日历日期（YYYY-MM-DD，租户内唯一，spec §3.3）',
  `type` tinyint NOT NULL COMMENT '日类型：0 工作日（含周末调休上班）/ 1 休息日 / 2 节假日（spec §3.3）',
  `remark` varchar(200) DEFAULT NULL COMMENT '备注（如节假日名称、调休说明）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_workday_tenant_date` (`tenant_id`,`date`),
  KEY `idx_oa_workday_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_workday_calendar`;
