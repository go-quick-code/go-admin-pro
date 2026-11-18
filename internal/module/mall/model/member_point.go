package model

// member_point.go 是积分配置与流水实体（issue #174，docs/mall-mvp-spec.md §3.1）。
//
// 表结构一次建全（迁移 00043/00044，T1），业务逻辑随 T2——T1 无任何写入
// 路径，两实体暂无读写方（repository/service 均不触碰），定义落在此处只为
// 与迁移 DDL 保持单一映射、T2 接线时不翻工：
//   - member_point_config 是租户级单行配置（UNIQUE(tenant_id)）：
//     point_per_yuan = 1 元实付入账 N 积分；行缺失时业务层按默认值 1 执行。
//   - member_point_record 是 append-only 流水（照 pay_wallet_transaction）：
//     point 正入负出、balance_after 快照结存（可重放代数和一致）；余额
//     只经 T2 唯一入账入口变动（事务内行锁读余额 + 非负 SQL 双保险），
//     member_user.point_balance 不得直改。

// MemberPointConfig 是积分配置（租户级单行）。
type MemberPointConfig struct {
	BaseEntity
	TenantID     int64  `gorm:"not null;default:1" json:"tenantId"`
	PointPerYuan int    `gorm:"not null;default:1" json:"pointPerYuan"`
	Remark       string `gorm:"size:500" json:"remark"`
}

func (MemberPointConfig) TableName() string { return "member_point_config" }

// MemberPointRecord 是积分流水（租户级，append-only：只插入，不更新不删除）。
type MemberPointRecord struct {
	BaseEntity
	TenantID int64  `gorm:"not null;default:1" json:"tenantId"`
	UserID   int64  `gorm:"not null" json:"userId"`
	BizType  string `gorm:"size:32;not null" json:"bizType"`
	BizID    int64  `gorm:"not null;default:0" json:"bizId"`
	Point    int    `gorm:"not null" json:"point"`
	// BalanceAfter 是变动后的余额快照（与按 id 重放的代数和一致）。
	BalanceAfter int    `gorm:"not null" json:"balanceAfter"`
	Remark       string `gorm:"size:500" json:"remark"`
}

func (MemberPointRecord) TableName() string { return "member_point_record" }
