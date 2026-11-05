package datascope_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lautung/go-admin-pro/internal/pkg/datascope"
)

// fakeResolver 用固定映射模拟「本部门及以下」的部门子树解析，纯内存、不连库。
type fakeResolver map[int64][]int64

func (f fakeResolver) DeptWithChildren(_ context.Context, deptID int64) ([]int64, error) {
	if ids, ok := f[deptID]; ok {
		return append([]int64(nil), ids...), nil
	}
	if deptID <= 0 {
		return nil, nil
	}
	return []int64{deptID}, nil
}

func TestScopeEnumValues(t *testing.T) {
	// 枚举对齐芋道：1 全部 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人。
	cases := []struct {
		name  string
		scope datascope.Scope
		want  int8
	}{
		{"全部数据", datascope.ScopeAll, 1},
		{"自定义部门", datascope.ScopeCustom, 2},
		{"本部门", datascope.ScopeDept, 3},
		{"本部门及以下", datascope.ScopeDeptAndSub, 4},
		{"仅本人", datascope.ScopeSelf, 5},
	}
	for _, tc := range cases {
		if got := int8(tc.scope); got != tc.want {
			t.Fatalf("%s = %d, 期望 %d", tc.name, got, tc.want)
		}
	}
}

func TestScopeValid(t *testing.T) {
	for s := datascope.Scope(1); s <= datascope.ScopeSelf; s++ {
		if !s.Valid() {
			t.Fatalf("Scope(%d) 应为合法值", int8(s))
		}
	}
	for _, s := range []datascope.Scope{0, 6, -1, 100} {
		if s.Valid() {
			t.Fatalf("Scope(%d) 应为非法值", int8(s))
		}
	}
}

// TestResolveSingleScopes 逐个验证 5 种数据范围在单角色下的可见谓词。
func TestResolveSingleScopes(t *testing.T) {
	// 部门树：100 总公司 → 101 研发部 → 201 后端组；102 市场部。
	resolver := fakeResolver{
		100: {100, 101, 102, 201},
		101: {101, 201},
		102: {102},
	}
	subject := datascope.Subject{UserID: 9, DeptID: 101}

	t.Run("全部数据_不限", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.ScopeAll}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !f.All {
			t.Fatalf("全部数据应得到 All=true，得到 %+v", f)
		}
		if !f.Permits(999, 42) {
			t.Fatal("全部数据应允许任意行")
		}
	})

	t.Run("自定义部门_精确集合_不含子部门", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.ScopeCustom, CustomDeptIDs: []int64{101, 102}}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if f.All || f.IncludeSelf {
			t.Fatalf("自定义部门不应 All/IncludeSelf：%+v", f)
		}
		// 自定义部门是精确集合，不自动扩展子部门：201 属于 101 的子部门但不在集合内。
		if !f.Permits(101, 42) || !f.Permits(102, 42) {
			t.Fatalf("101/102 应可见：%+v", f)
		}
		if f.Permits(100, 42) || f.Permits(201, 42) || f.Permits(0, 42) {
			t.Fatalf("集合外部门不可见：%+v", f)
		}
		if !reflect.DeepEqual(f.DeptIDs, []int64{101, 102}) {
			t.Fatalf("DeptIDs 应为升序精确集合，得到 %v", f.DeptIDs)
		}
	})

	t.Run("本部门_仅自身部门", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.ScopeDept}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.DeptIDs, []int64{101}) {
			t.Fatalf("本部门应只含 101，得到 %v", f.DeptIDs)
		}
		if !f.Permits(101, 1) || f.Permits(102, 1) || f.Permits(201, 1) {
			t.Fatalf("本部门不应含子部门：%+v", f)
		}
	})

	t.Run("本部门及以下_含子部门", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.ScopeDeptAndSub}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.DeptIDs, []int64{101, 201}) {
			t.Fatalf("本部门及以下应为 {101,201}，得到 %v", f.DeptIDs)
		}
		if f.Permits(100, 1) {
			t.Fatalf("上级部门不可见：%+v", f)
		}
	})

	t.Run("仅本人_用用户ID", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.ScopeSelf}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if f.All || len(f.DeptIDs) != 0 || !f.IncludeSelf {
			t.Fatalf("仅本人应为 IncludeSelf 且无部门集合：%+v", f)
		}
		if !f.Permits(0, 9) || !f.Permits(101, 9) {
			t.Fatal("本人行应可见")
		}
		if f.Permits(101, 8) {
			t.Fatal("他人行不可见")
		}
	})
}

