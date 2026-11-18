package service_test

import (
	"strings"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// product_rules_test.go 是商品域校验/树组装规则的纯函数表驱动测试（外部测试
// 包，照 member_rules_test 形状；issue #176，docs/mall-mvp-spec.md §3.2/§4）。
// 唯一冲突（同级 name/租户内 name 撞键 409）与租户隔离由 HTTP 集成测试覆盖，
// 这里只锁纯函数语义：非法值一律 400，删除约束 409，树组装孤儿降级。

func wantStatus(t *testing.T, err error, wantCode int, wantMsg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望报错含 %q，实际 nil", wantMsg)
	}
	if !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("报错 %q 应含 %q", err.Error(), wantMsg)
	}
	e, ok := xerr.As(err)
	if !ok || e.Status != wantCode {
		t.Fatalf("业务错误码应为 %d，实际 %v", wantCode, err)
	}
}

// TestValidateCategoryParent 两级树父级合法性表：0 挂根直接通过；父必须存在、
// 必须是根级；不能自指；挂子级的根不可降级。
func TestValidateCategoryParent(t *testing.T) {
	// 租户内两级树样本：1(根) → 11(子)；2(根) → 21(子)。
	list := []mallmodel.ProductCategory{
		{BaseEntity: mallmodel.BaseEntity{ID: 1}, ParentID: 0, Name: "数码"},
		{BaseEntity: mallmodel.BaseEntity{ID: 11}, ParentID: 1, Name: "手机"},
		{BaseEntity: mallmodel.BaseEntity{ID: 2}, ParentID: 0, Name: "服饰"},
		{BaseEntity: mallmodel.BaseEntity{ID: 21}, ParentID: 2, Name: "T恤"},
	}
	cases := []struct {
		name     string
		parentID int64
		selfID   int64
		wantMsg  string
	}{
		{"挂根通过", 0, 0, ""},
		{"挂根级父通过", 1, 0, ""},
		{"父不存在", 999, 0, "上级分类不存在"},
		{"父是子分类（三级）", 11, 0, "只允许两级"},
		{"自指", 2, 2, "上级分类不能是自己"},
		{"挂子级的根降级", 1, 2, "不能移动为子分类"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateCategoryParent(list, tc.parentID, tc.selfID)
			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("不应报错: %v", err)
				}
				return
			}
			wantStatus(t, err, 400, tc.wantMsg)
		})
	}
}

// TestValidateCategoryDelete 删除约束谓词：有子级 409、被 SPU 引用 409、
// 双重引用 409（子级优先文案）、干净可删。
func TestValidateCategoryDelete(t *testing.T) {
	if err := mallservice.ValidateCategoryDelete(mallservice.CategoryDeleteGuard{}); err != nil {
		t.Fatalf("干净分类可删: %v", err)
	}
	wantStatus(t, mallservice.ValidateCategoryDelete(mallservice.CategoryDeleteGuard{ChildrenCount: 2}), 409, "子分类")
	wantStatus(t, mallservice.ValidateCategoryDelete(mallservice.CategoryDeleteGuard{SPUCount: 1}), 409, "SPU")
	wantStatus(t,
		mallservice.ValidateCategoryDelete(mallservice.CategoryDeleteGuard{ChildrenCount: 1, SPUCount: 3}),
		409, "子分类")
}

