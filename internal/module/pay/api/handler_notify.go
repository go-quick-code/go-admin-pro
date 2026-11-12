package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/ratelimit"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_notify.go 是回调通知域的 HTTP 入口（issue #134，spec §5/§4.6 demo
// 回调；PR #140 F1 补渠道回调端点）：
//   - 管理端 GET /pay/notify-task/page|:id 挂 pay:notify-task:query（交易单据
//     append-only，无写入端点，spec §4.10）；
//   - demo 商户回调三端点（POST /pay/demo/notify/order|refund|transfer）是
//     公开路由（RegisterPublic，无登录；IP 限速照社交回调先例；公开端点不记
//     访问日志——router 结构使然）：验签 Pay-Signature → 解析 → 业务联动扩展点
//     → 业务码信封应答；
//   - 渠道支付回调 POST /pay/channel-notify/:channelCode（spec §4.6，仅承接
//     支付回调——退款/转账渠道回调随真实渠道实现另立工单）：公开可达同挂 IP
//     限速与 1MB 请求体上限；渠道码 → Client ∧ 平台启用（否则 404）→ 渠道
//     验签解析（失败 4xx）→ 支付成功入账（幂等认领）→ 业务码信封应答。
//
// 公开端点应答语义与商户回调 job 的响应语义闭环（spec §4.5）：
//   - 验签/解析失败 → HTTP 4xx（任务 22 请求失败）；
//   - 处理成功 → HTTP 200 {"code":0}（任务 10 成功）；
//   - 联动失败 → HTTP 200 {"code":500,...}（任务 21 继续重试）。

// demoNotifyIPLimit 是 demo 回调端点按客户端 IP 的限速阈值：每分钟 60 次
// （照社交回调 publicIPLimit 先例）。进程内单实例语义，多实例部署由反代层另配。
const demoNotifyIPLimit = 60

// demoNotifyBodyLimit 是 demo 回调请求体的读取上限（照 socialHTTP 出站侧的
// 1MB 先例，防御性——正常回调报文远小于此）。
const demoNotifyBodyLimit = 1 << 20

// demoNotifySignatureHeader 是 HMAC 签名请求头（spec §4.5）。
const demoNotifySignatureHeader = "Pay-Signature"

// demoNotifyLimiter 创建 IP 限速器（Handler 构造时调用）。
func demoNotifyLimiter() *ratelimit.Limiter {
	return ratelimit.New(demoNotifyIPLimit, time.Minute)
}

// RegisterPublic 注册 pay 模块公开接口（#134 接入 internal/router 预留的槽位）：
// demo 商户回调三端点 + 渠道支付回调端点（PR #140 F1，spec §4.6）。无登录
// （验签即身份）、挂 IP 限速、不记访问日志（公开组不在 router 的 protected
// 组内，结构使然，spec §4.6）。
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	demo := rg.Group("/pay/demo/notify")
	demo.Use(h.limitDemoIP())
	demo.POST("/"+service.DemoNotifyKindOrder, h.DemoNotifyOrder)
	demo.POST("/"+service.DemoNotifyKindRefund, h.DemoNotifyRefund)
	demo.POST("/"+service.DemoNotifyKindTransfer, h.DemoNotifyTransfer)
	// 渠道支付回调（spec §4.6：渠道码在路径，未实现/停用渠道 404）：
	// 限速与请求体上限照 demo 回调同款先例。
	channelNotify := rg.Group("/pay/channel-notify")
	channelNotify.Use(h.limitDemoIP())
	channelNotify.POST("/:channelCode", h.ChannelNotify)
}

// limitDemoIP 按直连对端地址（c.RemoteIP()）固定窗口限速（超限 429，照社交
// 回调先例）。限速键刻意不用 c.ClientIP()：仓库未配置 SetTrustedProxies 时
// gin 默认信任任意来源，ClientIP() 会采信请求方可随意伪造的 X-Forwarded-For，
// 每请求轮换 XFF 即可无限开新桶（PR #140 安全审查 P3-2）。
//
// 部署注意（已知权衡）：反代之后所有客户端共享直连对端（反代 IP）一个桶——
// 对 demo/notify 这类公开演练端点可接受；全局代理信任拓扑（SetTrustedProxies
// + 按跳数取真实来源）属部署级决策，不在本差异内收敛。
func (h *Handler) limitDemoIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.demoLimiter != nil && !h.demoLimiter.Allow(c.RemoteIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": http.StatusTooManyRequests, "msg": "请求过于频繁，请稍后重试"})
			return
		}
		c.Next()
	}
}

// ---------- demo 商户回调端点（公开） ----------

