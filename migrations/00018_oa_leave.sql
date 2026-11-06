-- +goose Up
-- 请假类型与额度（issue #51，docs/oa-mvp-spec.md §3.5/§4.8）：
--   oa_leave_type     请假类型（租户内编码/名称唯一，年度额度与占用额度标记）。
--   oa_leave_balance  请假额度，(用户, 类型, 自然年) 一行；可用 = quota + adjustment - used。
--   oa_leave_request  请假申请。本工单只建 schema（写入由 #52 承接）：列按
--     spec §3.5/§4.8 预齐——起止日期与半天（0 上午/1 下午，与 #50 Slot 语义
--     HalfAM/HalfPM 对齐）、事由、状态机、bpm 实例关联、驳回原因、工作日时长
--     快照。附件不建列，走 infra_file（biz_type='oa_leave'、biz_id=申请 ID）。
-- 半天/时长判定为纯函数（#50 workday_rule.go 的 ResolveWorkdaySlots，#52 复用）；
-- 额度算术为纯函数（internal/module/oa/service/leave_rule.go）。
-- 实体表统一底座：id/created_at/updated_at/deleted_at/creator/updater + tenant_id
-- （ADR 0002）。三表全部纳入租户插件（internal/pkg/database/tenant_tables.go）。

CREATE TABLE `oa_leave_type` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `name` varchar(50) NOT NULL COMMENT '类型名称（租户内唯一）',
  `code` varchar(50) NOT NULL COMMENT '类型编码（租户内唯一，小写字母/数字/连字符；创建后不可变）',
  `balance_managed` tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否占用额度：0 不占 / 1 占（占用额度：可用 = quota + adjustment - used，spec §4.8）',
  `annual_quota` decimal(5,1) NOT NULL DEFAULT '0.0' COMMENT '年度额度（天，一位小数支持半天；balance_managed=1 时额度初始化的默认值）',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 启用 / 1 停用（与系统通用状态语义一致；被申请引用时只能停用不能删除，spec §4.14）',
  `sort` int NOT NULL DEFAULT '0' COMMENT '显示顺序（小者在前）',
  `remark` varchar(200) DEFAULT NULL COMMENT '备注',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_leave_type_tenant_code` (`tenant_id`,`code`),
  UNIQUE KEY `uk_oa_leave_type_tenant_name` (`tenant_id`,`name`),
  KEY `idx_oa_leave_type_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_leave_balance` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '员工用户 ID（system_user，租户内）',
  `leave_type_id` bigint NOT NULL COMMENT '请假类型 ID（oa_leave_type，balance_managed=1）',
  `year` int NOT NULL COMMENT '自然年（额度年度，如 2026）',
  `quota` decimal(5,1) NOT NULL DEFAULT '0.0' COMMENT '初始额度（天，初始化时取类型年度额度快照，之后不随类型定义变化）',
  `adjustment` decimal(5,1) NOT NULL DEFAULT '0.0' COMMENT '管理员手工调整累计（天，可负；可用 = quota + adjustment - used，spec §4.8）',
  `used` decimal(5,1) NOT NULL DEFAULT '0.0' COMMENT '已使用（天，请假通过扣减 / 通过后取消返还，#52 维护）',
  `adjust_reason` varchar(200) DEFAULT NULL COMMENT '最近一次手工调整的原因（spec §4.8：调整留痕在 updater 与备注字段，操作人=updater、时间=updated_at）',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_oa_leave_balance_row` (`tenant_id`,`user_id`,`leave_type_id`,`year`),
  KEY `idx_oa_leave_balance_type_year` (`tenant_id`,`leave_type_id`,`year`),
  KEY `idx_oa_leave_balance_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `oa_leave_request` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `creator` varchar(64) NOT NULL DEFAULT '0',
  `updater` varchar(64) NOT NULL DEFAULT '0',
  `tenant_id` bigint NOT NULL DEFAULT '1',
  `user_id` bigint NOT NULL COMMENT '申请人（system_user，租户内）',
  `leave_type_id` bigint NOT NULL COMMENT '请假类型 ID（oa_leave_type，启用中的类型）',
  `start_date` date NOT NULL COMMENT '开始日期（YYYY-MM-DD）',
  `start_half` tinyint NOT NULL COMMENT '开始半天：0 上午 / 1 下午（与 #50 Slot 语义 HalfAM/HalfPM 对齐，spec §4.8）',
  `end_date` date NOT NULL COMMENT '结束日期（YYYY-MM-DD，可为休息日；跨自然年拒绝提交）',
  `end_half` tinyint NOT NULL COMMENT '结束半天：0 上午 / 1 下午；同日 下午→上午 非法',
  `duration_days` decimal(5,1) NOT NULL DEFAULT '0.0' COMMENT '时长快照（工作日天数，服务端按工作日历槽位计算，spec §4.8）',
  `reason` varchar(500) NOT NULL COMMENT '请假事由',
  `status` tinyint NOT NULL DEFAULT '0' COMMENT '状态：0 草稿 / 1 待审 / 2 通过 / 3 驳回 / 4 撤回 / 5 已取消（spec §4.8 状态机）',
  `reject_reason` varchar(200) DEFAULT NULL COMMENT '驳回原因（bpm 回调写入）',
  `approval_instance_id` bigint DEFAULT NULL COMMENT 'bpm 流程实例 ID（bpm_process_instance.id，提交时写入；草稿为 NULL）',
  PRIMARY KEY (`id`),
  KEY `idx_oa_leave_request_tenant_user_status` (`tenant_id`,`user_id`,`status`),
  KEY `idx_oa_leave_request_tenant_type` (`tenant_id`,`leave_type_id`),
  KEY `idx_oa_leave_request_instance` (`approval_instance_id`),
  KEY `idx_oa_leave_request_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS `oa_leave_request`;
DROP TABLE IF EXISTS `oa_leave_balance`;
DROP TABLE IF EXISTS `oa_leave_type`;
