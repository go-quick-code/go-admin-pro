package seed

import (
	"log/slog"
	"time"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// defaultBPMDefinition 是一份随安装初始化的默认流程定义。
type defaultBPMDefinition struct {
	Key          string
	Name         string
	BusinessType string
	Nodes        bpmmodel.NodeList
	Remark       string
}

// defaultBPMDefinitions 是安装时写入的默认审批流程（spec §7.3）：
//   - crm_contract_approval v1（issue #47）：单节点，审批人规则为权限码
//     crm:contract:approve（候选池物化，任一持有人可办理，ADR 0008）；
//   - oa_expense_approval v1（issue #55，spec §4.9）：节点 1 发起人所在部门
//     负责人，节点 2 权限码 oa:expense:manage（财务/管理员）；负责人缺失时由
//     bpm 返回 400 拒绝提交（不跳过节点、不自动通过）。
var defaultBPMDefinitions = []defaultBPMDefinition{
	{
		Key:          "crm_contract_approval",
		Name:         "合同审批",
		BusinessType: "crm_contract_approval",
		Nodes: bpmmodel.NodeList{{
			Name:          "合同审批",
			AssigneeType:  bpmmodel.AssigneePermission,
			AssigneeValue: "crm:contract:approve",
		}},
		Remark: "默认合同审批流程（issue #47，spec §4.5/§7.3）",
	},
	{
		Key:          "oa_expense_approval",
		Name:         "报销审批",
		BusinessType: "oa_expense_approval",
		Nodes: bpmmodel.NodeList{
			{Name: "部门负责人审批", AssigneeType: bpmmodel.AssigneeInitiatorDeptLeader},
			{Name: "财务审批", AssigneeType: bpmmodel.AssigneePermission, AssigneeValue: "oa:expense:manage"},
		},
		Remark: "默认报销审批流程（issue #55，spec §4.9/§7.3）",
	},
}

// seedBPMDefinitions 写入默认审批流程定义（spec §7.3，issue #47/#55）。
//
// 幂等：同 tenant + key 已有任意版本（含软删，版本号唯一键覆盖软删行）即跳过，
// 不覆盖租户自行维护的流程配置。默认版本直接以「已发布」落库——种子是安装
// 初始化动作，发布前的 BusinessHandler 注册 guard 由应用装配保证（app.New
// 在 seed 之后注册 oaSvc/ExpenseApprovalHandler）。
func seedBPMDefinitions(db *gorm.DB) error {
	now := time.Now()
	for _, item := range defaultBPMDefinitions {
		var count int64
		if err := tenant.Skip(db).Unscoped().Model(&bpmmodel.ProcessDefinition{}).
			Where("tenant_id = ? AND `key` = ?", 1, item.Key).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		def := &bpmmodel.ProcessDefinition{
			TenantID:     1,
			Key:          item.Key,
			Version:      1,
			Name:         item.Name,
			BusinessType: item.BusinessType,
			Nodes:        item.Nodes,
			Status:       bpmmodel.DefinitionStatusPublished,
			PublishedAt:  &now,
			Remark:       item.Remark,
		}
		def.Creator = "0"
		def.Updater = "0"
		if err := tenant.Skip(db).Create(def).Error; err != nil {
			return err
		}
		slog.Info("seed bpm definition created", "key", item.Key, "version", def.Version)
	}
	return nil
}
