// product_category_tree.go 承载产品分类树的纯逻辑（issue #88）：
// 父分类合法性（自指/环）校验。树组装在前端（spec §5：GET list 全量、前端组树），
// 后端只校验；不触库，单测钉死行为；落库路径见 product_category.go 的 service 方法。
package service

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ValidateProductCategoryParent 校验父分类合法性（新建 selfID 传 0；更新传自身 ID）：
//   - parentID 0 表示挂根，直接通过；
//   - 父分类必须存在（含软删过滤后的可见列表）；
//   - 父不能是自己；沿父链上溯遇到自己也拒绝（父不能是自己的子孙）。
//
// 纯函数：调用方传入「当前租户的全部分类」。父链成环的脏数据由 guard 截断，不死循环。
func ValidateProductCategoryParent(list []model.ProductCategory, parentID, selfID int64) error {
	if parentID == 0 {
		return nil
	}
	parents := make(map[int64]int64, len(list))
	for _, row := range list {
		parents[row.ID] = row.ParentID
	}
	if _, ok := parents[parentID]; !ok {
		return xerr.BadRequest("上级分类不存在")
	}
	if parentID == selfID {
		return xerr.BadRequest("上级分类不能是自己")
	}
	for cur, guard := parentID, 0; cur != 0 && guard < 1000; guard++ {
		if cur == selfID {
			return xerr.BadRequest("上级分类不能是自己的子孙分类")
		}
		cur = parents[cur]
	}
	return nil
}
