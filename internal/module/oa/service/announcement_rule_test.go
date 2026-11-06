// announcement_rule_test.go 以表驱动锁定公告受众与状态机规则（issue #48，
// docs/oa-mvp-spec.md §4.7/§4.14）：全员、部门子树、角色、部门∪角色四种
// 受众的可见性，以及草稿/发布/撤回的编辑、删除、发布、撤回决策。
package service

import (
	"reflect"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// TestAudiencePermits 覆盖四种受众的可见性判定：
//   - 全员：所有登录用户命中；
//   - 部门子树：查看者的祖先部门链命中任一被选部门即可见；
//   - 角色：查看者角色集合与被选角色有交集即可见；
//   - 部门∪角色：任一命中即可见，都不命中不可见。
func TestAudiencePermits(t *testing.T) {
	cases := []struct {
		name     string
		audience Audience
		viewer   AudienceViewer
		want     bool
	}{
		{
			name:     "全员_任意登录用户可见",
			audience: Audience{All: true},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{105, 102, 100}, RoleIDs: []int64{2}},
			want:     true,
		},
		{
			name:     "全员_无登录主体fail_closed",
			audience: Audience{All: true},
			viewer:   AudienceViewer{},
			want:     false,
		},
		{
			name:     "部门子树_查看者在所选部门子树内",
			audience: Audience{DeptIDs: []int64{101}},
			// 查看者部门 105，祖先链 105→101→100：101 是祖先，命中子树。
			viewer: AudienceViewer{UserID: 7, DeptIDs: []int64{105, 101, 100}, RoleIDs: []int64{2}},
			want:   true,
		},
		{
			name:     "部门子树_查看者部门自身就是所选部门",
			audience: Audience{DeptIDs: []int64{101}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{101, 100}},
			want:     true,
		},
		{
			name:     "部门子树_查看者在旁支不可见",
			audience: Audience{DeptIDs: []int64{101}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{103, 100}, RoleIDs: []int64{2}},
			want:     false,
		},
		{
			name:     "角色_持有任一被选角色可见",
			audience: Audience{RoleIDs: []int64{5, 6}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{103, 100}, RoleIDs: []int64{2, 6}},
			want:     true,
		},
		{
			name:     "角色_未持有不可见",
			audience: Audience{RoleIDs: []int64{5, 6}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{103, 100}, RoleIDs: []int64{2}},
			want:     false,
		},
		{
			name:     "部门∪角色_仅命中部门",
			audience: Audience{DeptIDs: []int64{101}, RoleIDs: []int64{5}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{105, 101, 100}, RoleIDs: []int64{2}},
			want:     true,
		},
		{
			name:     "部门∪角色_仅命中角色",
			audience: Audience{DeptIDs: []int64{101}, RoleIDs: []int64{5}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{103, 100}, RoleIDs: []int64{5}},
			want:     true,
		},
		{
			name:     "部门∪角色_两路都不命中",
			audience: Audience{DeptIDs: []int64{101}, RoleIDs: []int64{5}},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{103, 100}, RoleIDs: []int64{2}},
			want:     false,
		},
		{
			name:     "自定义受众_未选目标无人可见",
			audience: Audience{},
			viewer:   AudienceViewer{UserID: 7, DeptIDs: []int64{101, 100}, RoleIDs: []int64{2}},
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.audience.Permits(tc.viewer); got != tc.want {
				t.Fatalf("Permits() = %v, want %v（audience=%+v viewer=%+v）", got, tc.want, tc.audience, tc.viewer)
			}
		})
	}
}

// TestResolveAudienceUserIDs 锁定发布通知的收件人展开：全员取全部启用用户；
// 自定义取部门子树用户 ∪ 角色用户，升序去重并忽略非正 ID。
func TestResolveAudienceUserIDs(t *testing.T) {
	cases := []struct {
		name        string
		all         bool
		deptUsers   []int64
		roleUsers   []int64
		allUsers    []int64
		wantUserIDs []int64
	}{
		{
			name:        "全员_仅用全部用户集合",
			all:         true,
			deptUsers:   []int64{1, 2},
			roleUsers:   []int64{3},
			allUsers:    []int64{5, 4, 4, 0},
			wantUserIDs: []int64{4, 5},
		},
		{
			name:        "自定义_部门与角色并集去重",
			deptUsers:   []int64{3, 1, 0},
			roleUsers:   []int64{2, 1},
			allUsers:    []int64{9},
			wantUserIDs: []int64{1, 2, 3},
		},
		{
			name:        "自定义_无目标为空",
			wantUserIDs: []int64{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAudienceUserIDs(tc.all, tc.deptUsers, tc.roleUsers, tc.allUsers)
			if !reflect.DeepEqual(got, tc.wantUserIDs) {
				t.Fatalf("ResolveAudienceUserIDs() = %v, want %v", got, tc.wantUserIDs)
			}
		})
	}
}

// TestAnnouncementStatusMachine 锁定状态机决策（spec §4.7/§4.14）：
// 草稿可编辑/删除/发布；发布后不可编辑/删除、可撤回；撤回可重新发布。
func TestAnnouncementStatusMachine(t *testing.T) {
	cases := []struct {
		status      int8
		name        string
		canEdit     bool
		canDelete   bool
		canPublish  bool
		canWithdraw bool
	}{
		{model.AnnouncementStatusDraft, "草稿", true, true, true, false},
		{model.AnnouncementStatusPublished, "已发布", false, false, false, true},
		{model.AnnouncementStatusWithdrawn, "已撤回", false, false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanEditAnnouncement(tc.status); got != tc.canEdit {
				t.Fatalf("CanEdit = %v, want %v", got, tc.canEdit)
			}
			if got := CanDeleteAnnouncement(tc.status); got != tc.canDelete {
				t.Fatalf("CanDelete = %v, want %v", got, tc.canDelete)
			}
			if got := CanPublishAnnouncement(tc.status); got != tc.canPublish {
				t.Fatalf("CanPublish = %v, want %v", got, tc.canPublish)
			}
			if got := CanWithdrawAnnouncement(tc.status); got != tc.canWithdraw {
				t.Fatalf("CanWithdraw = %v, want %v", got, tc.canWithdraw)
			}
		})
	}
}
