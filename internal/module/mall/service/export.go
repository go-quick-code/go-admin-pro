package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// export.go 是管理列表 Excel 导出（docs/mall-mvp-spec.md §1 第 8 步「Excel
// 导出」，PR #184 双轴审查补票）：会员/商品 SPU/交易订单三个管理列表导出，
// 照 pay service/export.go 先例——导出复用各自列表的筛选谓词与租户隔离口径
//（repo ListAll*，固定 id DESC 与列表同序；「导出内容与权限一致」spec 硬约束），
// 状态/规格类型/上下架输出中文展示名（与前端页面同一套枚举文案）；导出启用
// NativeDates（日期按业务时区，ADR 0006），金额经 money.Money TextMarshaler
// 输出两位小数十进制字符串。行数无上限：与 pay/CRM/ERP ListAll 同口径，由
// 筛选条件控制结果规模。关联名（等级/分类/品牌/会员）由服务端批量查询映射
//（照 pay rechargePackageNames 批量解析口径），缺失 ID 回落占位不阻断导出。

// 展示名映射（与前端页面同一套文案）。
var (
	// MemberStatusNames 会员状态展示名：0 启用 / 1 停用（数值对齐
	// CommonStatusEnum，与会员管理页同文案）。
	MemberStatusNames = map[int8]string{
		model.StatusEnable:  "启用",
		model.StatusDisable: "停用",
	}
	// SPUStatusNames SPU 上下架展示名：0 下架 / 1 上架（与 SPU 管理页同文案）。
	SPUStatusNames = map[int8]string{
		model.ProductSPUStatusOff: "下架",
		model.ProductSPUStatusOn:  "上架",
	}
	// SPUSpecTypeNames 规格类型展示名：0 单规格 / 1 多规格。
	SPUSpecTypeNames = map[int8]string{
		model.ProductSpecTypeSingle: "单规格",
		model.ProductSpecTypeMulti:  "多规格",
	}
	// TradeOrderStatusNames 订单状态展示名：五态对齐订单管理页 statusLabels
	//（spec §3.3「0 待支付 / 10 已支付 / 20 已完成 / 30 已取消 / 40 已关闭」）。
	TradeOrderStatusNames = map[int8]string{
		model.TradeOrderStatusPending:   "待支付",
		model.TradeOrderStatusPaid:      "已支付",
		model.TradeOrderStatusCompleted: "已完成",
		model.TradeOrderStatusCanceled:  "已取消",
		model.TradeOrderStatusClosed:    "已关闭",
	}
)

// named 按 map 取展示名，未知值回落数字（历史数据兜底，与 pay named/
// 前端 ?? String(status) 一致）。
func named(names map[int8]string, status int8) string {
	if name, ok := names[status]; ok {
		return name
	}
	return fmt.Sprintf("%d", status)
}

