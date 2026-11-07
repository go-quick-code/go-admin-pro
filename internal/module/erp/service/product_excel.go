package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// ---------- 产品/分类/单位 Excel 导出（issue #88，spec §4.11）----------
// 只导出、不导入（spec §1 明确不做，差异记清单）。筛选与列表一致（含数据范围
// 谓词），金额/重量经 TextMarshaler 输出十进制字符串，状态输出中文展示。

// statusNames 是主数据状态的中文展示（导出用；分类/单位/产品同口径 0 启用 / 1 停用）。
var statusNames = map[int8]string{
	model.StatusEnable:  "启用",
	model.StatusDisable: "停用",
}

// ExportProducts 导出产品：筛选（含数据范围）与列表一致，分类/单位名称由服务端解析。
func (s *Service) ExportProducts(ctx context.Context, req *dto.ProductPageReq) ([]byte, error) {
	filter, err := s.productFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllProducts(ctx, filter)
	if err != nil {
		return nil, err
	}
	// 分类/单位名称解析用全量字典（展示补齐，不属数据范围约束；与 crm 导出解析
	// 分类名称同口径）。
	categories, err := s.repo.ListProductCategories(ctx, repository.ProductCategoryFilter{})
	if err != nil {
		return nil, err
	}
	categoryNames := make(map[int64]string, len(categories))
	for _, c := range categories {
		categoryNames[c.ID] = c.Name
	}
	units, err := s.repo.ListAllProductUnits(ctx, repository.ProductUnitFilter{})
	if err != nil {
		return nil, err
	}
	unitNames := make(map[int64]string, len(units))
	for _, u := range units {
		unitNames[u.ID] = u.Name
	}

	rows := make([]dto.ProductExportRow, 0, len(list))
	for _, p := range list {
		row := dto.ProductExportRow{
			Name:          p.Name,
			BarCode:       p.BarCode,
			Category:      categoryNames[p.CategoryId],
			Unit:          unitNames[p.UnitId],
			Standard:      p.Standard,
			ExpiryDay:     p.ExpiryDay,
			PurchasePrice: p.PurchasePrice,
			SalePrice:     p.SalePrice,
			MinPrice:      p.MinPrice,
			Status:        statusNames[p.Status],
			Remark:        p.Remark,
			CreateTime:    p.CreatedAt,
		}
		if p.Weight != nil {
			row.Weight = p.Weight.String()
		}
		rows = append(rows, row)
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportProductCategories 导出分类：全量列表口径（spec §5），含数据范围谓词。
func (s *Service) ExportProductCategories(ctx context.Context) ([]byte, error) {
	filter, err := s.categoryScopeFilter(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListProductCategories(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.ProductCategoryExportRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, dto.ProductCategoryExportRow{
			Name:       c.Name,
			Code:       c.Code,
			Sort:       c.Sort,
			Status:     statusNames[c.Status],
			CreateTime: c.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportProductUnits 导出单位：筛选与列表一致（含数据范围谓词）。
func (s *Service) ExportProductUnits(ctx context.Context, req *dto.ProductUnitPageReq) ([]byte, error) {
	filter, err := s.productUnitFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllProductUnits(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.ProductUnitExportRow, 0, len(list))
	for _, u := range list {
		rows = append(rows, dto.ProductUnitExportRow{
			Name:       u.Name,
			Status:     statusNames[u.Status],
			CreateTime: u.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}
