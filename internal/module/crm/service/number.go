package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 客户编号规则（docs/crm-mvp-spec.md、issue #8）：
// KH + yyyyMMdd + 4 位序号，租户内唯一；序号由 crm_sequence 在
// (tenant_id, biz_type, date_key) 上原子递增发放（见 repository.NextSequenceValue）。

// CustomerNoPrefix 是客户编号前缀。
const CustomerNoPrefix = "KH"

// BizTypeCustomer 是客户编号在 crm_sequence 中的业务类型。
const BizTypeCustomer = "customer"

// DateKey 返回编号的日期键 yyyyMMdd。按调用方 Location 解释（ADR 0006）。
func DateKey(t time.Time) string {
	return t.Format("20060102")
}

// FormatCustomerNo 把日期键与序号格式化为客户编号：KH + yyyyMMdd + 至少 4 位序号。
// 序号超过 9999 时自然扩位：唯一性优先于固定位数，绝不截断。
func FormatCustomerNo(dateKey string, seq int64) string {
	return CustomerNoPrefix + dateKey + fmt.Sprintf("%04d", seq)
}

// ValidateCustomerStatus 校验客户状态枚举，非法值返回用户可读的 400 错误。
func ValidateCustomerStatus(status int8) error {
	if !model.ValidCustomerStatus(status) {
		return xerr.BadRequest(fmt.Sprintf("客户状态 %d 不合法：期望 1 潜在 / 2 活跃 / 3 流失", status))
	}
	return nil
}
