package service

import (
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/system/model"
)

func TestBuildMenuTree(t *testing.T) {
	list := []model.Menu{
		{BaseEntity: model.BaseEntity{ID: 1}, Name: "系统管理", ParentID: 0, Sort: 1},
		{BaseEntity: model.BaseEntity{ID: 2}, Name: "用户管理", ParentID: 1, Sort: 1},
		{BaseEntity: model.BaseEntity{ID: 3}, Name: "角色管理", ParentID: 1, Sort: 2},
		{BaseEntity: model.BaseEntity{ID: 4}, Name: "用户新增", ParentID: 2, Sort: 1},
	}
	tree := BuildMenuTree(list)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(tree[0].Children))
	}
	if len(tree[0].Children[0].Children) != 1 {
		t.Fatalf("expected nested button, got %d", len(tree[0].Children[0].Children))
	}
}

func TestBuildDeptTree_OrphanBecomesRoot(t *testing.T) {
	list := []model.Dept{
		{BaseEntity: model.BaseEntity{ID: 1}, Name: "总公司", ParentID: 0},
		{BaseEntity: model.BaseEntity{ID: 2}, Name: "研发部", ParentID: 1},
		{BaseEntity: model.BaseEntity{ID: 3}, Name: "孤儿部门", ParentID: 999},
	}
	tree := BuildDeptTree(list)
	if len(tree) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tree))
	}
}