// ExportMembers 导出会员列表（复用会员列表筛选与租户隔离口径，
// mall:member:export）：等级列由服务端批量解析等级名（未分配「-」、已删
// 回落「等级 #ID」，与会员管理页 levelName 同口径）。
func (s *Service) ExportMembers(ctx context.Context, req *dto.MemberPageReq) ([]byte, error) {
	if req.Status != nil {
		if err := ValidateMemberStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	list, err := s.repo.ListAllMembers(ctx, mallrepo.MemberFilter{
		Mobile:   trimString(req.Mobile),
		Nickname: trimString(req.Nickname),
		LevelID:  req.LevelID,
		Status:   req.Status,
	})
	if err != nil {
		return nil, err
	}
	levelNames, err := s.memberLevelNames(ctx, list)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.MemberExportRow, 0, len(list))
	for i := range list {
		m := &list[i]
		rows = append(rows, dto.MemberExportRow{
			ID:           m.ID,
			Mobile:       m.Mobile,
			Nickname:     m.Nickname,
			Level:        memberLevelName(m.LevelID, levelNames),
			PointBalance: m.PointBalance,
			Status:       named(MemberStatusNames, m.Status),
			CreateTime:   m.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// memberLevelNames 批量解析会员等级名（缺失 ID 留空，同 pay 导出批量解析口径）。
func (s *Service) memberLevelNames(ctx context.Context, list []model.MemberUser) (map[int64]string, error) {
	ids := make([]int64, 0, len(list))
	for i := range list {
		if list[i].LevelID > 0 {
			ids = append(ids, list[i].LevelID)
		}
	}
	names := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	levels, err := s.repo.ListLevelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range levels {
		names[levels[i].ID] = levels[i].Name
	}
	return names, nil
}

// memberLevelName 等级列展示文本：未分配（level_id=0）「-」、已删等级回落
// 「等级 #ID」（与会员管理页 levelName 同口径）。
func memberLevelName(levelID int64, names map[int64]string) string {
	if levelID == 0 {
		return "-"
	}
	if name, ok := names[levelID]; ok {
		return name
	}
	return fmt.Sprintf("等级 #%d", levelID)
}

// ExportSPUs 导出商品 SPU 列表（复用 SPU 列表筛选与租户隔离口径，
// mall:spu:export）：分类/品牌名由服务端批量解析（SPU 只存 ID 引用，与 SPU
// 管理页 categoryName/brandName 展示列同口径；品牌未关联输出空）。
func (s *Service) ExportSPUs(ctx context.Context, req *dto.SPUPageReq) ([]byte, error) {
	if req.Status != nil {
		if err := ValidateSPUStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	list, err := s.repo.ListAllSPUs(ctx, mallrepo.SPUFilter{
		Name:       trimString(req.Name),
		CategoryID: req.CategoryID,
		BrandID:    req.BrandID,
		Status:     req.Status,
	})
	if err != nil {
		return nil, err
	}
	categoryNames, brandNames, err := s.spuCategoryBrandNames(ctx, list)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.SPUExportRow, 0, len(list))
	for i := range list {
		p := &list[i]
		rows = append(rows, dto.SPUExportRow{
			ID:          p.ID,
			Name:        p.Name,
			Category:    categoryNames[p.CategoryID],
			Brand:       brandNames[p.BrandID],
			SpecType:    named(SPUSpecTypeNames, p.SpecType),
			MarketPrice: p.MarketPrice,
			Status:      named(SPUStatusNames, p.Status),
			CreateTime:  p.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// spuCategoryBrandNames 批量解析 SPU 的分类名与品牌名（brand_id=0 不参与，
// 缺失 ID 留空，同 pay 导出批量解析口径）。
func (s *Service) spuCategoryBrandNames(ctx context.Context, list []model.ProductSPU) (map[int64]string, map[int64]string, error) {
	categoryIDs := make([]int64, 0, len(list))
	brandIDs := make([]int64, 0, len(list))
	for i := range list {
		categoryIDs = append(categoryIDs, list[i].CategoryID)
		if list[i].BrandID > 0 {
			brandIDs = append(brandIDs, list[i].BrandID)
		}
	}
	categories, err := s.repo.ListCategoriesByIDs(ctx, categoryIDs)
	if err != nil {
		return nil, nil, err
	}
	categoryNames := make(map[int64]string, len(categories))
	for i := range categories {
		categoryNames[categories[i].ID] = categories[i].Name
	}
	brands, err := s.repo.ListBrandsByIDs(ctx, brandIDs)
	if err != nil {
		return nil, nil, err
	}
	brandNames := make(map[int64]string, len(brands))
	for i := range brands {
		brandNames[brands[i].ID] = brands[i].Name
	}
	return categoryNames, brandNames, nil
}

// ExportOrders 导出交易订单列表（复用管理端订单筛选与租户隔离口径，
// mall:trade:export）：状态过滤合法性（五态枚举）与时间范围解析与
// AdminPageOrders 同口径（导出内容与权限一致）；会员列由服务端按 user_id
// 批量解析为「昵称(手机号)」（缺失回落 #ID）。
func (s *Service) ExportOrders(ctx context.Context, req *dto.AdminOrderPageReq) ([]byte, error) {
	var status *int8
	if req.Status != nil {
		if !model.IsTradeOrderStatus(*req.Status) {
			return nil, xerr.BadRequest("非法的订单状态过滤值")
		}
		status = req.Status
	}
	begin, end, err := s.parseOrderTimeRange(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllOrders(ctx, mallrepo.OrderFilter{
		Status:          status,
		NoLike:          trimString(req.No),
		UserID:          req.UserID,
		CreateTimeBegin: begin,
		CreateTimeEnd:   end,
	})
	if err != nil {
		return nil, err
	}
	memberNames, err := s.orderMemberNames(ctx, list)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.TradeOrderExportRow, 0, len(list))
	for i := range list {
		o := &list[i]
		rows = append(rows, dto.TradeOrderExportRow{
			No:             o.No,
			Member:         orderMemberName(o.UserID, memberNames),
			Status:         named(TradeOrderStatusNames, o.Status),
			TotalQuantity:  o.TotalQuantity,
			ItemMoney:      o.ItemMoney,
			PayMoney:       o.PayMoney,
			PayChannelCode: o.PayChannelCode,
			CreateTime:     o.CreatedAt,
			PayTime:        o.PayTime,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// orderMemberNames 批量解析订单会员展示要素（昵称/手机号，缺失 ID 留空）。
func (s *Service) orderMemberNames(ctx context.Context, list []model.TradeOrder) (map[int64]model.MemberUser, error) {
	ids := make([]int64, 0, len(list))
	seen := make(map[int64]bool, len(list))
	for i := range list {
		if list[i].UserID > 0 && !seen[list[i].UserID] {
			seen[list[i].UserID] = true
			ids = append(ids, list[i].UserID)
		}
	}
	out := make(map[int64]model.MemberUser, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	members, err := s.repo.ListMembersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range members {
		out[members[i].ID] = members[i]
	}
	return out, nil
}

// orderMemberName 会员列展示文本：「昵称(手机号)」，缺失回落「#ID」
// （会员无删除端点，正常数据恒可解析；回落仅为历史异常数据兜底）。
func orderMemberName(userID int64, members map[int64]model.MemberUser) string {
	if m, ok := members[userID]; ok {
		return fmt.Sprintf("%s(%s)", m.Nickname, m.Mobile)
	}
	return fmt.Sprintf("#%d", userID)
}
