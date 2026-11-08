package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// party_excel.go 是仓库/客户/供应商的 Excel 导出（issue #89，spec §4.11）：
// 只导出、不导入（spec §1 差异记清单）。筛选与列表一致（含数据范围谓词），
// 金额经 TextMarshaler 输出十进制字符串，状态输出中文展示，日期按业务时区
//（NativeDates，ADR 0006）。状态名复用 statusNames（product_excel.go）——
// 三新实体与分类/单位/产品同口径 0 启用 / 1 停用（迁移 00029）。

// ExportWarehouses 导出仓库：筛选（含数据范围）与列表一致。
func (s *Service) ExportWarehouses(ctx context.Context, req *dto.WarehousePageReq) ([]byte, error) {
	filter, err := s.warehouseFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllWarehouses(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.WarehouseExportRow, 0, len(list))
	for _, w := range list {
		rows = append(rows, dto.WarehouseExportRow{
			Name:       w.Name,
			Address:    w.Address,
			Principal:  w.Principal,
			Sort:       w.Sort,
			Status:     statusNames[w.Status],
			CreateTime: w.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportCustomers 导出 ERP 客户：筛选（含数据范围）与列表一致。
func (s *Service) ExportCustomers(ctx context.Context, req *dto.CustomerPageReq) ([]byte, error) {
	filter, err := s.customerFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllCustomers(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.CustomerExportRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, dto.CustomerExportRow{
			Name:        c.Name,
			Contact:     c.Contact,
			Mobile:      c.Mobile,
			Telephone:   c.Telephone,
			Email:       c.Email,
			Fax:         c.Fax,
			TaxNo:       deref(c.TaxNo),
			TaxPercent:  c.TaxPercent,
			BankName:    deref(c.BankName),
			BankAccount: deref(c.BankAccount),
			BankAddress: deref(c.BankAddress),
			Status:      statusNames[c.Status],
			Sort:        c.Sort,
			Remark:      c.Remark,
			CreateTime:  c.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// deref 字符串指针解引用（nil → 空串，导出单元格留白）。
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ExportSuppliers 导出供应商：筛选（含数据范围）与列表一致。
func (s *Service) ExportSuppliers(ctx context.Context, req *dto.SupplierPageReq) ([]byte, error) {
	filter, err := s.supplierFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllSuppliers(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows := make([]dto.SupplierExportRow, 0, len(list))
	for _, sup := range list {
		rows = append(rows, dto.SupplierExportRow{
			Name:        sup.Name,
			Contact:     sup.Contact,
			Mobile:      sup.Mobile,
			Telephone:   sup.Telephone,
			Email:       sup.Email,
			Fax:         sup.Fax,
			TaxNo:       deref(sup.TaxNo),
			TaxPercent:  sup.TaxPercent,
			BankName:    deref(sup.BankName),
			BankAccount: deref(sup.BankAccount),
			BankAddress: deref(sup.BankAddress),
			Status:      statusNames[sup.Status],
			Sort:        sup.Sort,
			Remark:      sup.Remark,
			CreateTime:  sup.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}
