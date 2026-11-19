package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_export.go 是管理列表 Excel 导出端点（docs/mall-mvp-spec.md §1 第 8 步
// 「Excel 导出」，PR #184 双轴审查补票）：会员/商品 SPU/交易订单三个管理列表
// 导出，照 pay api/excel.go 先例——复用各自列表的筛选 DTO（导出内容与权限
// 一致，spec 硬约束）与租户隔离口径；权限码 mall:member:export /
// mall:spu:export / mall:trade:export（按钮落菜单种子 9015/9145/9214）。
// 响应构造单点化为 response.SendXLSX（spec #144：ASCII 文件名兼容旧客户端，
// filename* 提供中文名；日期键按业务时区 ADR 0006）。

// sendExcel 输出 xlsx 附件：ASCII 文件名兼容旧客户端，filename* 提供中文名。
// 文件名日期键按业务时区（ADR 0006），不依赖服务器 TZ。响应构造单点化为
// response.SendXLSX（pay sendExcel 同款薄委托）。
func (h *Handler) sendExcel(c *gin.Context, base, title string, data []byte) {
	response.SendXLSX(c, h.location, base, title, data)
}

// ExportMembers 导出会员列表（mall:member:export，复用会员列表筛选）。
func (h *Handler) ExportMembers(c *gin.Context) {
	var req dto.MemberPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportMembers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "mall-members", "会员", data)
}

// ExportSPUs 导出商品 SPU 列表（mall:spu:export，复用 SPU 列表筛选）。
func (h *Handler) ExportSPUs(c *gin.Context) {
	var req dto.SPUPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportSPUs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "mall-spu", "商品SPU", data)
}

// ExportTradeOrders 导出交易订单列表（mall:trade:export，复用管理端订单筛选）。
func (h *Handler) ExportTradeOrders(c *gin.Context) {
	var req dto.AdminOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "mall-trade-orders", "交易订单", data)
}
