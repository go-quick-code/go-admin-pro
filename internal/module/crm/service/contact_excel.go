package service

import (
	"context"
	"io"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
)

// ---------- 联系人 Excel 导入（issue #20）----------
// 联系人没有负责人：导入用客户编号关联所属客户，客户必须存在且对当前用户可见
//（与列表 join 父客户的数据权限口径一致，范围外按行错误拒绝）。

// ContactImportTemplate 生成联系人导入模板（表头 + 填写说明）。
func (s *Service) ContactImportTemplate() ([]byte, error) {
	return excel.Template[dto.ContactImportRow](excel.TemplateOptions{
		Notes: map[string]string{
			"CustomerNo":   "所属客户编号，必填（如 KH202611050001），须存在且在当前用户可见范围内",
			"Name":         "联系人姓名，必填，最多 128 字",
			"Mobile":       "手机号，可留空；填写时须为 11 位大陆手机号（如 13800000001）",
			"Email":        "邮箱，可留空；填写时须为 name@example.com 形式",
			"Gender":       "0 未知 / 1 男 / 2 女；留空按 0 处理",
			"Position":     "职位，最多 64 字",
			"DecisionRole": "决策角色，最多 64 字",
			"Remark":       "备注，最多 500 字",
		},
	})
}

// ImportContacts 导入联系人：解析 → 全量校验（含客户可见性）→ 事务内批量创建。
func (s *Service) ImportContacts(ctx context.Context, r io.Reader, location *time.Location) (int, error) {
	rows, err := s.contactImports.Import(r, excel.GuardOptions[dto.ContactImportRow]{
		Location: location,
		Validate: func(row int, v *dto.ContactImportRow) error {
			v.Row = row
			return nil
		},
	})
	if err != nil {
		return 0, err
	}

	customerNos := make([]string, 0, len(rows))
	for _, row := range rows {
		customerNos = append(customerNos, row.CustomerNo)
	}
	customers, err := s.repo.FindCustomersByNos(ctx, customerNos)
	if err != nil {
		return 0, err
	}
	byNo := make(map[string]model.Customer, len(customers))
	for _, c := range customers {
		byNo[c.CustomerNo] = c
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return 0, err
	}

	errs := validateContactImportRows(rows, byNo, scope.Permits)
	if len(errs) > 0 {
		return 0, newImportValidationError(errs)
	}

	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		for i := range rows {
			contact := &model.Contact{
				CustomerID:   byNo[rows[i].CustomerNo].ID,
				Name:         rows[i].Name,
				Position:     rows[i].Position,
				Mobile:       rows[i].Mobile,
				Email:        rows[i].Email,
				DecisionRole: rows[i].DecisionRole,
				Remark:       rows[i].Remark,
			}
			if rows[i].Gender != nil {
				contact.Gender = *rows[i].Gender
			}
			if err := tx.CreateContact(ctx, contact); err != nil {
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

// validateContactImportRows 校验联系人导入行：客户存在且可见、手机/邮箱格式、
// 性别枚举与文本长度。permits 是当前用户的数据范围谓词（作用于父客户 owner_*）。
func validateContactImportRows(rows []dto.ContactImportRow, byNo map[string]model.Customer, permits func(ownerDeptID, ownerUserID int64) bool) []excel.CellError {
	var errs []excel.CellError
	add := func(err *excel.CellError) {
		if err != nil {
			errs = append(errs, *err)
		}
	}
	for _, row := range rows {
		customer, ok := byNo[row.CustomerNo]
		switch {
		case !ok:
			add(&excel.CellError{
				Row:    row.Row,
				Column: "客户编号",
				Msg:    "客户不存在：" + row.CustomerNo,
			})
		case !permits(customer.OwnerDeptID, customer.OwnerUserID):
			add(&excel.CellError{
				Row:    row.Row,
				Column: "客户编号",
				Msg:    "客户不存在或不在当前用户可见范围：" + row.CustomerNo,
			})
		}

		add(importTextError(row.Row, "姓名", row.Name, 128))
		add(importTextError(row.Row, "职位", row.Position, 64))
		add(importTextError(row.Row, "手机", row.Mobile, 32))
		add(importTextError(row.Row, "邮箱", row.Email, 128))
		add(importTextError(row.Row, "决策角色", row.DecisionRole, 64))
		add(importTextError(row.Row, "备注", row.Remark, 500))
		if err := ValidateContactMobile(row.Mobile); err != nil {
			add(&excel.CellError{Row: row.Row, Column: "手机", Msg: err.Error()})
		}
		if err := ValidateContactEmail(row.Email); err != nil {
			add(&excel.CellError{Row: row.Row, Column: "邮箱", Msg: err.Error()})
		}
		if row.Gender != nil {
			if err := ValidateContactGender(*row.Gender); err != nil {
				add(&excel.CellError{Row: row.Row, Column: "性别", Msg: err.Error()})
			}
		}
	}
	return errs
}
