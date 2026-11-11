package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// wallet.go 是钱包余额账与流水账（issue #132，docs/pay-mvp-spec.md §3.4/§4.3）。
//
// 余额变动只走 service 层唯一入账入口（ApplyWalletChange）：本包不提供任何
// 直改 balance 的便捷写入路径，仓储层入账原语只供入账函数在事务内调用。
// pay_wallet 无冻结列（v1 无冻结语义，spec §3.4 差异清单）。

// 钱包归属类型（user_type，spec §3.4/§10）。
const (
	// WalletUserTypeMember 是会员钱包（member 模块立项时启用，预留）。
	WalletUserTypeMember int8 = 1
	// WalletUserTypeAdmin 是平台后台用户钱包（自助端点当前唯一落点）。
	WalletUserTypeAdmin int8 = 2
)

// 钱包流水业务类型（biz_type，spec §3.4/§4.3）：六类枚举。
const (
	WalletBizRecharge       int8 = 1 // 充值（入账 pay+bonus，total_recharge 累计）
	WalletBizRechargeRefund int8 = 2 // 充值退款（扣回，total_recharge 调减）
	WalletBizPayment        int8 = 3 // 支付（扣款，total_expense 累计）
	WalletBizPaymentRefund  int8 = 4 // 支付退款（回加，total_expense 调减）
	WalletBizUpdateBalance  int8 = 5 // 余额调整（预留枚举：v1 无管理端调余额入口，入账函数拒绝）
	WalletBizTransfer       int8 = 6 // 转账（目标钱包入账）
)

// WalletBizTypes 是全部合法 biz_type 集合（入账函数与查询过滤共用）。
var WalletBizTypes = []int8{
	WalletBizRecharge, WalletBizRechargeRefund, WalletBizPayment,
	WalletBizPaymentRefund, WalletBizUpdateBalance, WalletBizTransfer,
}

// IsWalletBizType 报告 bizType 是否为合法的钱包流水业务类型。
func IsWalletBizType(bizType int8) bool {
	for _, t := range WalletBizTypes {
		if t == bizType {
			return true
		}
	}
	return false
}

// Wallet 是钱包余额账（租户级，懒创建）：UNIQUE(tenant_id, user_id, user_type)
// 兜底并发首触；balance 只经唯一入账入口变动，total_recharge/total_expense
// 是随流水联动的非负累计（biz=1/2 充值侧、biz=3/4 支出侧）。
type Wallet struct {
	BaseEntity
	TenantID      int64       `gorm:"not null;default:1" json:"tenantId"`
	UserID        int64       `gorm:"not null" json:"userId"`
	UserType      int8        `gorm:"not null" json:"userType"`
	Balance       money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"balance"`
	TotalRecharge money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalRecharge"`
	TotalExpense  money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalExpense"`
}

func (Wallet) TableName() string { return "pay_wallet" }

// WalletTransaction 是钱包流水（租户级，append-only：只插入，不更新不删除，
// spec §4.10 资金事实）。price 正入负出；balance 是变动后的余额快照，
// 与按 id 重放的代数和一致。no 为 LS 前缀单号（pay_sequence 事务内发放）。
type WalletTransaction struct {
	BaseEntity
	TenantID int64       `gorm:"not null;default:1" json:"tenantId"`
	No       string      `gorm:"size:32;not null" json:"no"`
	WalletID int64       `gorm:"not null" json:"walletId"`
	BizType  int8        `gorm:"not null" json:"bizType"`
	BizID    int64       `gorm:"not null;default:0" json:"bizId"`
	Title    string      `gorm:"size:100;not null" json:"title"`
	Price    money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	Balance  money.Money `gorm:"type:decimal(12,2);not null" json:"balance"`
}

func (WalletTransaction) TableName() string { return "pay_wallet_transaction" }
