package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// ---------- 回款计划 Excel 导出（issue #20，docs/crm-mvp-spec.md §7）----------
// 导出复用回款计划列表的筛选与数据范围谓词（join 合同）；状态与逾期是派生值，
// 与列表用同一纯函数判定；到期日启用 Excel 原生日期（#32），金额输出十进制字符串。

// ExportReceivablePlans 导出回款计划：含合同/客户、计划金额、已收金额、到期日与派生状态。
func (s *Service) ExportReceivablePlans(ctx context.Context, req *dto.ReceivablePlanPageReq) ([]byte, error) {
	now := s.businessNow()
	filter, err := s.receivablePlanFilter(ctx, req, now)
	if err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllReceivablePlans(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.fillReceivablePlans(ctx, list, now)

	rows := make([]dto.ReceivablePlanExportRow, 0, len(list))
	for _, p := range list {
		overdue := "否"
		if p.Overdue {
			overdue = "是"
		}
		rows = append(rows, dto.ReceivablePlanExportRow{
			ContractNo:     p.ContractNo,
			ContractName:   p.ContractName,
			CustomerName:   p.CustomerName,
			PeriodNo:       p.PeriodNo,
			ContractAmount: p.ContractAmount,
			Amount:         p.Amount,
			ReceivedAmount: p.ReceivedAmount,
			DueDate:        p.DueDate,
			Status:         ReceivableStatusNames[p.Status],
			Overdue:        overdue,
			Remark:         p.Remark,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}
