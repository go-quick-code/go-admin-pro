package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 充值套餐（租户级，issue #136，spec §5/§4.7） ----------

// RechargePackageSaveReq 创建充值套餐（POST /pay/wallet-recharge-package，
// 权限 pay:wallet-recharge-package:create）：name/payPrice/bonusPrice/status。
// 金额服务端校验（payPrice > 0，bonusPrice ≥ 0）；档位在建单时快照进充值单。
type RechargePackageSaveReq struct {
	Name       string      `json:"name" binding:"required,max=100"`
	PayPrice   money.Money `json:"payPrice"`
	BonusPrice money.Money `json:"bonusPrice"`
	Status     *int8       `json:"status"`
}

// RechargePackageUpdateReq 修改充值套餐（PUT /pay/wallet-recharge-package，
// id 在 body，与 CRM/ProTable 一致）。改档位只影响后续新建充值单
// （快照隔离），已建充值单不变（spec 验收）。
type RechargePackageUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	RechargePackageSaveReq
}

// RechargePackagePageReq 充值套餐分页筛选（GET /pay/wallet-recharge-package/page，
// 权限 pay:wallet-recharge-package:query）。
type RechargePackagePageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// RechargePackageOption 是自助端点「发起充值」的套餐选择项
// （GET /pay/wallet-recharge-package/list，登录即可，只含启用档位）。
type RechargePackageOption struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PayPrice   string `json:"payPrice"`
	BonusPrice string `json:"bonusPrice"`
}

// ---------- 充值单（租户级，issue #136，spec §5/§4.7） ----------

// RechargeCreateReq 发起充值（POST /pay/wallet-recharge/create，登录 + 本人钱包）：
//   - packageID > 0：选套餐（pay_price/bonus_price 服务端按套餐快照，请求不收
//     金额——金额服务端定，spec §4.2/§4.7）；
//   - packageID 缺省：自定义充值，payPrice 服务端校验（> 0），bonus_price 恒 0。
//
// 同事务挂一笔支付订单（subject=钱包充值，本人 user_id/user_type），订单走
// 收银台支付（钱包渠道被禁，spec §4.7）；expire_time 服务端设定（请求不收）。
type RechargeCreateReq struct {
	PackageID int64       `json:"packageId"`
	PayPrice  money.Money `json:"payPrice"`
}

// RechargeCreateResp 发起充值结果：充值单 + 支付订单定位（appKey + 收银台
// 参数），前端直接跳收银台发起支付。
type RechargeCreateResp struct {
	Recharge        *model.WalletRecharge `json:"recharge"`
	AppKey          string                `json:"appKey"`
	OrderID         int64                 `json:"orderId"`
	OrderNo         string                `json:"orderNo"`
	MerchantOrderID string                `json:"merchantOrderId"`
}

// RechargePageReq 充值单分页筛选（管理端 GET /pay/wallet-recharge/page，
// 权限 pay:wallet-recharge:query）。
type RechargePageReq struct {
	PageNo    int   `form:"pageNo"`
	PageSize  int   `form:"pageSize"`
	WalletID  int64 `form:"walletId"`
	PackageID int64 `form:"packageId"`
	PayStatus *int8 `form:"payStatus"`
}

// RechargeView 是充值单视图（充值单 + 挂单应用/订单定位回填）：我的充值页
// 据此跳收银台（appKey + merchantOrderId）与发起退款（POST /pay/refund/create
// 挂充值单订单，spec §4.7）；充值单本体列见 model.WalletRecharge。
type RechargeView struct {
	*model.WalletRecharge
	AppKey          string `json:"appKey"`
	OrderNo         string `json:"orderNo"`
	MerchantOrderID string `json:"merchantOrderId"`
}

// RechargeMyPageReq 本人充值单分页筛选（GET /pay/wallet-recharge/my，登录）：
// 服务层强制 walletId 为本人钱包，请求不收（spec §4.11）。
type RechargeMyPageReq struct {
	PageNo    int   `form:"pageNo"`
	PageSize  int   `form:"pageSize"`
	PayStatus *int8 `form:"payStatus"`
}
