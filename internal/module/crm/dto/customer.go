package dto

// ---------- 客户（issue #8，docs/crm-mvp-spec.md §5）----------

// CustomerCreateReq 新建客户。customerNo 由服务端发放，客户端不可指定。
// status 缺省 1（潜在）；ownerUserId 缺省当前登录用户（负责人快照随写落库）。
type CustomerCreateReq struct {
	Name        string `json:"name" binding:"required,max=128"`
	Industry    string `json:"industry" binding:"max=64"`
	Level       string `json:"level" binding:"max=64"`
	Source      string `json:"source" binding:"max=64"`
	Phone       string `json:"phone" binding:"max=32"`
	Website     string `json:"website" binding:"max=128"`
	Address     string `json:"address" binding:"max=255"`
	Status      *int8  `json:"status"`
	OwnerUserID int64  `json:"ownerUserId"`
	Remark      string `json:"remark" binding:"max=500"`
}

// CustomerUpdateReq 修改客户。ownerUserId 传 0 表示保持现负责人不变。
type CustomerUpdateReq struct {
	ID          int64  `json:"id" binding:"required"`
	Name        string `json:"name" binding:"required,max=128"`
	Industry    string `json:"industry" binding:"max=64"`
	Level       string `json:"level" binding:"max=64"`
	Source      string `json:"source" binding:"max=64"`
	Phone       string `json:"phone" binding:"max=32"`
	Website     string `json:"website" binding:"max=128"`
	Address     string `json:"address" binding:"max=255"`
	Status      *int8  `json:"status"`
	OwnerUserID int64  `json:"ownerUserId"`
	Remark      string `json:"remark" binding:"max=500"`
}

// CustomerPageReq 客户分页筛选：名称模糊、负责人/部门精确、状态枚举。
// status 传非法枚举直接 400，绝不静默当成「无此筛选」。
type CustomerPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	Name        string `form:"name"`
	Status      *int8  `form:"status"`
	OwnerUserID int64  `form:"ownerUserId"`
	OwnerDeptID int64  `form:"ownerDeptId"`
}