// TestResolveMultiRoleUnion 多角色取可见部门并集；任一角色「全部」则整体全可见。
func TestResolveMultiRoleUnion(t *testing.T) {
	resolver := fakeResolver{
		100: {100, 101, 102, 201},
		101: {101, 201},
		102: {102},
	}
	subject := datascope.Subject{UserID: 9, DeptID: 102}

	t.Run("部门角色并集", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject, []datascope.RoleInput{
			{Scope: datascope.ScopeCustom, CustomDeptIDs: []int64{201}},
			{Scope: datascope.ScopeDept}, // 本部门 102
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.DeptIDs, []int64{102, 201}) {
			t.Fatalf("并集应为 {102,201}，得到 %v", f.DeptIDs)
		}
		if !f.Permits(201, 1) || !f.Permits(102, 1) || f.Permits(101, 1) {
			t.Fatalf("并集语义不符：%+v", f)
		}
	})

	t.Run("部门角色与仅本人叠加", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject, []datascope.RoleInput{
			{Scope: datascope.ScopeCustom, CustomDeptIDs: []int64{101}},
			{Scope: datascope.ScopeSelf},
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !f.IncludeSelf || !reflect.DeepEqual(f.DeptIDs, []int64{101}) {
			t.Fatalf("应为部门集合 + 本人：%+v", f)
		}
		if !f.Permits(101, 7) || !f.Permits(999, 9) {
			t.Fatalf("部门行与本人行都应可见：%+v", f)
		}
		if f.Permits(102, 7) {
			t.Fatalf("非本人且部门外不可见：%+v", f)
		}
	})

	t.Run("任一角色全部则短路", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject, []datascope.RoleInput{
			{Scope: datascope.ScopeSelf},
			{Scope: datascope.ScopeAll},
			{Scope: datascope.ScopeDept},
		}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if !f.All || !f.Permits(999, 42) {
			t.Fatalf("含「全部」角色时应全可见：%+v", f)
		}
	})

	t.Run("无角色回退仅本人", func(t *testing.T) {
		f, err := datascope.Resolve(context.Background(), subject, nil, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if f.All || len(f.DeptIDs) != 0 || !f.IncludeSelf || f.UserID != 9 {
			t.Fatalf("无角色应回退仅本人：%+v", f)
		}
	})

	t.Run("无部门用户的部门范围贡献空集", func(t *testing.T) {
		noDept := datascope.Subject{UserID: 9, DeptID: 0}
		f, err := datascope.Resolve(context.Background(), noDept,
			[]datascope.RoleInput{{Scope: datascope.ScopeDeptAndSub}}, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if f.All || len(f.DeptIDs) != 0 || f.IncludeSelf {
			t.Fatalf("无部门用户应什么都看不到（fail-closed）：%+v", f)
		}
		if f.Permits(101, 9) {
			t.Fatal("无部门用户不应看到任何行")
		}
	})

	t.Run("非法范围报错不回退", func(t *testing.T) {
		_, err := datascope.Resolve(context.Background(), subject,
			[]datascope.RoleInput{{Scope: datascope.Scope(9)}}, resolver)
		if err == nil {
			t.Fatal("非法数据范围应返回错误，绝不回退「全部」")
		}
	})
}
