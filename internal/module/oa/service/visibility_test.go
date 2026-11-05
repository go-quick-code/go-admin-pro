package service

import (
	"reflect"
	"testing"
)

// 可见范围纯函数表驱动测试（docs/oa-mvp-spec.md §4.6）：
//  1. 持有 oa:<resource>:manage → 全租户（All）；
//  2. 当前用户是部门负责人 → 其部门子树内用户集合（多部门取并集）；
//  3. 否则仅本人。
//
// 谓词 fail-closed：可见集合之外的归属用户一律不可见（跨租户/越权在上游表现为 404，
// 不泄露存在性）；本人数据永远对本人可见（负责人不属于自己的部门时也不例外）。
func TestResolveVisibility(t *testing.T) {
	cases := []struct {
		name           string
		hasManage      bool
		ledSubtrees    [][]int64
		selfUserID     int64
		wantAll        bool
		wantUserIDs    []int64
		permitsUserIDs []int64 // Permits 必须为 true 的归属用户
		denyUserIDs    []int64 // Permits 必须为 false 的归属用户（含越权/跨租户归属）
	}{
		{
			name:           "持有manage权限_全租户",
			hasManage:      true,
			selfUserID:     7,
			wantAll:        true,
			permitsUserIDs: []int64{7, 8, 999},
		},
		{
			name:           "manage优先于部门负责人",
			hasManage:      true,
			ledSubtrees:    [][]int64{{10, 11}},
			selfUserID:     7,
			wantAll:        true,
			permitsUserIDs: []int64{7, 8, 999},
		},
		{
			name:           "部门负责人_单部门子树_含本人",
			ledSubtrees:    [][]int64{{10, 11, 12}},
			selfUserID:     10,
			wantUserIDs:    []int64{10, 11, 12},
			permitsUserIDs: []int64{10, 11, 12},
			denyUserIDs:    []int64{13, 99},
		},
		{
			name:           "部门负责人_多部门并集_去重升序",
			ledSubtrees:    [][]int64{{12, 10}, {15, 11, 10}},
			selfUserID:     12,
			wantUserIDs:    []int64{10, 11, 12, 15},
			permitsUserIDs: []int64{10, 11, 12, 15},
			denyUserIDs:    []int64{13},
		},
		{
			name:           "负责人不属于被负责部门_本人仍可见",
			ledSubtrees:    [][]int64{{10, 11}},
			selfUserID:     99,
			wantUserIDs:    []int64{10, 11, 99},
			permitsUserIDs: []int64{10, 11, 99},
			denyUserIDs:    []int64{12},
		},
		{
			name:           "非负责人_仅本人",
			selfUserID:     7,
			wantUserIDs:    []int64{7},
			permitsUserIDs: []int64{7},
			denyUserIDs:    []int64{8, 999},
		},
		{
			name:           "无数据_空集合fail_closed",
			selfUserID:     0, // 异常上下文（无登录主体）：宁可少看
			wantUserIDs:    []int64{},
			permitsUserIDs: nil,
			denyUserIDs:    []int64{1, 7},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveVisibility(tc.hasManage, tc.ledSubtrees, tc.selfUserID)
			if got.All != tc.wantAll {
				t.Fatalf("All = %v，期望 %v", got.All, tc.wantAll)
			}
			if !tc.wantAll && !reflect.DeepEqual(got.UserIDs, tc.wantUserIDs) {
				t.Fatalf("UserIDs = %v，期望 %v", got.UserIDs, tc.wantUserIDs)
			}
			for _, id := range tc.permitsUserIDs {
				if !got.Permits(id) {
					t.Fatalf("Permits(%d) = false，期望 true", id)
				}
			}
			for _, id := range tc.denyUserIDs {
				if got.Permits(id) {
					t.Fatalf("Permits(%d) = true，期望 false（范围外归属不可见）", id)
				}
			}
		})
	}
}

// ManagePermission 拼接的资源管理权限码必须与菜单种子约定一致（oa:<resource>:manage）。
func TestManagePermission(t *testing.T) {
	cases := []struct{ resource, want string }{
		{"leave", "oa:leave:manage"},
		{"expense", "oa:expense:manage"},
		{"attendance", "oa:attendance:manage"},
	}
	for _, tc := range cases {
		if got := ManagePermission(tc.resource); got != tc.want {
			t.Fatalf("ManagePermission(%q) = %q，期望 %q", tc.resource, got, tc.want)
		}
	}
}

// IsVisibilityResource 只放行登记过的资源（spec §4.6：请假/报销/考勤）。
func TestIsVisibilityResource(t *testing.T) {
	for _, r := range []string{"leave", "expense", "attendance"} {
		if !IsVisibilityResource(r) {
			t.Fatalf("IsVisibilityResource(%q) = false，期望 true", r)
		}
	}
	for _, r := range []string{"", "unknown", "Leave", "oa:leave"} {
		if IsVisibilityResource(r) {
			t.Fatalf("IsVisibilityResource(%q) = true，期望 false", r)
		}
	}
}
