package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// scope_row.go 承载 ERP 单行可见性校验（issue #88，spec §4.10）：
// 详情/修改/删除与列表可见性保持一致——数据范围外一律 404，不泄露存在性
//（与 crm findReadableCustomer 同语义；owner = 创建人，ADR 0004）。
// 内存判定用 datascope.Filter.Permits（谓词引擎为内存过滤预留的入口），
// 与 repository.applyOwnerScope 的 SQL 谓词同一套语义（部门 IN / 本人 / 全部）。

// checkRowVisible 校验单行对当前用户可见；不可见返回 404（entity 用于错误文案）。
func (s *Service) checkRowVisible(ctx context.Context, entity string, ownerUserID, ownerDeptID int64) error {
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return err
	}
	if !scope.Permits(ownerDeptID, ownerUserID) {
		return xerr.NotFound(entity + "不存在")
	}
	return nil
}
