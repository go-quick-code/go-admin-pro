package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 合同（issue #16，docs/crm-mvp-spec.md §3 销售节/§5）----------
// 金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。
// 金额不变量（spec §4.3）：有行项目时合同金额 = SUM(行项目金额)，由服务端计算落库，
// 客户端提交的 amount 被忽略（与商机行项目同规则）；无行项目时手填且 ≥ 0。
// 行项目金额（amount）由服务端按 数量 × 单价 计算，客户端不提交。

// ContractProductItem 是合同行项目的提交载体。
// quantity 不挂 binding:required：0/负数的拒绝文案在 service 层（带行序号的中文），
// binding 层的英文报错对用户不友好；productId 挂 required（缺产品即结构性错误）。
type ContractProductItem struct {
	ProductID int64       `json:"productId" binding:"required"`
	Quantity  int64       `json:"quantity"`
	Price     money.Money `json:"price"`
}

// ContractCreateReq 新建合同：编号 HT+yyyyMMdd+4 位序号由服务端发放；
// 状态缺省 1 草稿（创建即待审/已签不做——状态机入口唯一，走 PUT /crm/contract/status）。
// 负责人缺省当前登录用户，部门快照随负责人写入（ADR 0004）。
// opportunityId 可空：合同可不经商机直接创建；传了则商机须存在、可见且属于同一客户。
type ContractCreateReq struct {
	CustomerID    int64                 `json:"customerId" binding:"required"`
	OpportunityID int64                 `json:"opportunityId"`
	Name          string                `json:"name" binding:"required,max=128"`
	Amount        money.Money           `json:"amount"`
	SignDate      string                `json:"signDate"`
	StartDate     string                `json:"startDate"`
	EndDate       string                `json:"endDate"`
	OwnerUserID   int64                 `json:"ownerUserId"`
	Remark        string                `json:"remark" binding:"max=500"`
	Products      []ContractProductItem `json:"products"`
}

// ContractUpdateReq 修改合同：编号/客户/来源商机/状态不可经编辑接口改动
// （状态流转走 PUT /crm/contract/status，入口唯一）。
// 编辑口径（见 service 层与提交说明）：草稿/待审可全量编辑（行项目随单整体替换，
// 金额按不变量重算）；已签仅元数据可改（名称/签订/起止日期/备注），行项目必须缺省
// （传了 400），金额锁定；已作废不可编辑。products 传 nil 保持行项目不变，传空数组清空。
type ContractUpdateReq struct {
	ID        int64                 `json:"id" binding:"required"`
	Name      string                `json:"name" binding:"required,max=128"`
	Amount    money.Money           `json:"amount"`
	SignDate  string                `json:"signDate"`
	StartDate string                `json:"startDate"`
	EndDate   string                `json:"endDate"`
	Remark    string                `json:"remark" binding:"max=500"`
	Products  []ContractProductItem `json:"products"`
}

// ContractPageReq 合同分页筛选：名称模糊、状态、所属客户（客户详情合同 tab）。
// 状态传非法值直接 400，绝不静默当成「无此筛选」。
type ContractPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Name       string `form:"name"`
	Status     *int8  `form:"status"`
	CustomerID int64  `form:"customerId"`
}

// ContractStatusReq 状态流转（PUT /crm/contract/status，唯一状态入口）：
//   - 1 草稿 ← 2 待审：驳回（reason 为驳回原因，可空），须 crm:contract:approve；
//   - 2 待审 ← 1 草稿：提交审批；
//   - 3 已签 ← 2 待审：审批通过，须 crm:contract:approve；
//   - 4 作废：草稿/待审/已签均可进入（#18 将接「有回款记录禁止作废」守卫）。
//
// 非法流转与重复操作 400；并发冲突（状态已被他人变更）409。
type ContractStatusReq struct {
	ID     int64  `json:"id" binding:"required"`
	Status int8   `json:"status" binding:"required"`
	Reason string `json:"reason" binding:"max=500"`
}

// ContractFileResp 是合同附件的展示视图（issue #17：复用 infra_file，
// 只暴露前端附件区需要的字段，不透出 infra 存储实现细节）。
type ContractFileResp struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	Type       string    `json:"type"`
	CreateTime time.Time `json:"createTime"`
}
