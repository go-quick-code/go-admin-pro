package dto

// ---------- 跟进记录（issue #12 客户维度，issue #15 扩展商机维度，docs/crm-mvp-spec.md §3）----------
// 创建时挂客户或挂商机二选一（customerId XOR opportunityId，service 层校验 400）；
// 修改不漂移所属对象；可见性跟随所属对象的负责人（spec §4.1 父谓词）。

// FollowUpCreateReq 新建跟进。内容必填（≤1000 字）；类型可省
// （服务层缺省「其他」，传了必须是 1–5 枚举）；下次跟进时间可空，以字符串承载
// （RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"），服务层解析，非法格式 400
// （与租户 expireTime 同一约定：Go 的 time.Time 直绑只认 RFC3339，容不下前端 value-format）。
type FollowUpCreateReq struct {
	CustomerID     int64  `json:"customerId"`
	OpportunityID  int64  `json:"opportunityId"`
	Content        string `json:"content" binding:"required,max=1000"`
	Type           *int8  `json:"type"`
	NextFollowTime string `json:"nextFollowTime"`
}

// FollowUpUpdateReq 修改跟进：内容/类型/下次跟进时间可改，所属对象创建后不可漂移
// （无 customerId/opportunityId 字段，服务层保持原值不动）。下次跟进时间传空串即清空。
type FollowUpUpdateReq struct {
	ID             int64  `json:"id" binding:"required"`
	Content        string `json:"content" binding:"required,max=1000"`
	Type           *int8  `json:"type"`
	NextFollowTime string `json:"nextFollowTime"`
}

// FollowUpPageReq 跟进分页筛选：按所属客户或商机精确（各自详情 tab）、类型枚举，
// 两者二选一（同时传 400）。可见性经所属对象传导（join 父记录后套用父谓词），
// 本请求不接收负责人参数。
type FollowUpPageReq struct {
	PageNo        int   `form:"pageNo"`
	PageSize      int   `form:"pageSize"`
	CustomerID    int64 `form:"customerId"`
	OpportunityID int64 `form:"opportunityId"`
	Type          *int8 `form:"type"`
}
