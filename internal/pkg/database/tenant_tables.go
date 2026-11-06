package database

import "gorm.io/gorm/clause"

// tenantClause 生成 tenant_id = ? 条件。
func tenantClause(id int64) clause.Expression {
	return clause.Eq{
		Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"},
		Value:  id,
	}
}

// TenantTables 返回需要租户隔离的表。
var TenantTables = []string{
	"system_user",
	"system_role",
	"system_dept",
	"system_post",
	"system_user_role",
	"system_user_post",
	"system_role_menu",
	"system_role_dept",
	"system_dict_type",
	"system_dict_data",
	"system_login_log",
	"system_operate_log",
	"infra_file",
	"infra_config",
	"infra_job",
	"infra_job_log",
	"crm_customer",
	"crm_contact",
	"crm_follow_up",
	"crm_sequence",
	"crm_customer_owner_history",
	// 产品（issue #11）：分类树与产品均为租户级共享资源。
	"crm_product_category",
	"crm_product",
	"crm_pool_config",
	// 商机（issue #15）：商机 owner-scoped；行项目为关联表，tenant_id 入键隔离。
	"crm_opportunity",
	"crm_opportunity_product",
	// 合同（issue #16）：合同 owner-scoped；行项目为关联表，tenant_id 入键隔离。
	"crm_contract",
	"crm_contract_product",
	// 回款（issue #18）：计划/记录均带 tenant_id；可见性 join 合同套用合同 owner 谓词。
	"crm_receivable_plan",
	"crm_receivable_record",

	// OA（issue #43 起预留登记位，spec §4.6：所有 OA 表纳入租户插件）：
	// oa_* 业务表（请假/报销/考勤/日程/会议室/文档等，docs/oa-mvp-spec.md
	// §3.3/§7.1）由各建表工单落地迁移时把表名追加到这里；登记后须补对应
	// 租户隔离集成用例（跨租户业务 ID 一律 404）。
	// 工作日历（issue #50，迁移 00017）：租户级日期三态显式记录，唯一键
	// (tenant_id, date)；查询经租户插件自动限定，核心判定见 oa/service 纯函数。
	"oa_workday_calendar",
	// 请假（issue #51，迁移 00018）：类型/额度/申请三表；申请表本工单只建
	// schema（写入由 #52 承接）。唯一键：类型 (tenant_id, code)/(tenant_id, name)、
	// 额度 (tenant_id, user_id, leave_type_id, year)；额度算术见 oa/service
	// leave_rule.go 纯函数。
	"oa_leave_type",
	"oa_leave_balance",
	"oa_leave_request",
	// 报销（issue #54，迁移 00019）：报销单 + 明细两表。明细按 claim_id 归属，
	// 表头 total_amount 由明细求和（服务端计算）；两表都经租户插件限定，
	// 跨租户业务 ID 一律 404（spec §4.6）。
	"oa_expense_claim",
	"oa_expense_item",
	// 日程与会议室（issue #56，迁移 00020）：日程/会议室/预约三张表全量
	// 租户隔离（spec §4.6：跨租户业务 ID 一律 404，不泄露存在性）。日程的
	// 可见性谓词见 oa/repository/calendar.go（单条 SQL 谓词 + 纯函数锁定）；
	// 会议室/预约的写入由 #57 承接，迁移已一次建全三表。
	"oa_calendar_event",
	"oa_meeting_room",
	"oa_room_booking",
	// 文档（issue #58，迁移 00021）：目录/目录 ACL/文件三表全量租户隔离
	// （spec §4.6：跨租户业务 ID 一律 404，不泄露存在性）。目录树为租户级
	// 共享资源；读权限按最近一个有显式 ACL 的祖先判定（递归 CTE 谓词见
	// oa/repository/document.go，纯函数镜像见 oa/service/document_rule.go）。
	// 文件表读写（上传/列表/下载/软删）由 #59 承接，归属行按 (tenant_id,
	// folder_id) 限定，跨租户/跨目录一律 404。
	"oa_document_folder",
	"oa_document_folder_acl",
	"oa_document_file",
	// 考勤基础（issue #60，迁移 00022）：班次/考勤组/组员/打卡/结果五张表
	// 全量租户隔离（spec §4.6：跨租户业务 ID 一律 404，不泄露存在性）。
	// 组员唯一键 (tenant_id, user_id)——一个用户只属于一个考勤组；
	// 结果唯一键 (tenant_id, user_id, work_date)——一人一天一条结果（#61 写入）。
	"oa_attendance_shift",
	"oa_attendance_group",
	"oa_attendance_group_user",
	"oa_attendance_punch",
	"oa_attendance_result",
	// bpm 内核（issue #44）：定义/实例/任务/审批记录全量租户隔离（spec §4.6：
	// 跨租户业务 ID 一律 404，不泄露存在性）。
	"bpm_process_definition",
	"bpm_process_instance",
	"bpm_task",
	"bpm_approval_record",

	// 站内通知（issue #45）：通知按接收人物化，严格本人 + 租户隔离
	//（docs/oa-mvp-spec.md §4.13）；写入/查询的租户条件由插件自动追加。
	"notify_message",
	// 公告（issue #48，迁移 00016）：公告/受众/已读三表；受众子查询显式
	// 带主表 tenant_id（见 oa/repository/announcement.go），跨租户业务 ID
	// 一律 404，不泄露存在性（spec §4.6）。
	"oa_announcement",
	"oa_announcement_target",
	"oa_announcement_read",

	// 消息发送日志（issue #72，迁移 00024，spec §3.4/§4.3）：租户级
	// append-only，租户只能看自己的日志，跨租户 ID 一律 404。
	// 渠道/模板是平台级表，刻意不登记（系统级读取显式 tenant.Skip）。
	"message_log",

	// 社交账号绑定（issue #76，迁移 00025，spec §3.6/§4.4）：绑定关系按租户
	// 隔离——不同租户可绑定同一 openid 的不同本地用户，跨租户绑定 ID 一律
	// 404。system_social_client 是平台级表，刻意不登记。
	"system_social_user",

	// API 访问日志（issue #80，迁移 00026，spec §3.7/§4.6，ADR 0014）：租户级
	// append-only，租户只能查/删自己的日志，跨租户业务 ID 一律 404。中间件
	// 采集在请求上下文内（Auth 之后，插件可取到租户），异步落库在后台
	// goroutine 无租户上下文，写入走 tenant.Skip + 行内显式 tenant_id；
	// 保留期清理（cleanAccessLogs）同属系统级动作，显式 tenant.Skip。
	"infra_api_log",
}
