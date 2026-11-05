package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// PageContracts 合同分页（名称/状态/客户筛选 + owner-scoped 数据范围）。
func (h *Handler) PageContracts(c *gin.Context) {
	var req dto.ContractPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListContracts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetContract 合同详情（含行项目与合计；数据范围外 404）。
func (h *Handler) GetContract(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	contract, err := h.svc.GetContract(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, contract)
}

// CreateContract 新建合同（HT 编号 + 行项目同事务；金额不变量服务端保证）。
func (h *Handler) CreateContract(c *gin.Context) {
	var req dto.ContractCreateReq
	if !bindJSON(c, &req) {
		return
	}
	contract, err := h.svc.CreateContract(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, contract)
}

// UpdateContract 修改合同（编号/客户/来源商机/状态不可改；已签仅元数据，见 service 层）。
func (h *Handler) UpdateContract(c *gin.Context) {
	var req dto.ContractUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateContract(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ChangeContractStatus 状态流转（唯一状态入口：提交/审批通过/驳回/作废；
// 审批动作的 crm:contract:approve 校验在 ApprovalService 内做，范围外 404，
// 非法流转 400，并发冲突 409）。
func (h *Handler) ChangeContractStatus(c *gin.Context) {
	var req dto.ContractStatusReq
	if !bindJSON(c, &req) {
		return
	}
	contract, err := h.svc.ChangeContractStatus(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, contract)
}

// DeleteContract 删除合同（软删除；待审/已签不可删，见 service 层）。
func (h *Handler) DeleteContract(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteContract(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
