// pool_limit.go 承载公海认领上限的纯判断逻辑（issue #13）。
package service

import (
	"fmt"

	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// DefaultClaimLimit 是认领上限的默认值（docs/crm-mvp-spec.md §4.2：缺省 50）。
// 统计口径：owner_user_id = 当前用户 的未删除客户总数，不区分是否成交。
//
// TODO(#14): 上限将迁移到租户级配置 infra_config 的 crm.pool.claim_limit
// （缺省仍为 50），本常量届时降级为配置缺失时的兜底值。
const DefaultClaimLimit = 50

// CheckClaimLimit 判断当前名下客户数是否还可认领：
// 认领后名下总数不得超过上限，故名下数量已 ≥ 上限时直接拒绝。
func CheckClaimLimit(ownedCount int64) error {
	if ownedCount >= DefaultClaimLimit {
		return xerr.BadRequest(fmt.Sprintf("认领失败：名下客户数已达认领上限 %d，请先释放部分客户", DefaultClaimLimit))
	}
	return nil
}
