package model

// SensitiveWord 是敏感词（issue #81，spec §3.8）：租户级词库，登记
// TenantTables（internal/pkg/database/tenant_tables.go），读写经租户插件隔离，
// 跨租户业务 ID 一律 404。唯一键 (tenant_id, word, deleted_at)，软删后同一词
// 可重新添加；停用（status=1）的词不参与匹配（spec §4.7.3）。
type SensitiveWord struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// Word 是敏感词本体（去首尾空白后 1–50 字符，service 层校验）。
	Word string `gorm:"size:100;not null" json:"word"`
	// Category 是自由标注的分类（如 涉政/广告），仅检索用，不参与匹配。
	Category string `gorm:"size:50" json:"category"`
	Status   int8   `gorm:"not null;default:0" json:"status"`
	Remark   string `gorm:"size:500" json:"remark"`
}

func (SensitiveWord) TableName() string { return "system_sensitive_word" }
