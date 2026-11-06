// Package quota 定义租户套餐的限额与用量中性类型。
//
// system 模块负责解释套餐，infra/message 等消费模块只依赖本包的类型与
// 自己的窄接口，避免模块之间直接 import（ADR 0012）。
package quota

// Limits 是套餐限额；0 表示该项不限，Unlimited 为 true 时全部不限。
type Limits struct {
	MaxUsers       int64 `json:"maxUsers"`
	MaxStorageMB   int64 `json:"maxStorageMb"`
	MaxSMSMonthly  int64 `json:"maxSmsMonthly"`
	MaxMailMonthly int64 `json:"maxMailMonthly"`
	Unlimited      bool  `json:"unlimited"`
}

// Usage 是租户当前用量。
type Usage struct {
	Users        int64 `json:"users"`
	StorageBytes int64 `json:"storageBytes"`
	SMSMonthly   int64 `json:"smsMonthly"`
	MailMonthly  int64 `json:"mailMonthly"`
}

// Status 是限额与用量的组合，供配额接口返回。
type Status struct {
	Limits Limits `json:"limits"`
	Usage  Usage  `json:"usage"`
}

// UnlimitedLimits 返回不限量的限额。
func UnlimitedLimits() Limits {
	return Limits{Unlimited: true}
}
