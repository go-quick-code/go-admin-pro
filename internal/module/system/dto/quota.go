package dto

// TenantQuotaResp 是租户配额接口响应（issue #71，spec §4.2.5）：
// 返回 limit 与 used 两组值；unlimited=true 表示平台租户或未绑定套餐
// （此时各项限额值无约束语义，仅作展示）。
type TenantQuotaResp struct {
	Unlimited bool              `json:"unlimited"`
	Limit     TenantQuotaLimits `json:"limit"`
	Used      TenantQuotaUsage  `json:"used"`
}

// TenantQuotaLimits 是套餐限额（0 表示该项不限）。
type TenantQuotaLimits struct {
	MaxUsers       int64 `json:"maxUsers"`
	MaxStorageMB   int64 `json:"maxStorageMb"`
	MaxSMSMonthly  int64 `json:"maxSmsMonthly"`
	MaxMailMonthly int64 `json:"maxMailMonthly"`
}

// TenantQuotaUsage 是当前用量；短信/邮件月度用量由消息工单（#75）补齐，
// 当前恒为 0。
type TenantQuotaUsage struct {
	Users        int64 `json:"users"`
	StorageBytes int64 `json:"storageBytes"`
	SMSMonthly   int64 `json:"smsMonthly"`
	MailMonthly  int64 `json:"mailMonthly"`
}
