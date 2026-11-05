package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
)

// ---------- 合同 Excel 导出（issue #20，docs/crm-mvp-spec.md §7）----------
// 导出复用合同列表的筛选与数据范围谓词（owner-scoped）；日期列启用 Excel 原生日期
//（#32），金额输出十进制字符串（Money 的 TextMarshaler）。

// contractStatusNames 是合同状态的中文展示（导出用，与前端选项一致）。
var contractStatusNames = map[int8]string{
	model.ContractStatusDraft:   "草稿",
	model.ContractStatusPending: "待审",
	model.ContractStatusSigned:  "已签",
	model.ContractStatusVoid:    "作废",
}

// ExportContracts 导出合同：行内容与当前用户可见范围一致，含金额与签订/起止日期。
func (s *Service) ExportContracts(ctx context.Context, req *dto.ContractPageReq) ([]byte, error) {
	filter, err := s.contractFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllContracts(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.fillContractNames(ctx, list)
	s.fillContractCustomerNames(ctx, list)

	rows := make([]dto.ContractExportRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, dto.ContractExportRow{
			ContractNo:    c.ContractNo,
			Name:          c.Name,
			CustomerName:  c.CustomerName,
			Amount:        c.Amount,
			Status:        contractStatusNames[c.Status],
			SignDate:      c.SignDate,
			StartDate:     c.StartDate,
			EndDate:       c.EndDate,
			OwnerUserName: c.OwnerUserName,
			OwnerDeptName: c.OwnerDeptName,
			Remark:        c.Remark,
			CreateTime:    c.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// fillContractCustomerNames 批量补齐合同的所属客户名称（导出用；列表页客户名由
// 各自组件按需补齐，导出在一张表里统一输出，避免逐行查询）。
func (s *Service) fillContractCustomerNames(ctx context.Context, list []model.Contract) {
	ids := make([]int64, 0, len(list))
	seen := make(map[int64]bool, len(list))
	for _, c := range list {
		if c.CustomerID > 0 && !seen[c.CustomerID] {
			ids = append(ids, c.CustomerID)
			seen[c.CustomerID] = true
		}
	}
	if len(ids) == 0 {
		return
	}
	customers, err := s.repo.FindCustomersByIDs(ctx, ids)
	if err != nil {
		return // 名称缺失不阻塞导出（与列表补齐的降级口径一致）
	}
	names := make(map[int64]string, len(customers))
	for _, c := range customers {
		names[c.ID] = c.Name
	}
	for i := range list {
		list[i].CustomerName = names[list[i].CustomerID]
	}
}
