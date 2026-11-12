package dto

// 钱包 DTO 只含分页筛选：自助/管理端返回直接复用 service 层的
// model.Wallet / model.WalletTransaction（JSON 契约即模型序列化，同应用域）。

// ---------- 钱包（租户级，issue #132） ----------

// WalletPageReq 钱包分页筛选（管理端 GET /pay/wallet/page，权限 pay:wallet:query）。
type WalletPageReq struct {
	PageNo   int   `form:"pageNo"`
	PageSize int   `form:"pageSize"`
	UserID   int64 `form:"userId"`
	UserType *int8 `form:"userType"`
}

// WalletTransactionPageReq 钱包流水分页筛选。
//   - 管理端 GET /pay/wallet-transaction/page（权限 pay:wallet:query）：
//     walletId/bizType/no 全部可选。
//   - 自助端点 GET /pay/wallet-transaction/my（仅登录）：服务层强制 walletId
//     为本人钱包，请求里的 walletId 不采纳（spec §4.11）。
type WalletTransactionPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	WalletID int64  `form:"walletId"`
	BizType  *int8  `form:"bizType"`
	No       string `form:"no"`
}
