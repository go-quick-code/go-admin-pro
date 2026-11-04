package service_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 产品分类树纯函数单测（issue #11）：扁平列表 → 树的组装与父分类合法性校验。
// 树组装与环检测是纯逻辑，单测在此钉死行为；落库路径由 HTTP 集成测试覆盖。

// catRow 构造测试用分类行（ID 走赋值：BaseEntity.ID 是提升字段，不能进字面量）。
func catRow(id, parentID int64, sort int) model.ProductCategory {
	row := model.ProductCategory{ParentID: parentID, Sort: sort, Name: "c"}
	row.ID = id
	return row
}

// rootIDs 返回树根节点 ID 序列，供断言。
func rootIDs(tree []*model.ProductCategory) []int64 {
	out := make([]int64, 0, len(tree))
	for _, n := range tree {
		out = append(out, n.ID)
	}
	return out
}

// childIDs 返回某节点的子节点 ID 序列，供断言。
func childIDs(n *model.ProductCategory) []int64 {
	out := make([]int64, 0, len(n.Children))
	for _, c := range n.Children {
		out = append(out, c.ID)
	}
	return out
}

func TestBuildProductCategoryTree_NestsAndSorts(t *testing.T) {
	// 输入顺序刻意打乱：函数必须自行按 (sort, id) 排序，不依赖 SQL 的 ORDER BY。
	list := []model.ProductCategory{
		catRow(1, 0, 1),
		catRow(2, 1, 2),
		catRow(3, 1, 1),
		catRow(4, 0, 0),
	}

	tree := service.BuildProductCategoryTree(list)
	if got := rootIDs(tree); len(got) != 2 || got[0] != 4 || got[1] != 1 {
		t.Fatalf("根应为 [4 1]（sort 0 < 1），实际 %v", got)
	}
	// 分类 1 的子分类按 sort 升序：3(sort 1) 在 2(sort 2) 前。
	if got := childIDs(tree[1]); len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("分类 1 的子分类应为 [3 2]（sort 升序），实际 %v", got)
	}
	// sort 相同时按 id 升序，保证确定性。
	list2 := []model.ProductCategory{catRow(2, 0, 5), catRow(1, 0, 5)}
	tree2 := service.BuildProductCategoryTree(list2)
	if got := rootIDs(tree2); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("sort 相同应按 id 升序，实际 %v", got)
	}
}

func TestBuildProductCategoryTree_OrphanBecomesRoot(t *testing.T) {
	// 父分类不存在（已删/脏数据）的孤儿不丢失，降级为根节点——与部门树同口径。
	list := []model.ProductCategory{catRow(1, 99, 0), catRow(2, 0, 1)}
	tree := service.BuildProductCategoryTree(list)
	if got := rootIDs(tree); len(got) != 2 {
		t.Fatalf("孤儿应降级为根，根=%v", got)
	}
	for _, n := range tree {
		if n.ID == 1 && len(n.Children) != 0 {
			t.Fatalf("孤儿不应有子节点")
		}
	}
}

func TestBuildProductCategoryTree_Empty(t *testing.T) {
	tree := service.BuildProductCategoryTree(nil)
	if tree == nil || len(tree) != 0 {
		t.Fatalf("空输入应返回空切片（JSON 序列化为 [] 而非 null），实际 %#v", tree)
	}
}

func TestValidateProductCategoryParent(t *testing.T) {
	existing := []model.ProductCategory{
		catRow(1, 0, 0),
		catRow(2, 1, 0),
		catRow(3, 2, 0),
	}

	cases := []struct {
		name      string
		parentID  int64
		selfID    int64
		wantError string // 空串表示应通过
	}{
		{"根分类", 0, 0, ""},
		{"挂到既有分类下", 1, 0, ""},
		{"更新不改父", 1, 2, ""},
		{"父分类不存在", 99, 0, "上级分类不存在"},
		{"父不能是自己", 5, 5, "上级分类不能是自己"},
		{"父不能是自己的子孙", 3, 1, "上级分类不能是自己的子孙分类"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := existing
			if tc.selfID == 5 {
				// self=5 是一条挂在 1 下的既有行：模拟「把 5 改到自己的子孙下」的更新路径。
				list = append(append([]model.ProductCategory{}, existing...), catRow(5, 1, 0))
			}
			err := service.ValidateProductCategoryParent(list, tc.parentID, tc.selfID)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("应通过，实际报错 %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("错误文案应含 %q，实际 %v", tc.wantError, err)
			}
		})
	}
}

func TestValidateProductCategoryParent_CorruptedChainTerminates(t *testing.T) {
	// 脏数据成环（1→2→1）时不得死循环：从父链向上走必须终止。
	list := []model.ProductCategory{catRow(1, 2, 0), catRow(2, 1, 0)}
	if err := service.ValidateProductCategoryParent(list, 2, 3); err != nil {
		t.Fatalf("父链成环但 self 不在链上，应通过校验（由数据修复处理），实际报错 %v", err)
	}
}
