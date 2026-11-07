package service

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// BadRequestOwnerMissing 生成「负责人不存在」的业务错误（owner 恒有值，
// spec §4.10；文案不暴露内部实现细节，给出可定位的值）。
func BadRequestOwnerMissing(ownerUserID int64) error {
	return xerr.BadRequest(fmt.Sprintf("负责人不存在：用户 %d", ownerUserID))
}
