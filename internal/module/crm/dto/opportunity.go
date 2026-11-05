package dto

import (
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// ---------- 商机（issue #15，docs/crm-mvp-spec.md §3 销售节/§5）----------
// 金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。
// 行项目金额（amount）由服务端按 数量 × 单价 计算，客户端不提交——保证一致性，
// #16 合同行项目复用同一规则（提交说明已注明）。

// OpportunityProductItem 是商机行项目的提交载体。
// quantity 不挂 binding:required：0/负数的拒绝文案在 service 层（带行序号的中文），
// binding 层的英文报错对用户不友好；productId 挂 required（缺产品即结构性错误）。
type OpportunityProductItem struct {
	ProductID int64       `json:"productId" binding:"required"`
	Quantity  int64       `json:"quantity"`
	Price     money.Money `json:"price"`
}

// OpportunityCreateReq 新建商机。阶段缺省 1 初步，允许传 1–4（进行中）；
// 赢单/输单只能经 PUT /opportunity/stage 推进进入（状态机入口唯一，见 service 层）。
// 负责人缺省当前登录用户，部门快照随负责人写入（ADR 0004）。
type OpportunityCreateReq struct {
	CustomerID        int64                    `json:"customerId" binding:"required"`
	Name              string                   `json:"name" binding:"required,max=128"`
	Stage             *int8                    `json:"stage"`
	ExpectedAmount    money.Money              `json:"expectedAmount"`
	ExpectedCloseDate string                   `json:"expectedCloseDate"`
	OwnerUserID       int64                    `json:"ownerUserId"`
	Remark            string                   `json:"remark" binding:"max=500"`
	Products          []OpportunityProductItem `json:"products"`
}

// OpportunityUpdateReq 修改商机：编号/客户/阶段不可经编辑接口改动
// （阶段推进走 PUT /opportunity/stage，入口唯一）。products 传 nil 保持行项目不变，
// 传空数组清空（编辑表单总是提交完整行项目列表）。
type OpportunityUpdateReq struct {
	ID                int64                    `json:"id" binding:"required"`
	Name              string                   `json:"name" binding:"required,max=128"`
	ExpectedAmount    money.Money              `json:"expectedAmount"`
	ExpectedCloseDate string                   `json:"expectedCloseDate"`
	Remark            string                   `json:"remark" binding:"max=500"`
	Products          []OpportunityProductItem `json:"products"`
}

// OpportunityPageReq 商机分页筛选：名称模糊、阶段、所属客户（客户详情 tab）。
// 阶段传非法值直接 400，绝不静默当成「无此筛选」。
type OpportunityPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Name       string `form:"name"`
	Stage      *int8  `form:"stage"`
	CustomerID int64  `form:"customerId"`
}

// OpportunityStageReq 阶段推进（PUT /opportunity/stage）：5 赢单 / 6 输单为终态，
// 终态后再推进返回 400（spec §4.4）。
type OpportunityStageReq struct {
	ID    int64 `json:"id" binding:"required"`
	Stage int8  `json:"stage" binding:"required"`
}
