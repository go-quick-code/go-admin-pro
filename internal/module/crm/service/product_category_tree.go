// product_category_tree.go 承载产品分类树的纯逻辑（issue #11）：
// 扁平列表组装、父分类合法性（自指/环）校验。不触库，单测钉死行为；
// 落库路径见 product_category.go 的 service 方法。
package service

import (
	"sort"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// BuildProductCategoryTree 把扁平分类列表组装成树（system 的 BuildDeptTree 同款）：
//   - 根与各层子节点均按 (sort, id) 升序排好，不依赖调用方的 ORDER BY；
//   - 父分类不存在（已删/脏数据）的孤儿降级为根节点，不丢数据；
//   - 空输入返回空切片（JSON 序列化为 [] 而非 null）。
func BuildProductCategoryTree(list []model.ProductCategory) []*model.ProductCategory {
	nodes := make(map[int64]*model.ProductCategory, len(list))
	for i := range list {
		row := list[i]
		row.Children = nil
		nodes[row.ID] = &row
	}
	roots := make([]*model.ProductCategory, 0)
	for i := range list {
		node := nodes[list[i].ID]
		if list[i].ParentID != 0 {
			if parent, ok := nodes[list[i].ParentID]; ok {
				parent.Children = append(parent.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	sortProductCategories(roots)
	for _, node := range nodes {
		sortProductCategories(node.Children)
	}
	return roots
}

// sortProductCategories 按 (sort, id) 升序原地排序，保证同一份数据永远输出同一棵树。
func sortProductCategories(list []*model.ProductCategory) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Sort != list[j].Sort {
			return list[i].Sort < list[j].Sort
		}
		return list[i].ID < list[j].ID
	})
}

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
