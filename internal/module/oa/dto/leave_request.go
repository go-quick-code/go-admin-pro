// leave_request.go 是请假申请的请求/响应 DTO（issue #52，docs/oa-mvp-spec.md
// §3.5/§4.8/§5）：草稿保存、本人分页、详情（含审批时间线与附件）。
package dto

import (
	bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// LeaveRequestSaveReq 是请假申请草稿的创建/更新请求（POST/PUT
// /oa/leave/request）：起止日期 YYYY-MM-DD + 半天（0 上午 / 1 下午）+ 事由；
// 时长由服务端按工作日历计算，非工作日不计。参数问题 400（同日
// 下午→上午、起止颠倒、跨自然年），重叠/余额不足在提交时校验。
type LeaveRequestSaveReq struct {
	// ID 更新时必填（仅草稿/已驳回可改）。
	ID int64 `json:"id"`
	// LeaveTypeID 请假类型（启用中的类型）。
	LeaveTypeID int64 `json:"leaveTypeId" binding:"required"`
	// StartDate 开始日期 YYYY-MM-DD。
	StartDate string `json:"startDate" binding:"required"`
	// StartHalf 开始半天：0 上午 / 1 下午。
	StartHalf int8 `json:"startHalf"`
	// EndDate 结束日期 YYYY-MM-DD（可以是休息日，时长只计工作日）。
	EndDate string `json:"endDate" binding:"required"`
	// EndHalf 结束半天：0 上午 / 1 下午。
	EndHalf int8 `json:"endHalf"`
	// Reason 请假事由（1–500 字）。
	Reason string `json:"reason" binding:"required"`
}

// LeaveRequestPageReq 是本人请假分页（GET /oa/leave/request/page）：
// status 可选精确过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回/5 已取消）。
type LeaveRequestPageReq struct {
	web.PageQuery
	Status *int8 `form:"status"`
}

// LeaveRequestItem 是请假申请行视图（列表/详情/提交返回共用）：
// 附类型编码/名称与时长快照；approvalInstanceId 提交后才非空。
type LeaveRequestItem struct {
	ID                 int64       `json:"id"`
	UserID             int64       `json:"userId"`
	LeaveTypeID        int64       `json:"leaveTypeId"`
	TypeCode           string      `json:"typeCode"`
	TypeName           string      `json:"typeName"`
	StartDate          string      `json:"startDate"`
	StartHalf          int8        `json:"startHalf"`
	EndDate            string      `json:"endDate"`
	EndHalf            int8        `json:"endHalf"`
	DurationDays       money.Money `json:"durationDays"`
	Reason             string      `json:"reason"`
	Status             int8        `json:"status"`
	RejectReason       string      `json:"rejectReason"`
	ApprovalInstanceID *int64      `json:"approvalInstanceId"`
	CreateTime         string      `json:"createTime"`
	UpdateTime         string      `json:"updateTime"`
}

// LeaveRequestDetailResp 是请假申请详情（GET /oa/leave/request/{id}）：
// 申请行 + 附件列表 + bpm 审批时间线（提交后；审批人/时间线展示名由 bpm
// 侧解析）。approvals 是历次审批尝试（倒序，current 标记当前实例；spec
// §4.3 每次重新提交创建新实例）；approval 保留为当前实例的便捷引用，
// 草稿尚未发起审批时二者均为空。
type LeaveRequestDetailResp struct {
	LeaveRequestItem
	Files     []LeaveFileResp         `json:"files"`
	Approvals []bpmdto.InstanceDetail `json:"approvals,omitempty"`
	Approval  *bpmdto.InstanceDetail  `json:"approval,omitempty"`
}
