package service

import (
	"context"
	"io"
	"strconv"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 产品 Excel 导入导出（issue #20）----------
// 产品是租户级共享资源（无 owner 与数据范围）：导入用分类名称关联分类（可空），
// 编码租户内唯一（批内与库内都预检，落库撞唯一键兜底）；导出复用列表筛选。

// productStatusNames 是产品状态的中文展示（导出用）。
var productStatusNames = map[int8]string{
	model.ProductStatusOffShelf: "下架",
	model.ProductStatusOnSale:   "上架",
}

// ProductImportTemplate 生成产品导入模板（表头 + 填写说明）。
func (s *Service) ProductImportTemplate() ([]byte, error) {
	return excel.Template[dto.ProductImportRow](excel.TemplateOptions{
		Notes: map[string]string{
			"Name":         "产品名称，必填，最多 128 字",
			"Code":         "产品编码，必填，租户内唯一，最多 64 字",
			"CategoryName": "产品分类名称，可留空；填写时须为已存在的分类名称",
			"Unit":         "单位，最多 32 字",
			"Price":        "价格，必填，十进制金额字符串（如 1234.56），最多两位小数",
			"Status":       "0 下架 / 1 上架；留空按 1 上架处理",
			"Description":  "描述，最多 500 字",
		},
	})
}

// ImportProducts 导入产品：解析 → 全量校验（分类名称、编码唯一性）→ 事务内批量创建。
func (s *Service) ImportProducts(ctx context.Context, r io.Reader, location *time.Location) (int, error) {
	rows, err := s.productImports.Import(r, excel.GuardOptions[dto.ProductImportRow]{
		Location: location,
		Validate: func(row int, v *dto.ProductImportRow) error {
			v.Row = row
			return nil
		},
	})
	if err != nil {
		return 0, err
	}

	categories, err := s.repo.ListProductCategories(ctx)
	if err != nil {
		return 0, err
	}
	categoryIDs := make(map[string][]int64, len(categories))
	for _, c := range categories {
		categoryIDs[c.Name] = append(categoryIDs[c.Name], c.ID)
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		codes = append(codes, row.Code)
	}
	existing, err := s.repo.FindProductsByCodes(ctx, codes)
	if err != nil {
		return 0, err
	}
	usedCodes := make(map[string]bool, len(existing))
	for _, p := range existing {
		usedCodes[p.Code] = true
	}

	resolved := make([]int64, len(rows))
	errs := validateProductImportRows(rows, categoryIDs, usedCodes, resolved)
	if len(errs) > 0 {
		return 0, newImportValidationError(errs)
	}

	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		for i := range rows {
			status := int8(1)
			if rows[i].Status != nil {
				status = *rows[i].Status
			}
			row := &model.Product{
				CategoryId:  resolved[i],
				Name:        rows[i].Name,
				Code:        rows[i].Code,
				Unit:        rows[i].Unit,
				Price:       rows[i].Price,
				Status:      status,
				Description: rows[i].Description,
			}
			if err := tx.CreateProduct(ctx, row); err != nil {
				if repository.IsDuplicateEntry(err) {
					return xerr.BadRequest("产品编码已存在：" + rows[i].Code)
				}
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

// validateProductImportRows 校验产品导入行并把分类名称解析结果写入 resolved 下标。
// 批内重复编码以首行之外的每一行报错；库内已存在的编码按行报错。
func validateProductImportRows(rows []dto.ProductImportRow, categoryIDs map[string][]int64, usedCodes map[string]bool, resolved []int64) []excel.CellError {
	var errs []excel.CellError
	add := func(err *excel.CellError) {
		if err != nil {
			errs = append(errs, *err)
		}
	}
	firstRowByCode := make(map[string]int, len(rows))
	for i, row := range rows {
		add(importTextError(row.Row, "产品名称", row.Name, 128))
		add(importTextError(row.Row, "产品编码", row.Code, 64))
		add(importTextError(row.Row, "单位", row.Unit, 32))
		add(importTextError(row.Row, "描述", row.Description, 500))

		if row.CategoryName != "" {
			switch ids := categoryIDs[row.CategoryName]; {
			case len(ids) == 0:
				add(&excel.CellError{
					Row:    row.Row,
					Column: "分类名称",
					Msg:    "产品分类不存在：" + row.CategoryName,
				})
			case len(ids) > 1:
				add(&excel.CellError{
					Row:    row.Row,
					Column: "分类名称",
					Msg:    "分类名称对应多个分类，请改用唯一名称：" + row.CategoryName,
				})
			default:
				resolved[i] = ids[0]
			}
		}

		if row.Status != nil && !model.ValidProductStatus(*row.Status) {
			add(&excel.CellError{
				Row:    row.Row,
				Column: "状态",
				Msg:    "产品状态只支持 0 下架 / 1 上架",
			})
		}

		switch firstRow, seen := firstRowByCode[row.Code]; {
		case usedCodes[row.Code]:
			add(&excel.CellError{
				Row:    row.Row,
				Column: "产品编码",
				Msg:    "产品编码已存在：" + row.Code,
			})
			usedCodes[row.Code] = true
		case seen:
			add(&excel.CellError{
				Row:    row.Row,
				Column: "产品编码",
				Msg:    "产品编码与第 " + strconv.Itoa(firstRow) + " 行重复：" + row.Code,
			})
		default:
			firstRowByCode[row.Code] = row.Row
		}
	}
	return errs
}

// ExportProducts 导出产品：筛选与列表一致，分类名称由服务端解析，价格输出十进制字符串。
func (s *Service) ExportProducts(ctx context.Context, req *dto.ProductPageReq) ([]byte, error) {
	filter, err := productFilter(req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllProducts(ctx, filter)
	if err != nil {
		return nil, err
	}
	categories, err := s.repo.ListProductCategories(ctx)
	if err != nil {
		return nil, err
	}
	categoryNames := make(map[int64]string, len(categories))
	for _, c := range categories {
		categoryNames[c.ID] = c.Name
	}

	rows := make([]dto.ProductExportRow, 0, len(list))
	for _, p := range list {
		rows = append(rows, dto.ProductExportRow{
			Name:        p.Name,
			Code:        p.Code,
			Category:    categoryNames[p.CategoryId],
			Unit:        p.Unit,
			Price:       p.Price,
			Status:      productStatusNames[p.Status],
			Description: p.Description,
			CreateTime:  p.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}
