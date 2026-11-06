package dto

// ---------- 敏感词（issue #81，spec §5.6） ----------

// SensitiveWordCreateReq 创建敏感词（当前租户）。
type SensitiveWordCreateReq struct {
	Word     string `json:"word" binding:"required,max=100"`
	Category string `json:"category" binding:"max=50"`
	Status   int8   `json:"status"`
	Remark   string `json:"remark" binding:"max=500"`
}

// SensitiveWordUpdateReq 修改敏感词；跨租户/不存在 404。
type SensitiveWordUpdateReq struct {
	ID       int64  `json:"id" binding:"required"`
	Word     string `json:"word" binding:"required,max=100"`
	Category string `json:"category" binding:"max=50"`
	Status   int8   `json:"status"`
	Remark   string `json:"remark" binding:"max=500"`
}

// SensitiveWordPageReq 敏感词分页（当前租户）；分页参数经 service 层
// web.PageQuery 归一化（与 social 等既有分页 DTO 同口径）。
type SensitiveWordPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Word     string `form:"word"`
	Category string `form:"category"`
	Status   *int8  `form:"status"`
}

// SensitiveWordCheckReq 敏感词检测请求（POST /system/sensitive-word/check）。
type SensitiveWordCheckReq struct {
	Text string `json:"text" binding:"required"`
}

// SensitiveWordCheckResp 敏感词检测结果：去重后的命中词（按首次出现位置
// 排序）与打码文本（命中字符替换 *）。
type SensitiveWordCheckResp struct {
	Words  []string `json:"words"`
	Masked string   `json:"masked"`
}
