package service

import (
	"reflect"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

func TestExpandPackageMenuIDs(t *testing.T) {
	menus := []model.Menu{
		{BaseEntity: model.BaseEntity{ID: 100}, Type: model.MenuTypeDir, Status: model.StatusEnable},
		{BaseEntity: model.BaseEntity{ID: 101}, Type: model.MenuTypeMenu, ParentID: 100, Status: model.StatusEnable},
		{BaseEntity: model.BaseEntity{ID: 102}, Type: model.MenuTypeMenu, ParentID: 100, Status: model.StatusDisable},
		{BaseEntity: model.BaseEntity{ID: 1011}, Type: model.MenuTypeButton, ParentID: 101, Status: model.StatusEnable},
		{BaseEntity: model.BaseEntity{ID: 1012}, Type: model.MenuTypeButton, ParentID: 101, Status: model.StatusDisable},
		{BaseEntity: model.BaseEntity{ID: 1013}, Type: model.MenuTypeButton, ParentID: 101, Status: model.StatusEnable},
	}

	got := expandPackageMenuIDs(menus, model.Int64List{100, 101})
	want := []int64{100, 101, 1011, 1013}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("展开结果不符：want %+v got %+v", want, got)
	}

	// 只选目录：不能隐式带出子菜单，只带目录本身。
	got = expandPackageMenuIDs(menus, model.Int64List{100})
	if !reflect.DeepEqual(got, []int64{100}) {
		t.Fatalf("只选目录不应带出子菜单，实际 %+v", got)
	}

	// 停用菜单不授予。
	got = expandPackageMenuIDs(menus, model.Int64List{102})
	if len(got) != 0 {
		t.Fatalf("停用菜单不应授予，实际 %+v", got)
	}
}
