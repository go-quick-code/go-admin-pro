// Package push 是实时推送的基础设施（issue #78，spec §4.5 / ADR 0013）：
// 连接 hub、一次性票据与统一事件模型。传输层（SSE/WebSocket）在 notify 模块
// 的 HTTP 入口上，经 hub 收发。
//
// 单实例限制：hub 与票据都只存进程内，多实例部署时其他实例的连接收不到
// 事件，需要外部 pub/sub（Redis/NATS）——本轮不实现，见 ADR 0013。
package push

import "time"

// EventTypeNotification 是站内通知事件的 type（spec §4.5.4）。
const EventTypeNotification = "notification"

// EventData 是通知事件的载荷。字段与 notify 模块的站内通知一致：
// 前端收到后刷新未读数并可按 Route + RouteParams 跳转。
type EventData struct {
	Title       string `json:"title,omitempty"`
	Content     string `json:"content,omitempty"`
	Type        string `json:"type,omitempty"`
	BizType     string `json:"bizType,omitempty"`
	BizID       int64  `json:"bizId,omitempty"`
	Route       string `json:"route,omitempty"`
	RouteParams string `json:"routeParams,omitempty"`
}

// Event 是推送到客户端的统一事件信封：
//
//	{"type":"notification","data":{...},"ts":1730000000}
type Event struct {
	Type string    `json:"type"`
	Data EventData `json:"data"`
	Ts   int64     `json:"ts"`
}

// NewNotification 构造站内通知事件，Ts 取当前 unix 秒。
func NewNotification(data EventData) Event {
	return Event{Type: EventTypeNotification, Data: data, Ts: time.Now().Unix()}
}
