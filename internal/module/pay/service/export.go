package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// export.go 是管理列表 Excel 导出（issue #138，docs/pay-mvp-spec.md §4.12）：
// 五个管理列表（订单/退款/转账/钱包流水/充值单）复用各自列表的筛选谓词与租户
// 隔离口径（repo ListAll*，固定 id DESC 与列表一致），状态/业务类型输出中文
// 展示名（与前端页面同一套枚举文案）；导出启用 NativeDates（日期按业务时区，
// ADR 0006），金额经 money.Money TextMarshaler 输出两位小数十进制字符串。
// 行数无上限：与 CRM/ERP ListAll 同口径，由筛选条件控制结果规模。

// 展示名映射（与前端页面 statusLabels/bizLabels 同一套文案）。
var (
	// OrderStatusNames 订单状态展示名：0 待支付 / 10 成功 / 20 已退款 / 30 已关闭。
	OrderStatusNames = map[int8]string{
		model.OrderStatusPending:  "待支付",
		model.OrderStatusSuccess:  "成功",
		model.OrderStatusRefunded: "已退款",
		model.OrderStatusClosed:   "已关闭",
	}
	// RefundStatusNames 退款单状态展示名：0 待处理 / 10 成功 / 20 失败。
	RefundStatusNames = map[int8]string{
		model.RefundStatusPending: "待处理",
		model.RefundStatusSuccess: "成功",
		model.RefundStatusFail:    "失败",
	}
	// TransferStatusNames 转账单状态展示名：0 等待 / 5 进行中 / 10 成功 / 20 关闭。
	TransferStatusNames = map[int8]string{
		model.TransferStatusWaiting: "等待",
		model.TransferStatusDoing:   "进行中",
		model.TransferStatusSuccess: "成功",
		model.TransferStatusClosed:  "关闭",
	}
	// WalletBizTypeNames 流水业务类型展示名：与 model.WalletBizTypes 六类枚举一致。
	WalletBizTypeNames = map[int8]string{
		model.WalletBizRecharge:       "充值",
		model.WalletBizRechargeRefund: "充值退款",
		model.WalletBizPayment:        "支付",
		model.WalletBizPaymentRefund:  "支付退款",
		model.WalletBizUpdateBalance:  "余额调整",
		model.WalletBizTransfer:       "转账",
	}
	// RechargePayStatusNames 充值单支付状态展示名：0 未支付 / 1 已支付。
	RechargePayStatusNames = map[int8]string{
		model.RechargePayStatusPending: "未支付",
		model.RechargePayStatusSuccess: "已支付",
	}
	// RechargeRefundStatusNames 充值单退款状态展示名：0 无退款 / 10 退款成功 / 20 退款失败。
	RechargeRefundStatusNames = map[int8]string{
		model.RechargeRefundStatusNone:    "无退款",
		model.RechargeRefundStatusSuccess: "退款成功",
		model.RechargeRefundStatusFail:    "退款失败",
	}
	// WalletUserTypeNames 钱包归属类型展示名：1 会员 / 2 后台用户。
	WalletUserTypeNames = map[int8]string{
		model.WalletUserTypeMember: "会员",
		model.WalletUserTypeAdmin:  "后台用户",
	}
)

// named 按 map 取展示名，未知值回落数字（历史数据兜底，与前端 ?? String(status) 一致）。
func named(names map[int8]string, status int8) string {
	if name, ok := names[status]; ok {
		return name
	}
	return fmt.Sprintf("%d", status)
}

// walletOwner 归属用户展示文本：「类型 #ID」（与钱包管理页归属列同口径）。
func walletOwner(userType int8, userID int64) string {
	return fmt.Sprintf("%s #%d", named(WalletUserTypeNames, userType), userID)
}

