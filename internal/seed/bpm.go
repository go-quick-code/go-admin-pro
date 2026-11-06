package seed

import (
	"log/slog"
	"time"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// seedBPMDefinitions 写入默认审批流程定义（spec §7.3，issue #47）：
// crm_contract_approval v1 —— 单节点「合同审批」，审批人规则为权限码
// crm:contract:approve（候选池物化，任一持有人可办理，ADR 0008）。
//
// 幂等：同 tenant + key 已有任意版本（含软删，版本号唯一键覆盖软删行）即跳过，
// 不覆盖租户自行维护的流程配置。默认版本直接以「已发布」落库——种子是安装
// 初始化动作，发布前的 BusinessHandler 注册 guard 由应用装配保证。
func seedBPMDefinitions(db *gorm.DB) error {
	const (
		key          = "crm_contract_approval"
		businessType = "crm_contract_approval"
	)
	var count int64
	if err := tenant.Skip(db).Unscoped().Model(&bpmmodel.ProcessDefinition{}).
		Where("tenant_id = ? AND `key` = ?", 1, key).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	now := time.Now()
	def := &bpmmodel.ProcessDefinition{
		TenantID:     1,
		Key:          key,
		Version:      1,
		Name:         "合同审批",
		BusinessType: businessType,
		Nodes: bpmmodel.NodeList{{
			Name:          "合同审批",
			AssigneeType:  bpmmodel.AssigneePermission,
			AssigneeValue: "crm:contract:approve",
		}},
		Status:      bpmmodel.DefinitionStatusPublished,
		PublishedAt: &now,
		Remark:      "默认合同审批流程（issue #47，spec §4.5/§7.3）",
	}
	def.Creator = "0"
	def.Updater = "0"
	if err := tenant.Skip(db).Create(def).Error; err != nil {
		return err
	}
	slog.Info("seed bpm definition created", "key", key, "version", def.Version)
	return nil
}
