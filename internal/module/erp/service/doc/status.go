package doc

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CheckStatusTransition 校验审核状态机（spec §4.1）：幂等目标态放行；
// 10→20 审核、20→10 反审核合法；其余非法。返回 (幂等?, 错误)。
func CheckStatusTransition(current, target int8) (bool, error) {
	if !model.ValidDocStatus(target) {
		return false, xerr.BadRequest(fmt.Sprintf("单据审核状态 %d 不合法：期望 10 未审核 / 20 已审核", target))
	}
	if current == target {
		return true, nil
	}
	switch {
	case current == model.DocStatusUnaudited && target == model.DocStatusApproved,
		current == model.DocStatusApproved && target == model.DocStatusUnaudited:
		return false, nil
	}
	return false, xerr.Conflict(fmt.Sprintf("单据当前状态 %d 不允许变更到 %d", current, target))
}
