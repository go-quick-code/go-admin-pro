package model

// notify_log.go 是商户回调执行日志实体与回调域补充状态常量（issue #134，
// docs/pay-mvp-spec.md §3.3/§4.5）。表随迁移 00037 建齐（schema only），
// 模型与业务由 #134 落地（先建后用）。放独立文件避免与 #133 的 order.go /
// #135 的退款模型在并行工单下产生文本冲突。

// 商户回调任务状态补充（spec §3.3/§4.5，与 00037 迁移注释一致）：
// 等待/成功两值已定义在 order.go，这里补齐 job 的执行语义三值。
// 21/22 均继续重试（job 扫描条件 status IN (0,21,22)），第 9 次失败置 20。
const (
	NotifyTaskStatusFail        int8 = 20 // 彻底失败（第 9 次仍失败）
	NotifyTaskStatusResultFail  int8 = 21 // 请求成功但结果失败（2xx 且业务码非 0）
	NotifyTaskStatusRequestFail int8 = 22 // 请求失败（超时/非 2xx）
)

// NotifyLog 是商户回调执行日志（租户级，append-only）：job 每次执行追加一行，
// response 为响应摘要（截断 1024 rune，列 varchar(1024)，spec §3.3）；
// status 为本次结果语义（10/21/22），交易单据不提供删除（spec §4.10）。
type NotifyLog struct {
	BaseEntity
	TenantID    int64  `gorm:"not null;default:1" json:"tenantId"`
	TaskID      int64  `gorm:"not null" json:"taskId"`
	NotifyTimes int    `gorm:"not null" json:"notifyTimes"`
	Response    string `gorm:"size:1024" json:"response"`
	Status      int8   `gorm:"not null" json:"status"`
}

func (NotifyLog) TableName() string { return "pay_notify_log" }
