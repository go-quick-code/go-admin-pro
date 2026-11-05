// pool_config.go 承载租户级公海配置（issue #14）：取值、默认值与边界校验。
// 配置项：回收天数（自动回收的「超期」口径）与认领上限（认领校验）。
// 规则按租户配置（docs/crm-mvp-spec.md §4.2），行缺失（新建租户尚未配置）
// 时按默认值执行，读取路径绝不回退全局硬编码。
package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// PermPoolConfigUpdate 是公海配置编辑的权限码：与菜单种子（internal/seed
// 的公海页按钮 4050）保持一致。查询侧复用 crm:customer:pool:view——
// 能看公海的人即可读配置，写权限单独控制。
const PermPoolConfigUpdate = "crm:pool-config:update"

// DefaultRecycleDays 是回收天数的默认值（docs/crm-mvp-spec.md §4.2：缺省 30）。
const DefaultRecycleDays = 30

// 配置取值边界：回收天数 1–3650（约 10 年），认领上限 1–100000。
// 上限取「业务上永远不会真用到的宽松界」，防手滑输入灾难值（如 0 天全量回收）。
const (
	MinRecycleDays = 1
	MaxRecycleDays = 3650
	MinClaimLimit  = 1
	MaxClaimLimit  = 100000
)

// PoolConfigValues 是租户级公海配置的取值（不含实体底座列）：
// 配置读写 API 与自动回收 job 共用同一形状。
type PoolConfigValues struct {
	RecycleDays int `json:"recycleDays"`
	ClaimLimit  int `json:"claimLimit"`
}

// DefaultPoolConfigValues 返回默认配置（docs/crm-mvp-spec.md §4.2：
// 回收天数缺省 30；认领上限缺省 50，与 DefaultClaimLimit 同源）。
func DefaultPoolConfigValues() PoolConfigValues {
	return PoolConfigValues{RecycleDays: DefaultRecycleDays, ClaimLimit: DefaultClaimLimit}
}

// ValidatePoolConfig 校验配置取值边界，错误文案中文且指明字段（用户可见）。
func ValidatePoolConfig(recycleDays, claimLimit int) error {
	if recycleDays < MinRecycleDays || recycleDays > MaxRecycleDays {
		return xerr.BadRequest(fmt.Sprintf("回收天数须在 %d–%d 之间", MinRecycleDays, MaxRecycleDays))
	}
	if claimLimit < MinClaimLimit || claimLimit > MaxClaimLimit {
		return xerr.BadRequest(fmt.Sprintf("认领上限须在 %d–%d 之间", MinClaimLimit, MaxClaimLimit))
	}
	return nil
}

// GetPoolConfig 读取当前租户的公海配置：行缺失（新建租户尚未配置）时
// 返回默认值，绝不回退到「读不到就报错」——配置读取是公海页与回收 job
// 的常态前置，缺省语义由规格锁定（30/50）。
func (s *Service) GetPoolConfig(ctx context.Context) (PoolConfigValues, error) {
	row, err := s.repo.FindPoolConfig(ctx)
	if err != nil {
		return PoolConfigValues{}, err
	}
	if row == nil {
		return DefaultPoolConfigValues(), nil
	}
	return PoolConfigValues{RecycleDays: row.RecycleDays, ClaimLimit: row.ClaimLimit}, nil
}

// UpdatePoolConfig 保存当前租户的公海配置（upsert）：先校验边界（400），
// 再落库。写路径挂 crm:pool-config:update 权限码（api 层中间件）。
func (s *Service) UpdatePoolConfig(ctx context.Context, req *dto.PoolConfigUpdateReq) (PoolConfigValues, error) {
	if err := ValidatePoolConfig(req.RecycleDays, req.ClaimLimit); err != nil {
		return PoolConfigValues{}, err
	}
	if err := s.repo.SavePoolConfig(ctx, req.RecycleDays, req.ClaimLimit); err != nil {
		return PoolConfigValues{}, err
	}
	return PoolConfigValues{RecycleDays: req.RecycleDays, ClaimLimit: req.ClaimLimit}, nil
}
