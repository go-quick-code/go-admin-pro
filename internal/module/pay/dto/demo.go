package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 示例订单（租户级，issue #137，spec §5/§6/§4.11） ----------

// DemoSPUOption 是演示商品字典项（GET /pay/demo-order/spus，登录即可）：示例
// 订单创建表单的商品选择数据源。spec §5 未列字典端点——按 #136 套餐自助清单
// （GET /pay/wallet-recharge-package/list）先例补充，避免前端复刻字典漂移。
type DemoSPUOption struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Price string `json:"price"`
}

// DemoOrderCreateReq 创建示例订单（POST /pay/demo-order/create，登录）：
// 只收 spuId——名称/金额服务端按硬编码字典定（spec §3.5 字典不建表、金额
// 服务端定 spec §4.2），请求不收金额。
type DemoOrderCreateReq struct {
	SpuID int64 `json:"spuId" binding:"required"`
}

// DemoOrderPageReq 示例订单分页筛选（管理端 GET /pay/demo-order/page，权限
// pay:demo-order:query）。
type DemoOrderPageReq struct {
	PageNo    int   `form:"pageNo"`
	PageSize  int   `form:"pageSize"`
	UserID    int64 `form:"userId"`
	PayStatus *int8 `form:"payStatus"`
}

// DemoOrderView 是示例订单视图（订单 + 挂单应用/订单定位回填）：发起支付按钮
// 据此跳收银台（appKey + merchantOrderId = DEMO-<订单 ID>，照 #136 我的充值
// 形态）；订单本体列见 model.DemoOrder。
type DemoOrderView struct {
	*model.DemoOrder
	AppKey          string `json:"appKey"`
	PayOrderNo      string `json:"payOrderNo"`
	MerchantOrderID string `json:"merchantOrderId"`
}

// DemoOrderRefundReq 示例订单发起退款（PUT /pay/demo-order/:id/refund，登录 +
// 本人订单）：reason 可选；金额服务端定（原单未退余额全额退，spec §4.4 累计
// 裁决在 #135 退款链路），请求不收金额。
type DemoOrderRefundReq struct {
	Reason string `json:"reason" binding:"max=255"`
}

// ---------- 示例提现（租户级，issue #137，spec §5/§6/§4.8/§4.11） ----------

// DemoWithdrawCreateReq 申请提现（POST /pay/demo-withdraw/create，登录）：
// subject/price 服务端校验；user_account/user_name 服务端取登录主体（请求不收
// ——自助端点归属即本人，spec §4.11）；type 仅 1 钱包（2 支付宝预留，创建即
// 拒绝——无渠道可转，spec §3.5）。
type DemoWithdrawCreateReq struct {
	Subject string      `json:"subject" binding:"required,max=100"`
	Price   money.Money `json:"price"`
	Type    *int8       `json:"type"`
}

// DemoWithdrawPageReq 示例提现分页筛选（管理端 GET /pay/demo-withdraw/page，
// 权限 pay:demo-withdraw:query）。
type DemoWithdrawPageReq struct {
	PageNo   int   `form:"pageNo"`
	PageSize int   `form:"pageSize"`
	UserID   int64 `form:"userId"`
	Status   *int8 `form:"status"`
}
