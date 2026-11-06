package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	systemmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// fakeSystemGateway 是 SystemGateway 的最小实现：记录调用并按预设返回，
// 用于验证可见范围取数编排（不触库；带真库的链路由 HTTP 集成测试覆盖）。
type fakeSystemGateway struct {
	hasManage map[string]bool   // permission -> 是否持有
	ledDepts  map[int64][]int64 // userID -> 负责的部门
	children  map[int64][]int64 // deptID -> 部门子树
	deptUsers map[int64][]int64 // deptID -> 部门内用户
	childrenQ []int64           // DeptWithChildren 调用记录
	usersQ    [][]int64         // ListUserIDsByDeptIDs 调用记录
}

func (f *fakeSystemGateway) UserHasPermission(_ context.Context, _ int64, permission string) (bool, error) {
	return f.hasManage[permission], nil
}

func (f *fakeSystemGateway) ListDeptIDsByLeader(_ context.Context, leaderUserID int64) ([]int64, error) {
	return f.ledDepts[leaderUserID], nil
}

func (f *fakeSystemGateway) DeptWithChildren(_ context.Context, deptID int64) ([]int64, error) {
	f.childrenQ = append(f.childrenQ, deptID)
	return f.children[deptID], nil
}

func (f *fakeSystemGateway) ListUserIDsByDeptIDs(_ context.Context, deptIDs []int64) ([]int64, error) {
	f.usersQ = append(f.usersQ, append([]int64(nil), deptIDs...))
	seen := map[int64]struct{}{}
	var ids []int64
	for _, d := range deptIDs {
		for _, u := range f.deptUsers[d] {
			if _, ok := seen[u]; !ok {
				seen[u] = struct{}{}
				ids = append(ids, u)
			}
		}
	}
	return ids, nil
}

// ListSimpleUsers 返回空启用用户集（可见范围编排不消费，issue #60 仅为满足接口）。
func (f *fakeSystemGateway) ListSimpleUsers(_ context.Context) ([]systemmodel.SimpleUser, error) {
	return nil, nil
}

// UserNicknames 返回固定昵称映射（额度管理页 #51 与考勤组员列表 #60 展示用）。
func (f *fakeSystemGateway) UserNicknames(_ context.Context, userIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(userIDs))
	for _, id := range userIDs {
		out[id] = fmt.Sprintf("用户%d", id)
	}
	return out, nil
}

// 以下方法服务于公告受众（issue #48）：可见范围单测不触达，返回零值即可。
func (f *fakeSystemGateway) UserDeptID(_ context.Context, _ int64) (int64, bool, error) {
	return 0, false, nil
}

func (f *fakeSystemGateway) DeptParentID(_ context.Context, _ int64) (int64, bool, error) {
	return 0, false, nil
}

func (f *fakeSystemGateway) UserRoleIDs(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}

func (f *fakeSystemGateway) ListActiveUserIDsByRole(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}

func (f *fakeSystemGateway) ListActiveUserIDs(_ context.Context) ([]int64, error) {
	return nil, nil
}

// ListEnabledTenantIDs 服务于考勤日结果任务的租户迭代（issue #61）：
// 可见范围单测不触达，返回零值即可。
func (f *fakeSystemGateway) ListEnabledTenantIDs(_ context.Context) ([]int64, error) {
	return nil, nil
}

func (f *fakeSystemGateway) DeptNames(_ context.Context, _ []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}

func (f *fakeSystemGateway) RoleNames(_ context.Context, _ []int64) (map[int64]string, error) {
	return map[int64]string{}, nil
}

// userContext 返回带登录主体（用户 7，租户 1）的上下文。
func userContext() context.Context {
	return authn.With(context.Background(), authn.Principal{UserID: 7, TenantID: 1})
}

