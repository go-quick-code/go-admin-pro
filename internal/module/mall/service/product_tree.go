package service

import (
	"sort"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
)

// product_tree.go 承载商品分类树的纯组装逻辑（issue #176）：
// 扁平列表 → 两级树。不触库，单测钉死行为；落库路径见 product.go 的
// service 方法，父级合法性校验纯函数见 product_rules.go。

// BuildProductCategoryTree 把扁平分类列表组装成树（crm 的
// BuildProductCategoryTree 同款）：
//   - 根与子节点均按 (sort, id) 升序排好，不依赖调用方的 ORDER BY；
//   - 父分类不存在（已删/脏数据）的孤儿降级为根节点，不丢数据——两级树
//     口径下父缺失即视为根；
//   - 空输入返回空切片（JSON 序列化为 [] 而非 null）。
func BuildProductCategoryTree(list []mallmodel.ProductCategory) []*mallmodel.ProductCategory {
	nodes := make(map[int64]*mallmodel.ProductCategory, len(list))
	for i := range list {
		row := list[i]
		row.Children = nil
		nodes[row.ID] = &row
	}
	roots := make([]*mallmodel.ProductCategory, 0)
	for i := range list {
		node := nodes[list[i].ID]
		if list[i].ParentID != mallmodel.ProductCategoryRootID {
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
func sortProductCategories(list []*mallmodel.ProductCategory) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Sort != list[j].Sort {
			return list[i].Sort < list[j].Sort
		}
		return list[i].ID < list[j].ID
	})
}
