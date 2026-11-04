package dto

// ---------- 联系人（issue #9，docs/crm-mvp-spec.md §5）----------

// ContactCreateReq 在指定客户下新建联系人。手机/邮箱选填，填了必须合法格式
// （service 层校验，400）；gender 缺省 0（未知）。
type ContactCreateReq struct {
	CustomerID   int64  `json:"customerId" binding:"required"`
	Name         string `json:"name" binding:"required,max=128"`
	Position     string `json:"position" binding:"max=64"`
	Mobile       string `json:"mobile" binding:"max=32"`
	Email        string `json:"email" binding:"max=128"`
	Gender       *int8  `json:"gender"`
	DecisionRole string `json:"decisionRole" binding:"max=64"`
	Remark       string `json:"remark" binding:"max=500"`
}

// ContactUpdateReq 修改联系人。所属客户创建后不可漂移（无 customerId 字段，
// 服务层保持原值不动）。
type ContactUpdateReq struct {
	ID           int64  `json:"id" binding:"required"`
	Name         string `json:"name" binding:"required,max=128"`
	Position     string `json:"position" binding:"max=64"`
	Mobile       string `json:"mobile" binding:"max=32"`
	Email        string `json:"email" binding:"max=128"`
	Gender       *int8  `json:"gender"`
	DecisionRole string `json:"decisionRole" binding:"max=64"`
	Remark       string `json:"remark" binding:"max=500"`
}

// ContactPageReq 联系人分页筛选：按所属客户精确（客户详情 tab）、姓名/手机模糊。
// 可见性经所属客户传导（join 父记录后套用父谓词），本请求不接收负责人参数。
type ContactPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	CustomerID int64  `form:"customerId"`
	Name       string `form:"name"`
	Mobile     string `form:"mobile"`
}