// TestVisibilityScopeOfOrchestration 验证取数编排：manage 短路不查部门；
// 负责人按部门逐个解析子树与子树用户；无命中仅本人；非法资源 400。
func TestVisibilityScopeOfOrchestration(t *testing.T) {
	cases := []struct {
		name        string
		ctx         context.Context
		resource    string
		gateway     *fakeSystemGateway
		wantAll     bool
		wantUserIDs []int64
		wantErr     bool
	}{
		{
			name:     "manage_短路_不查部门",
			ctx:      userContext(),
			resource: "leave",
			gateway: &fakeSystemGateway{
				hasManage: map[string]bool{"oa:leave:manage": true},
			},
			wantAll: true,
		},
		{
			name:     "负责人_多部门_子树用户并集",
			ctx:      userContext(),
			resource: "expense",
			gateway: &fakeSystemGateway{
				ledDepts:  map[int64][]int64{7: {102, 103}},
				children:  map[int64][]int64{102: {102, 105}, 103: {103}},
				deptUsers: map[int64][]int64{102: {10, 11}, 105: {12}, 103: {13}},
			},
			wantUserIDs: []int64{7, 10, 11, 12, 13},
		},
		{
			name:     "非负责人_仅本人",
			ctx:      userContext(),
			resource: "attendance",
			gateway:  &fakeSystemGateway{},
			// 部门 105 内没有用户 7 也没有负责人关系：仅本人
			wantUserIDs: []int64{7},
		},
		{
			name:     "负责人_子树内无用户_仍有本人",
			ctx:      userContext(),
			resource: "leave",
			gateway: &fakeSystemGateway{
				ledDepts:  map[int64][]int64{7: {102}},
				children:  map[int64][]int64{102: {102}},
				deptUsers: map[int64][]int64{102: {}},
			},
			wantUserIDs: []int64{7},
		},
		{
			name:     "无登录主体_空集合fail_closed",
			ctx:      context.Background(),
			resource: "leave",
			gateway: &fakeSystemGateway{
				hasManage: map[string]bool{"oa:leave:manage": true}, // 即使预设也不该被查询
			},
			wantUserIDs: []int64{},
		},
		{
			name:     "非法资源_400",
			ctx:      userContext(),
			resource: "document", // 文档走目录 ACL，不经本 helper
			gateway:  &fakeSystemGateway{},
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(tc.gateway, nil, nil)
			got, err := svc.VisibilityScopeOf(tc.ctx, tc.resource)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际 got=%+v", got)
				}
				if _, ok := xerr.As(err); !ok {
					t.Fatalf("期望 xerr 错误，实际 %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got.All != tc.wantAll {
				t.Fatalf("All = %v，期望 %v", got.All, tc.wantAll)
			}
			if !tc.wantAll && !reflect.DeepEqual(got.UserIDs, tc.wantUserIDs) {
				t.Fatalf("UserIDs = %v，期望 %v", got.UserIDs, tc.wantUserIDs)
			}
		})
	}
}

// TestVisibilityScopeOfManageSkipsDeptQueries manage 命中时不取部门数据；
// 负责人路径才逐部门解析子树（取数次数可预算）。
func TestVisibilityScopeOfManageSkipsDeptQueries(t *testing.T) {
	gw := &fakeSystemGateway{hasManage: map[string]bool{"oa:leave:manage": true}}
	svc := New(gw, nil, nil)
	if _, err := svc.VisibilityScopeOf(userContext(), "leave"); err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if len(gw.childrenQ) != 0 || len(gw.usersQ) != 0 {
		t.Fatalf("manage 路径不应查询部门（children=%v users=%v）", gw.childrenQ, gw.usersQ)
	}

	gw2 := &fakeSystemGateway{ledDepts: map[int64][]int64{7: {102, 103}}}
	svc2 := New(gw2, nil, nil)
	if _, err := svc2.VisibilityScopeOf(userContext(), "leave"); err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if !reflect.DeepEqual(gw2.childrenQ, []int64{102, 103}) {
		t.Fatalf("子树查询应逐负责部门进行，实际 %v", gw2.childrenQ)
	}
}
