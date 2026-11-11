// Package dto 是 pay 模块的 HTTP 契约。
package dto

// ---------- 渠道（平台级） ----------

// ChannelPageReq 渠道分页筛选（平台侧）。
type ChannelPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Code     string `form:"code"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// ChannelUpdateReq 修改渠道（仅平台租户，PUT /pay/channel/:id）：
//   - status：nil 不动；0 启用 / 1 停用（渠道只启停）；
//   - config：nil 不动；非 nil 提交完整 JSON 对象，密钥键提交 ******
//     表示保留原值（三段式合并，spec §3.1）。
//   - 渠道码/名称为种子常量不可改；fee_rate 纯记录恒 0 不开放编辑。
type ChannelUpdateReq struct {
	Status *int8   `json:"status"`
	Config *string `json:"config"`
	Remark string  `json:"remark"`
}

// ChannelOption 是租户侧只读渠道清单项（GET /pay/channel/list，无凭证字段）。
type ChannelOption struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int8   `json:"status"`
}