// DemoNotifyOrder 模拟商户接收支付结果回调（POST /pay/demo/notify/order）。
func (h *Handler) DemoNotifyOrder(c *gin.Context) {
	h.handleDemoNotify(c, service.DemoNotifyKindOrder)
}

// DemoNotifyRefund 模拟商户接收退款结果回调（POST /pay/demo/notify/refund）。
func (h *Handler) DemoNotifyRefund(c *gin.Context) {
	h.handleDemoNotify(c, service.DemoNotifyKindRefund)
}

// DemoNotifyTransfer 模拟商户接收转账结果回调（POST /pay/demo/notify/transfer）。
func (h *Handler) DemoNotifyTransfer(c *gin.Context) {
	h.handleDemoNotify(c, service.DemoNotifyKindTransfer)
}

// handleDemoNotify 是三端点的公共链路：读原文（限 1MB）→ 验签 + 解析 +
// 业务联动扩展点（service 层）→ 业务码信封应答（channel-style JSON）。
func (h *Handler) handleDemoNotify(c *gin.Context, kind string) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, demoNotifyBodyLimit))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "msg": "读取请求体失败"})
		return
	}
	payload, err := h.svc.ProcessDemoNotify(c.Request.Context(), kind, c.GetHeader(demoNotifySignatureHeader), body)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "成功", "data": gin.H{
			"merchantOrderId":    payload.MerchantOrderID,
			"merchantRefundId":   payload.MerchantRefundID,
			"merchantTransferId": payload.MerchantTransferID,
			"dataId":             payload.DataID,
		}})
	case errors.Is(err, service.ErrDemoSignatureInvalid), errors.Is(err, service.ErrDemoPayloadInvalid):
		// 验签/报文失败 4xx（spec §4.6：不泄内部信息——文案即哨兵错误文案）。
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "msg": err.Error()})
	default:
		// 业务联动失败：HTTP 200 + 非 0 业务码（回调任务转 21 继续重试，
		// spec §4.5 响应语义；摘要进 pay_notify_log 供排查）。
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": truncateMsg(err.Error())})
	}
}

// ---------- 渠道支付回调端点（公开，spec §4.6，PR #140 F1） ----------

// ChannelNotify 渠道支付回调（POST /pay/channel-notify/:channelCode，公开）：
// 渠道码 → Client ∧ 平台启用（未实现/停用渠道 404，spec §4.6）→ 渠道验签解析
// （失败 4xx，验签在渠道 Client 内）→ 支付成功入账（幂等认领，重复回调不双
// 入账/不双建任务）→ 业务码信封应答（语义同 demo 商户回调端点；在途回调任务
// 的重试分类由商户回调 job 闭环，spec §4.5）。
//
// 仅承接支付（order）回调：退款/转账渠道回调随真实渠道实现另立工单（SPI 的
// ParseRefundNotify / 异步转账回调成对承接，spec §4.6/§10）。
func (h *Handler) ChannelNotify(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, demoNotifyBodyLimit))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "msg": "读取请求体失败"})
		return
	}
	result, err := h.svc.ChannelNotifyOrder(c.Request.Context(), c.Param("channelCode"), body)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "成功", "data": gin.H{
			"outTradeNo":     result.OutTradeNo,
			"channelOrderNo": result.ChannelOrderNo,
			"success":        result.Success,
			"claimed":        result.Claimed,
		}})
	case errors.Is(err, service.ErrChannelNotifyUnknownChannel):
		// 未实现/停用渠道 404（spec §4.6）：同答不区分实现/启停差异。
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "msg": service.ErrChannelNotifyUnknownChannel.Error()})
	case errors.Is(err, service.ErrChannelNotifyPayloadInvalid):
		// 验签/解析失败 4xx（spec §4.6）：文案随渠道实现（即面向回调方的
		// 报文错误说明），不泄内部信息。
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "msg": err.Error()})
	default:
		// 定位/联动失败：HTTP 200 + 非 0 业务码（同 demo 端点语义——回调方
		// 可重试；在途回调任务由商户回调 job 分类器转 21 继续，spec §4.5）。
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": truncateMsg(err.Error())})
	}
}

// truncateMsg 把业务失败文案截断到 500 rune（应答体进 pay_notify_log 摘要，
// 防异常实现把日志灌爆）。
func truncateMsg(msg string) string {
	runes := []rune(msg)
	if len(runes) > 500 {
		return string(runes[:500])
	}
	return msg
}

// ---------- 回调通知管理端（pay:notify-task:query） ----------

// PageNotifyTasks 回调任务分页（管理端）。
func (h *Handler) PageNotifyTasks(c *gin.Context) {
	var req dto.NotifyTaskPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListNotifyTasks(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetNotifyTask 回调任务详情（含执行日志时间线）。
func (h *Handler) GetNotifyTask(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetNotifyTask(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}
