package service

import (
	"context"
	"io"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// ---------- 客户 Excel 导入导出（issue #20，docs/crm-mvp-spec.md §7）----------
// 导入列与 CustomerCreateReq 对齐（编号由服务端发放，负责人缺省当前用户）；
// 先全量校验、有错整批拒绝且不写任何数据；校验通过后在事务内批量创建
//（编号、客户行与负责人历史同事务）。导出复用列表筛选与数据范围谓词。

// customerStatusNames 是客户状态的中文展示（导出用，与前端选项一致）。
var customerStatusNames = map[int8]string{
	model.CustomerStatusPotential: "潜在",
	model.CustomerStatusActive:    "活跃",
	model.CustomerStatusLost:      "流失",
}

// CustomerImportTemplate 生成客户导入模板（表头 + 填写说明）。
func (s *Service) CustomerImportTemplate() ([]byte, error) {
	return excel.Template[dto.CustomerImportRow](excel.TemplateOptions{
		Notes: map[string]string{
			"Name":     "客户全称，必填，最多 128 字",
			"Status":   "1 潜在 / 2 活跃 / 3 流失；留空按 1 潜在处理",
			"Phone":    "联系电话，最多 32 字",
			"Website":  "网址，最多 128 字",
			"Address":  "地址，最多 255 字",
			"Remark":   "备注，最多 500 字",
			"Industry": "行业，最多 64 字",
			"Level":    "客户级别，最多 64 字",
			"Source":   "客户来源，最多 64 字",
		},
	})
}

// ImportCustomers 导入客户：解析 → 全量业务校验 → 事务内批量创建。
// 解析/限额错误返回 guard 错误；任一行不合法返回 *excel.ValidationError
// （含行号、列名与原因），调用方整批拒绝，不写任何数据。
func (s *Service) ImportCustomers(ctx context.Context, r io.Reader, location *time.Location) (int, error) {
	rows, err := s.customerImports.Import(r, excel.GuardOptions[dto.CustomerImportRow]{
		Location: location,
		Validate: func(row int, v *dto.CustomerImportRow) error {
			v.Row = row
			return nil
		},
	})
	if err != nil {
		return 0, err
	}

	errs := validateCustomerImportRows(rows)
	if len(errs) > 0 {
		return 0, newImportValidationError(errs)
	}

	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return 0, err
	}

	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		dateKey := DateKey(s.businessNow())
		for i := range rows {
			status := model.CustomerStatusPotential
			if rows[i].Status != nil {
				status = *rows[i].Status
			}
			c := &model.Customer{
				Name:        rows[i].Name,
				Industry:    rows[i].Industry,
				Level:       rows[i].Level,
				Source:      rows[i].Source,
				Phone:       rows[i].Phone,
				Website:     rows[i].Website,
				Address:     rows[i].Address,
				Status:      status,
				OwnerUserID: ownerID,
				OwnerDeptID: deptID,
				Remark:      rows[i].Remark,
			}
			seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeCustomer, dateKey)
			if err != nil {
				return err
			}
			c.CustomerNo = FormatCustomerNo(dateKey, seq)
			if err := tx.CreateCustomer(ctx, c); err != nil {
				return err
			}
			if err := tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
				TenantID:       currentTenantID(ctx),
				CustomerID:     c.ID,
				Action:         model.OwnerHistoryCreate,
				ToOwnerUserID:  ownerID,
				ToOwnerDeptID:  deptID,
				OperatorUserID: CurrentUserID(ctx),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// validateCustomerImportRows 校验客户导入行（名称必填由 excel 标签保证），
// 返回全部行级错误（不是遇到第一个就停）。
func validateCustomerImportRows(rows []dto.CustomerImportRow) []excel.CellError {
	var errs []excel.CellError
	for _, row := range rows {
		add := func(err *excel.CellError) {
			if err != nil {
				errs = append(errs, *err)
			}
		}
		add(importTextError(row.Row, "客户名称", row.Name, 128))
		add(importTextError(row.Row, "行业", row.Industry, 64))
		add(importTextError(row.Row, "级别", row.Level, 64))
		add(importTextError(row.Row, "来源", row.Source, 64))
		add(importTextError(row.Row, "联系电话", row.Phone, 32))
		add(importTextError(row.Row, "网址", row.Website, 128))
		add(importTextError(row.Row, "地址", row.Address, 255))
		add(importTextError(row.Row, "备注", row.Remark, 500))
		if row.Status != nil && !model.ValidCustomerStatus(*row.Status) {
			add(&excel.CellError{
				Row:    row.Row,
				Column: "状态",
				Msg:    "客户状态只支持 1 潜在 / 2 活跃 / 3 流失",
			})
		}
	}
	return errs
}

// ExportCustomers 导出客户：筛选与数据范围谓词与列表完全一致（看不见的不会导出），
// 日期列启用 Excel 原生日期（#32），金额无涉及。
func (s *Service) ExportCustomers(ctx context.Context, req *dto.CustomerPageReq) ([]byte, error) {
	filter, err := s.customerFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllCustomers(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.fillOwnerNames(ctx, list)

	rows := make([]dto.CustomerExportRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, dto.CustomerExportRow{
			CustomerNo:    c.CustomerNo,
			Name:          c.Name,
			Status:        customerStatusNames[c.Status],
			Industry:      c.Industry,
			Level:         c.Level,
			Source:        c.Source,
			Phone:         c.Phone,
			Website:       c.Website,
			Address:       c.Address,
			OwnerUserName: c.OwnerUserName,
			OwnerDeptName: c.OwnerDeptName,
			Remark:        c.Remark,
			CreateTime:    c.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}
