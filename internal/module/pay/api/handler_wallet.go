package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_wallet.go 是钱包域的 HTTP 入口（issue #132，spec §5/§4.11）：
//   - 自助端点（GET /pay/wallet/my、GET /pay/wallet-transaction/my）只挂登录
//     （路由不设权限码），user_id 归属过滤在 service 层强制；
//   - 管理端点（GET /pay/wallet/page、GET /pay/wallet-transaction/page）挂
//     pay:wallet:query。钱包与流水只读（唯一入账入口无 HTTP 面，余额调整
//     v1 无管理端入口，spec §4.3 差异清单）。

// GetMyWallet 本人钱包（懒创建：首次查询无钱包则插入）。
func (h *Handler) GetMyWallet(c *gin.Context) {
	wallet, err := h.svc.GetMyWallet(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, wallet)
}

// PageMyWalletTransactions 本人钱包流水分页（服务层强制本人钱包过滤）。
func (h *Handler) PageMyWalletTransactions(c *gin.Context) {
	var req dto.WalletTransactionPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListMyWalletTransactions(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// PageWallets 钱包分页（管理端）。
func (h *Handler) PageWallets(c *gin.Context) {
	var req dto.WalletPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListWallets(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// PageWalletTransactions 钱包流水分页（管理端，流水抽屉数据源）。
func (h *Handler) PageWalletTransactions(c *gin.Context) {
	var req dto.WalletTransactionPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListWalletTransactions(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
