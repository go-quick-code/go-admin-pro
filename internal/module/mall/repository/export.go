package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
)

// export.go 是管理列表 Excel 导出的数据访问原语（docs/mall-mvp-spec.md §1
// 第 8 步「Excel 导出」，PR #184 审查补票；照 pay ListAllOrders 先例）：
// 导出复用各自列表的筛选谓词（memberQuery/spuQuery/orderQuery——导出内容与
// 权限一致的硬约束），固定 id DESC 与分页列表同序，不分页（行数无上限，与
// pay/CRM/ERP ListAll 同口径，由筛选条件控制结果规模）；租户隔离由插件自动
// 追加 tenant_id 条件。另含导出行展示列的批量名解析查询（等级/分类/品牌/
// 会员，照 pay rechargePackageNames 的批量解析口径）。

// ListAllMembers 全量查询会员（导出，复用会员分页谓词，固定 id DESC）。
func (r *Repository) ListAllMembers(ctx context.Context, f MemberFilter) ([]model.MemberUser, error) {
	var list []model.MemberUser
	err := r.memberQuery(ctx, f).Order("id DESC").Find(&list).Error
	return list, err
}

// ListAllSPUs 全量查询 SPU（导出，复用 SPU 分页谓词，固定 id DESC）。
func (r *Repository) ListAllSPUs(ctx context.Context, f SPUFilter) ([]model.ProductSPU, error) {
	var list []model.ProductSPU
	err := r.spuQuery(ctx, f).Order("id DESC").Find(&list).Error
	return list, err
}

// ListAllOrders 全量查询订单（导出，复用订单分页谓词，固定 id DESC）。
func (r *Repository) ListAllOrders(ctx context.Context, f OrderFilter) ([]model.TradeOrder, error) {
	var list []model.TradeOrder
	err := r.orderQuery(ctx, f).Order("id DESC").Find(&list).Error
	return list, err
}

// ListLevelsByIDs 按 ID 批量查等级（导出等级名解析；租户插件隔离，缺失 ID
// 由 service 层回落展示）。
func (r *Repository) ListLevelsByIDs(ctx context.Context, ids []int64) ([]model.MemberLevel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.MemberLevel
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// ListCategoriesByIDs 按 ID 批量查分类（导出分类名解析，同 ListSKUsByIDs 口径）。
func (r *Repository) ListCategoriesByIDs(ctx context.Context, ids []int64) ([]model.ProductCategory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.ProductCategory
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// ListBrandsByIDs 按 ID 批量查品牌（导出品牌名解析，同上）。
func (r *Repository) ListBrandsByIDs(ctx context.Context, ids []int64) ([]model.ProductBrand, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.ProductBrand
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// ListMembersByIDs 按 ID 批量查会员（订单导出的会员列名解析，同上）。
func (r *Repository) ListMembersByIDs(ctx context.Context, ids []int64) ([]model.MemberUser, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.MemberUser
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}
