// pool_limit.go 承载公海认领上限的纯判断逻辑（issue #13/#14）。
package service

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// DefaultClaimLimit 是认领上限的默认值（docs/crm-mvp-spec.md §4.2：缺省 50）。
// 统计口径：owner_user_id = 当前用户 的未删除客户总数，不区分是否成交。
//
// #14 落地后上限按租户级公海配置（crm_pool_config.claim_limit）生效，
// 本常量降级为配置行缺失（新建租户尚未配置）时的兜底默认值，
// 与 DefaultPoolConfigValues().ClaimLimit 同源。
const DefaultClaimLimit = 50

// CheckClaimLimit 判断当前名下客户数是否还可认领（上限值由租户配置传入）：
// 认领后名下总数不得超过上限，故名下数量已 ≥ 上限时直接拒绝。
func CheckClaimLimit(ownedCount int64, claimLimit int) error {
	if int64(claimLimit) <= ownedCount {
		return xerr.BadRequest(fmt.Sprintf("认领失败：名下客户数已达认领上限 %d，请先释放部分客户", claimLimit))
	}
	return nil
}
