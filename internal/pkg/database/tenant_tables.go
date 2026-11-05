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
	// bpm 内核（issue #44）：定义/实例/任务/审批记录全量租户隔离（spec §4.6：
	// 跨租户业务 ID 一律 404，不泄露存在性）。
	"bpm_process_definition",
	"bpm_process_instance",
	"bpm_task",
	"bpm_approval_record",
}
