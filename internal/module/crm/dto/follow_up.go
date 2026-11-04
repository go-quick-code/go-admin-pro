package dto

// ---------- 跟进记录（issue #12，docs/crm-mvp-spec.md §3 客户节/§5）----------
// 本工单只做客户维度：customerId 直连所属客户；商机/合同维度由各自工单扩展。

// FollowUpCreateReq 在指定客户下新建跟进。内容必填（≤1000 字）；类型可省
// （服务层缺省「其他」，传了必须是 1–5 枚举）；下次跟进时间可空，以字符串承载
// （RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"），服务层解析，非法格式 400
// （与租户 expireTime 同一约定：Go 的 time.Time 直绑只认 RFC3339，容不下前端 value-format）。
type FollowUpCreateReq struct {
	CustomerID     int64  `json:"customerId" binding:"required"`
	Content        string `json:"content" binding:"required,max=1000"`
	Type           *int8  `json:"type"`
	NextFollowTime string `json:"nextFollowTime"`
}

// FollowUpUpdateReq 修改跟进：内容/类型/下次跟进时间可改，所属客户创建后不可漂移
// （无 customerId 字段，服务层保持原值不动）。下次跟进时间传空串即清空。
type FollowUpUpdateReq struct {
	ID             int64  `json:"id" binding:"required"`
	Content        string `json:"content" binding:"required,max=1000"`
	Type           *int8  `json:"type"`
	NextFollowTime string `json:"nextFollowTime"`
}

// FollowUpPageReq 跟进分页筛选：按所属客户精确（客户详情 tab）、类型枚举。
// 可见性经所属客户传导（join 父记录后套用父谓词），本请求不接收负责人参数。
type FollowUpPageReq struct {
	PageNo     int   `form:"pageNo"`
	PageSize   int   `form:"pageSize"`
	CustomerID int64 `form:"customerId"`
	Type       *int8 `form:"type"`
}
