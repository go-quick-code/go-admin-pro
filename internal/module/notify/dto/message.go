// Package dto 是 notify 模块的传输对象（HTTP 入参/出参）。
package dto

import "github.com/go-quick-code/go-admin-pro/internal/pkg/web"

// MessagePageReq 是站内通知分页查询入参。
// 查询范围强制「本人」（service 层以当前登录用户收口，不接受 userId 入参）；
// Type/Read 为可选过滤：Read=false 仅未读、true 仅已读、nil 全部。
type MessagePageReq struct {
	web.PageQuery
	Type string `form:"type" json:"type"`
	Read *bool  `form:"read" json:"read"`
}

// UnreadCountResp 是未读数响应（顶栏铃铛 60 秒轮询消费，spec §4.13）。
type UnreadCountResp struct {
	Count int64 `json:"count"`
}