// TestBuildProductCategoryTree 树组装：两级形态 + (sort, id) 稳定排序 +
// 孤儿（父缺失）降级为根 + 空输入返回空切片。
func TestBuildProductCategoryTree(t *testing.T) {
	if got := mallservice.BuildProductCategoryTree(nil); len(got) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %v", got)
	}
	list := []mallmodel.ProductCategory{
		{BaseEntity: mallmodel.BaseEntity{ID: 30}, ParentID: 0, Name: "服饰", Sort: 2},
		{BaseEntity: mallmodel.BaseEntity{ID: 10}, ParentID: 0, Name: "数码", Sort: 1},
		{BaseEntity: mallmodel.BaseEntity{ID: 12}, ParentID: 10, Name: "手机", Sort: 2},
		{BaseEntity: mallmodel.BaseEntity{ID: 11}, ParentID: 10, Name: "电脑", Sort: 1},
		{BaseEntity: mallmodel.BaseEntity{ID: 99}, ParentID: 777, Name: "孤儿", Sort: 0},
	}
	tree := mallservice.BuildProductCategoryTree(list)
	if len(tree) != 3 {
		t.Fatalf("根应为 3（数码/服饰 + 孤儿降级），实际 %d：%v", len(tree), tree)
	}
	// (sort, id) 升序：孤儿 sort=0 在前，数码 sort=1、服饰 sort=2 随后。
	if tree[0].ID != 99 || tree[1].ID != 10 || tree[2].ID != 30 {
		t.Fatalf("根排序应为 sort, id 升序（99/10/30），实际 %d/%d/%d", tree[0].ID, tree[1].ID, tree[2].ID)
	}
	if len(tree[1].Children) != 2 || tree[1].Children[0].ID != 11 || tree[1].Children[1].ID != 12 {
		t.Fatalf("数码的子级应为 (sort,id) 升序 11/12：%v", tree[1].Children)
	}
	if len(tree[2].Children) != 0 || len(tree[0].Children) != 0 {
		t.Fatalf("服饰与孤儿不应有子级：%v/%v", tree[2].Children, tree[0].Children)
	}
}

// TestValidateProductNames 品牌/属性名/属性值名称校验表：必填 + ≤64 字
// （rune 口径，64 个汉字合法）。
func TestValidateProductNames(t *testing.T) {
	long64 := strings.Repeat("色", 64)
	long65 := strings.Repeat("色", 65)
	for _, tc := range []struct {
		name    string
		fn      func(string) error
		good    []string
		badMsgs map[string]string
	}{
		{"分类名", mallservice.ValidateCategoryName, []string{"手机", long64}, map[string]string{"": "不能为空", " ": "不能为空", long65: "不能超过 64 字"}},
		{"品牌名", mallservice.ValidateBrandName, []string{"Xiaomi", long64}, map[string]string{"": "不能为空", long65: "不能超过 64 字"}},
		{"属性名", mallservice.ValidatePropertyName, []string{"颜色", long64}, map[string]string{"": "不能为空", long65: "不能超过 64 字"}},
		{"属性值", mallservice.ValidatePropertyValueName, []string{"红色", long64}, map[string]string{"": "不能为空", long65: "不能超过 64 字"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, good := range tc.good {
				if err := tc.fn(good); err != nil {
					t.Fatalf("合法输入 %q 不应报错: %v", good, err)
				}
			}
			for bad, wantMsg := range tc.badMsgs {
				err := tc.fn(bad)
				if err == nil || !strings.Contains(err.Error(), wantMsg) {
					t.Fatalf("输入 %q 期望报错含 %q，实际 %v", bad, wantMsg, err)
				}
				e, ok := xerr.As(err)
				if !ok || e.Status != 400 {
					t.Fatalf("字段校验失败应为 400 业务错误，实际 %v", err)
				}
			}
		})
	}
}

// TestValidateProductStatus 品牌/分类状态枚举：0 启用 / 1 停用，其余 400。
func TestValidateProductStatus(t *testing.T) {
	for _, fn := range []func(int8) error{mallservice.ValidateCategoryStatus, mallservice.ValidateBrandStatus} {
		for _, good := range []int8{0, 1} {
			if err := fn(good); err != nil {
				t.Fatalf("状态 %d 合法不应报错: %v", good, err)
			}
		}
		if err := fn(2); err == nil {
			t.Fatalf("状态 2 应报 400")
		} else {
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("非法状态应为 400 业务错误，实际 %v", err)
			}
		}
	}
}
