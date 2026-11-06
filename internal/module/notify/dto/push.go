// Package dto 是 notify 模块的传输对象（HTTP 入参/出参）。
package dto

// PushTicketResp 是推送票据签发响应（spec §4.5.3 / §5.4）：
// 前端先带 Bearer 调本接口，再凭 ticket 建立流（EventSource 不能带
// Authorization 头，避免 JWT 进入 URL/日志，ADR 0013）。
type PushTicketResp struct {
	Ticket     string   `json:"ticket"`
	ExpiresIn  int      `json:"expiresIn"`  // 票据有效期（秒）
	Transports []string `json:"transports"` // 可用传输：本期仅 sse（ws 由 #79 追加）
}