// ExportOrders 导出支付订单列表（复用订单列表筛选与租户隔离口径，pay:order:export）。
func (s *Service) ExportOrders(ctx context.Context, req *dto.OrderPageReq) ([]byte, error) {
	if req.Status != nil {
		if err := validateOrderStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	list, err := s.repo.ListAllOrders(ctx, payrepo.OrderFilter{
		No:              trimString(req.No),
		MerchantOrderID: trimString(req.MerchantOrderID),
		AppID:           req.AppID,
		ChannelCode:     trimString(req.ChannelCode),
		Status:          req.Status,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]dto.OrderExportRow, 0, len(list))
	for i := range list {
		o := &list[i]
		rows = append(rows, dto.OrderExportRow{
			No:             o.No,
			MerchantID:     o.MerchantOrderID,
			Subject:        o.Subject,
			Price:          o.Price,
			RefundPrice:    o.RefundPrice,
			Status:         named(OrderStatusNames, o.Status),
			ChannelCode:    o.ChannelCode,
			ChannelOrderNo: o.ChannelOrderNo,
			Owner:          walletOwner(o.UserType, o.UserID),
			SuccessTime:    o.SuccessTime,
			CreateTime:     o.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportRefunds 导出退款单列表（复用退款列表筛选与租户隔离口径，pay:refund:export）。
func (s *Service) ExportRefunds(ctx context.Context, req *dto.RefundPageReq) ([]byte, error) {
	if req.Status != nil {
		if err := validateRefundStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	list, err := s.repo.ListAllRefunds(ctx, payrepo.RefundFilter{
		No:               trimString(req.No),
		MerchantRefundID: trimString(req.MerchantRefundID),
		MerchantOrderID:  trimString(req.MerchantOrderID),
		AppID:            req.AppID,
		OrderID:          req.OrderID,
		ChannelCode:      trimString(req.ChannelCode),
		Status:           req.Status,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]dto.RefundExportRow, 0, len(list))
	for i := range list {
		r := &list[i]
		rows = append(rows, dto.RefundExportRow{
			No:               r.No,
			MerchantRefundID: r.MerchantRefundID,
			OrderNo:          r.OrderNo,
			MerchantOrderID:  r.MerchantOrderID,
			PayPrice:         r.PayPrice,
			RefundPrice:      r.RefundPrice,
			Status:           named(RefundStatusNames, r.Status),
			ChannelCode:      r.ChannelCode,
			ChannelRefundNo:  r.ChannelRefundNo,
			Reason:           r.Reason,
			SuccessTime:      r.SuccessTime,
			CreateTime:       r.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportTransfers 导出转账单列表（复用转账列表筛选与租户隔离口径，pay:transfer:export）。
func (s *Service) ExportTransfers(ctx context.Context, req *dto.TransferPageReq) ([]byte, error) {
	if req.Status != nil {
		if err := validateTransferStatus(*req.Status); err != nil {
			return nil, err
		}
	}
	list, err := s.repo.ListAllTransfers(ctx, payrepo.TransferFilter{
		No:                 trimString(req.No),
		MerchantTransferID: trimString(req.MerchantTransferID),
		AppID:              req.AppID,
		ChannelCode:        trimString(req.ChannelCode),
		Status:             req.Status,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]dto.TransferExportRow, 0, len(list))
	for i := range list {
		t := &list[i]
		rows = append(rows, dto.TransferExportRow{
			No:                 t.No,
			MerchantTransferID: t.MerchantTransferID,
			Subject:            t.Subject,
			Price:              t.Price,
			UserAccount:        t.UserAccount,
			UserName:           t.UserName,
			Status:             named(TransferStatusNames, t.Status),
			ChannelCode:        t.ChannelCode,
			ChannelTransferNo:  t.ChannelTransferNo,
			SuccessTime:        t.SuccessTime,
			CreateTime:         t.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportWalletTransactions 导出钱包流水列表（复用流水分页筛选与租户隔离口径，
// pay:wallet:export；spec §7 流水导出挂钱包权限码对）。
func (s *Service) ExportWalletTransactions(ctx context.Context, req *dto.WalletTransactionPageReq) ([]byte, error) {
	if err := validateBizTypeFilter(req.BizType); err != nil {
		return nil, err
	}
	list, err := s.repo.ListAllWalletTransactions(ctx, payrepo.WalletTransactionFilter{
		WalletID: req.WalletID,
		BizType:  req.BizType,
		No:       trimString(req.No),
	})
	if err != nil {
		return nil, err
	}
	rows := make([]dto.WalletTransactionExportRow, 0, len(list))
	for i := range list {
		t := &list[i]
		rows = append(rows, dto.WalletTransactionExportRow{
			No:         t.No,
			WalletID:   t.WalletID,
			BizType:    named(WalletBizTypeNames, t.BizType),
			BizID:      t.BizID,
			Title:      t.Title,
			Price:      t.Price,
			Balance:    t.Balance,
			CreateTime: t.CreatedAt,
		})
	}
	return excel.Export(rows, excel.ExportOptions{NativeDates: true})
}

// ExportRecharges 导出充值单列表（复用充值单列表筛选与租户隔离口径，
// pay:wallet-recharge:export）：套餐名称与挂单订单号由服务端批量解析回填。
func (s *Service) ExportRecharges(ctx context.Context, req *dto.RechargePageReq) ([]byte, error) {
	if req.PayStatus != nil {
		if err := validateRechargePayStatus(*req.PayStatus); err != nil {
			return nil, err
		}
	}
	rows, err := s.repo.ListAllRecharges(ctx, payrepo.RechargeFilter{
		WalletID:  req.WalletID,
		PackageID: req.PackageID,
		PayStatus: req.PayStatus,
	})
	if err != nil {
		return nil, err
	}
	views, err := s.enrichRecharges(ctx, rows)
	if err != nil {
		return nil, err
	}
	packageName, err := s.rechargePackageNames(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := make([]dto.RechargeExportRow, 0, len(views))
	for i := range views {
		v := &views[i]
		pkg := "自定义"
		if v.PackageID != nil {
			pkg = packageName[*v.PackageID]
		}
		out = append(out, dto.RechargeExportRow{
			WalletID:         v.WalletID,
			TotalPrice:       v.TotalPrice,
			PayPrice:         v.PayPrice,
			BonusPrice:       v.BonusPrice,
			Package:          pkg,
			PayStatus:        named(RechargePayStatusNames, v.PayStatus),
			PayChannelCode:   v.PayChannelCode,
			OrderNo:          v.OrderNo,
			PayTime:          v.PayTime,
			RefundStatus:     named(RechargeRefundStatusNames, v.RefundStatus),
			RefundTotalPrice: v.RefundTotalPrice,
			CreateTime:       v.CreatedAt,
		})
	}
	return excel.Export(out, excel.ExportOptions{NativeDates: true})
}

// rechargePackageNames 批量解析充值套餐名称（缺失 ID 留空，同 ERP 导出人名解析口径）。
func (s *Service) rechargePackageNames(ctx context.Context, rows []model.WalletRecharge) (map[int64]string, error) {
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		if rows[i].PackageID != nil {
			ids = append(ids, *rows[i].PackageID)
		}
	}
	names := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	pkgs, err := s.repo.FindRechargePackagesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range pkgs {
		names[pkgs[i].ID] = pkgs[i].Name
	}
	return names, nil
}
