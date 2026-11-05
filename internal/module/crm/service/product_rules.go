package service

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 产品枚举校验（本文件由 codegen 生成（issue #10 样板）；非法值一律 400，文案说清字段与期望）----------

// ValidateProductStatus 校验产品状态枚举，非法值返回用户可读的 400 错误。
func ValidateProductStatus(v int8) error {
	if !model.ValidProductStatus(v) {
		return xerr.BadRequest(fmt.Sprintf("产品状态 %d 不合法：期望 0 下架 / 1 上架", v))
	}
	return nil
}
