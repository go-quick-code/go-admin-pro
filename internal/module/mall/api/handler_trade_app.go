package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// handler_trade_app.go 是 mall C 端交易端点的 HTTP 入口（issue #179，T6；
// docs/mall-mvp-spec.md §4「购物车四操作 + 试算 + 创建订单 + 我的订单/详情/
// 取消 + 我的积分流水」）。与 handler_app.go 同形状：挂公开组
//（RegisterPublic 的 tokenGroup），Bearer 会员令牌经 memberAuth 中间件自
// 校验后进入（Principal.UserID = 会员 ID，TenantID 由令牌自述 + DB 回查），
// 60 次/分钟浏览限速。
//
// 越权防线（spec §4）：全部端点的归属主体恒取 memberAuth 写入的 Principal
//（UserID），不收客户端传参——会员 A 拿不到会员 B 的购物车/订单/积分行，
// service 层 user_id 谓词 + 404 不泄露存在性。

// appMemberID 取当前会员 ID（memberAuth 自校验通过即存在；防御路径——路由
// 误挂组时 401，不把无主体请求送进 service，照 AppMe 同款）。
func (h *Handler) appMemberID(c *gin.Context) (int64, bool) {
	principal, ok := authn.From(c.Request.Context())
	if !ok {
		response.Abort(c, xerr.Unauthorized(service.MemberTokenUnauthorizedMsg))
		return 0, false
	}
	return principal.UserID, true
}

// appUserIP 取发起方 IP 审计列取值：恒用 c.RemoteIP()（直连对端），与
// limitAppIP 限速同口径。刻意不用 c.ClientIP()：仓库未配置 SetTrustedProxies
// 时 gin 默认信任任意来源，ClientIP() 会采信请求方可随意伪造的
// X-Forwarded-For——审计列（pay_order 的发起来源记录）若取 XFF，伪造 IP
// 即污染审计与风控数据（PR #184 审查 H3，理由全文见 handler_app.go
// limitAppIP 注释；部署级代理信任拓扑不在 handler 层收敛）。
func (h *Handler) appUserIP(c *gin.Context) string {
	return c.RemoteIP()
}

// ---------- 购物车（加购/数量/选中/删除/我的购物车） ----------

// AppAddCart 加购（POST /mall/app/cart）：skuId + count，同 SKU 合并数量；
// SKU 必须属于上架 SPU（下架 409，不存在 404）。
func (h *Handler) AppAddCart(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	var req dto.AppCartAddReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AppAddCart(c.Request.Context(), memberID, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// AppUpdateCartCount 修改加购数量（PUT /mall/app/cart/:id/count）：绝对值
// 覆盖，正整数。
func (h *Handler) AppUpdateCartCount(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	var req dto.AppCartCountReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AppUpdateCartCount(c.Request.Context(), memberID, id, req.Count); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// AppSelectCart 结算选中切换（PUT /mall/app/cart/:id/selected）：selected 0/1。
func (h *Handler) AppSelectCart(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	var req dto.AppCartSelectReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AppSelectCart(c.Request.Context(), memberID, id, *req.Selected); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// AppDeleteCart 删除购物车行（POST /mall/app/cart/delete，批量 IDs）。
func (h *Handler) AppDeleteCart(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	var req dto.AppCartDeleteReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AppDeleteCart(c.Request.Context(), memberID, req.IDs); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// AppListCart 我的购物车（GET /mall/app/cart）：全量行 + SKU 现价/现库存/
// 规格图 + SPU 名称/上架态 + 选中项汇总。
func (h *Handler) AppListCart(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	resp, err := h.svc.AppListCart(c.Request.Context(), memberID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// ---------- 结算试算 / 创建订单 ----------

// AppSettlement 结算试算（GET /mall/app/trade/settlement）：选中项 + 默认地址
// + 金额三列（服务端定），不下单。
func (h *Handler) AppSettlement(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	resp, err := h.svc.AppSettlementPreview(c.Request.Context(), memberID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// AppCreateOrder 创建订单（POST /mall/app/trade/order）：收货地址 id + 备注，
// 按选中购物车项结算；下单即扣库存（不足 409）；金额/单号服务端定。
// 事务提交后挂 pay 支付单（issue #181，clientIP 供 pay 单记录发起来源）。
func (h *Handler) AppCreateOrder(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	var req dto.AppCreateOrderReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.AppCreateOrder(c.Request.Context(), memberID, &req, h.appUserIP(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// ---------- 我的订单（分页/详情/取消） ----------

// AppPageOrders 我的订单分页（GET /mall/app/trade/order/page）：状态过滤选填，
// user_id 恒取当前会员。
func (h *Handler) AppPageOrders(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	var req dto.AppOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.AppPageOrders(c.Request.Context(), memberID, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// AppGetOrder 我的订单详情（GET /mall/app/trade/order/:id）：含 items；
// 不存在/跨会员统一 404。
func (h *Handler) AppGetOrder(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	resp, err := h.svc.AppGetOrder(c.Request.Context(), memberID, id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// AppCancelOrder 取消待支付订单（POST /mall/app/trade/order/:id/cancel）：
// 状态机校验（非待支付 409）；库存回补与状态迁移同事务。
func (h *Handler) AppCancelOrder(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	var req dto.AppCancelOrderReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AppCancelOrder(c.Request.Context(), memberID, id, &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ---------- 发起支付（内部转 pay，issue #181 T8） ----------

// AppPayOrder 发起支付（POST /mall/app/trade/order/:id/pay）：channelCode
// 选渠道，mall 端点内部委托转 paySvc（建单幂等复用 + SubmitOrder，见
// service.AppPayOrder）。wallet 渠道同步成功返回 status=10（订单联动已
// 支付）；mock 渠道返回 status=0/displayMode=none（等渠道回调）。pay 侧
// 业务错误（余额不足 409 等）原样透传。
func (h *Handler) AppPayOrder(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	var req dto.AppOrderPayReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.AppPayOrder(c.Request.Context(), memberID, id, &req, h.appUserIP(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// ---------- 我的积分流水 ----------

// AppPageMyPointRecords 我的积分流水分页（GET /mall/app/trade/point-records）：
// 复用 T2 的 repo.ListPointRecords，user_id 恒取当前会员。
func (h *Handler) AppPageMyPointRecords(c *gin.Context) {
	memberID, ok := h.appMemberID(c)
	if !ok {
		return
	}
	var req dto.AppPointRecordPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.AppPageMyPointRecords(c.Request.Context(), memberID, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
