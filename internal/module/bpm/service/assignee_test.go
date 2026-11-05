package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// fakeAssigneeSys 是审批人解析纯函数的 SystemGateway 内存替身：部门树/角色/
// 权限码/用户启用性全部表驱动，专供表驱动用例锁定 spec §4.2 的六规则语义。
type fakeAssigneeSys struct {
	activeUsers   map[int64]bool     // 用户 → 是否启用（缺失 = 不存在）
	roleUsers     map[int64][]int64  // 角色 → 启用用户（替身已预过滤启用性）
	permUsers     map[string][]int64 // 权限码 → 启用用户
	userDept      map[int64]int64    // 用户 → 部门（缺失 = 未归属）
	deptLeader    map[int64]int64    // 部门 → 负责人（缺失/0 = 空缺）
	deptParent    map[int64]int64    // 部门 → 父部门（缺失/0 = 根）
	deptVisitErr  map[int64]error    // 部门查询注入错误（基础设施故障传播用）
	leaderLookups map[int64]int      // 部门负责人查询次数（向上查找断言用）
}

func newFakeAssigneeSys() *fakeAssigneeSys {
	return &fakeAssigneeSys{
		activeUsers:   map[int64]bool{},
		roleUsers:     map[int64][]int64{},
		permUsers:     map[string][]int64{},
		userDept:      map[int64]int64{},
		deptLeader:    map[int64]int64{},
		deptParent:    map[int64]int64{},
		deptVisitErr:  map[int64]error{},
		leaderLookups: map[int64]int{},
	}
}

func (f *fakeAssigneeSys) ActiveUserExists(_ context.Context, userID int64) (bool, error) {
	return f.activeUsers[userID], nil
}

func (f *fakeAssigneeSys) RoleExists(_ context.Context, _ int64) (bool, error) { return true, nil }

func (f *fakeAssigneeSys) DeptExists(_ context.Context, _ int64) (bool, error) { return true, nil }

func (f *fakeAssigneeSys) UserDeptID(_ context.Context, userID int64) (int64, bool, error) {
	d, ok := f.userDept[userID]
	return d, ok && d > 0, nil
}

func (f *fakeAssigneeSys) DeptLeaderUserID(_ context.Context, deptID int64) (int64, bool, error) {
	if err := f.deptVisitErr[deptID]; err != nil {
		return 0, false, err
	}
	f.leaderLookups[deptID]++
	leader := f.deptLeader[deptID]
	return leader, leader > 0, nil
}

func (f *fakeAssigneeSys) DeptParentID(_ context.Context, deptID int64) (int64, bool, error) {
	if err := f.deptVisitErr[deptID]; err != nil {
		return 0, false, err
	}
	parent := f.deptParent[deptID]
	return parent, parent > 0, nil
}

func (f *fakeAssigneeSys) ActiveUserIDsByRole(_ context.Context, roleID int64) ([]int64, error) {
	return f.roleUsers[roleID], nil
}

func (f *fakeAssigneeSys) ActiveUserIDsByPermission(_ context.Context, permission string) ([]int64, error) {
	return f.permUsers[permission], nil
}

func (f *fakeAssigneeSys) UserHasPermission(_ context.Context, _ int64, _ string) (bool, error) {
	return false, nil
}

func (f *fakeAssigneeSys) UserNicknames(_ context.Context, userIDs []int64) (map[int64]string, error) {
	names := make(map[int64]string, len(userIDs))
	for _, id := range userIDs {
		names[id] = "用户-" + strconv.FormatInt(id, 10)
	}
	return names, nil
}

// wantErr 语义："" = 期望成功；"400" = 期望 400 业务错误。
func TestResolveAssignees_TableDriven(t *testing.T) {
	sys := newFakeAssigneeSys()
	// 用户档案：3/4/5 启用，6 停用，7 不存在。
	for _, id := range []int64{3, 4, 5} {
		sys.activeUsers[id] = true
	}
	sys.activeUsers[6] = false
	// 角色池：角色 20 → 启用用户 5、3（乱序传入，断言去重排序）；角色 21 → 空。
	sys.roleUsers[20] = []int64{5, 3, 5, 3}
	sys.roleUsers[21] = nil
	// 权限码池：crm:approve → 4、3；oa:empty → 空。
	sys.permUsers["crm:approve"] = []int64{4, 3}
	sys.permUsers["oa:empty"] = nil
	// 部门树（spec §4.2 向上查找示例）：
	//   200 销售部(负责人 3) ── 201 销售一组(负责人 4) ── 202 销售一组甲分队(空缺)
	//   300 财务部(负责人 6，停用) ── 301 核算组(空缺) ── 400 根外孤岛(空缺，父 0)
	//   210 研发部(负责人 3) ── 211 平台组(负责人 6，停用)
	sys.userDept[3] = 201
	sys.userDept[9] = 202
	sys.userDept[10] = 301
	sys.userDept[11] = 211
	sys.deptLeader[200] = 3
	sys.deptParent[200] = 0
	sys.deptLeader[201] = 4
	sys.deptParent[201] = 200
	sys.deptParent[202] = 201
	sys.deptLeader[300] = 6
	sys.deptParent[300] = 0
	sys.deptParent[301] = 300
	sys.deptParent[400] = 0
	sys.deptLeader[210] = 3
	sys.deptParent[210] = 0
	sys.deptLeader[211] = 6
	sys.deptParent[211] = 210

	cases := []struct {
		name    string
		node    model.Node
		initr   int64
		want    []int64
		wantErr string // ""=成功；"400"=400 业务错误
	}{
		{
			name: "规则user_指定启用用户解析出单人",
			node: model.Node{Name: "指定审批", AssigneeType: model.AssigneeUser, AssigneeValue: "5"},
			want: []int64{5},
		},
		{
			name:    "规则user_指定停用用户解析失败",
			node:    model.Node{Name: "指定审批", AssigneeType: model.AssigneeUser, AssigneeValue: "6"},
			wantErr: "400",
		},
		{
			name:    "规则user_指定不存在用户解析失败",
			node:    model.Node{Name: "指定审批", AssigneeType: model.AssigneeUser, AssigneeValue: "7"},
			wantErr: "400",
		},
		{
			name:    "规则user_取值非法解析失败",
			node:    model.Node{Name: "指定审批", AssigneeType: model.AssigneeUser, AssigneeValue: "abc"},
			wantErr: "400",
		},
		{
			name: "规则role_候选池去重排序",
			node: model.Node{Name: "角色审批", AssigneeType: model.AssigneeRole, AssigneeValue: "20"},
			want: []int64{3, 5},
		},
		{
			name:    "规则role_无持有人解析失败",
			node:    model.Node{Name: "角色审批", AssigneeType: model.AssigneeRole, AssigneeValue: "21"},
			wantErr: "400",
		},
		{
			name: "规则permission_候选池含全部持有人",
			node: model.Node{Name: "权限审批", AssigneeType: model.AssigneePermission, AssigneeValue: "crm:approve"},
			want: []int64{3, 4},
		},
		{
			name:    "规则permission_无持有人解析失败",
			node:    model.Node{Name: "权限审批", AssigneeType: model.AssigneePermission, AssigneeValue: "oa:empty"},
			wantErr: "400",
		},
		{
			name:  "规则initiator_自审批v1允许",
			node:  model.Node{Name: "发起人确认", AssigneeType: model.AssigneeInitiator},
			initr: 9,
			want:  []int64{9},
		},
		{
			name:  "规则initiator_dept_leader_直属部门有负责人",
			node:  model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
			initr: 3,
			want:  []int64{4},
		},
		{
			name:  "规则initiator_dept_leader_空缺沿部门树向上",
			node:  model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
			initr: 9, // 202 空缺 → 201 有负责人
			want:  []int64{4},
		},
		{
			name:    "规则initiator_dept_leader_到根仍空缺失败",
			node:    model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
			initr:   10, // 301 空缺 → 300 负责人停用 → 继续向上？300 已是根 → 失败
			wantErr: "400",
		},
		{
			name:  "规则initiator_dept_leader_停用负责人视为空缺继续向上",
			node:  model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
			initr: 11, // 211 负责人 6 停用 → 210 负责人 3 启用
			want:  []int64{3},
		},
		{
			name:    "规则initiator_dept_leader_发起人未归属部门失败",
			node:    model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
			initr:   12,
			wantErr: "400",
		},
		{
			name: "规则dept_leader_指定部门负责人",
			node: model.Node{Name: "财务审批", AssigneeType: model.AssigneeDeptLeader, DeptID: 200},
			want: []int64{3},
		},
		{
			name: "规则dept_leader_指定部门空缺向上查找",
			node: model.Node{Name: "财务审批", AssigneeType: model.AssigneeDeptLeader, DeptID: 202},
			want: []int64{4},
		},
		{
			name:    "规则dept_leader_指定部门到根空缺失败",
			node:    model.Node{Name: "财务审批", AssigneeType: model.AssigneeDeptLeader, DeptID: 400},
			wantErr: "400",
		},
		{
			name:    "未知规则类型解析失败",
			node:    model.Node{Name: "怪节点", AssigneeType: "magic"},
			wantErr: "400",
		},
		{
			name:  "解析结果对发起人自审批保持允许",
			node:  model.Node{Name: "角色审批", AssigneeType: model.AssigneeRole, AssigneeValue: "20"},
			initr: 3,
			want:  []int64{3, 5},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAssignees(context.Background(), sys, tc.node, tc.initr)
			if tc.wantErr == "400" {
				if err == nil {
					t.Fatalf("期望 400 错误，实际解析出 %v", got)
				}
				xe, ok := xerr.As(err)
				if !ok || xe.Status != 400 {
					t.Fatalf("期望 400 业务错误，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望解析成功 %v，实际错误 %v", tc.want, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("解析结果 %v，期望 %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("解析结果 %v，期望 %v", got, tc.want)
				}
			}
		})
	}
}

// TestResolveAssignees_WalkUpStopsAtResolvedLeader 锁定向上查找语义：
// 命中负责人后不再继续向上（到达 201 即返回 4，不再查询 200 的负责人）。
func TestResolveAssignees_WalkUpStopsAtResolvedLeader(t *testing.T) {
	sys := newFakeAssigneeSys()
	sys.activeUsers[4] = true
	sys.userDept[9] = 202
	sys.deptLeader[200] = 88
	sys.deptParent[200] = 0
	sys.deptLeader[201] = 4
	sys.deptParent[201] = 200
	sys.deptParent[202] = 201

	got, err := ResolveAssignees(context.Background(), sys,
		model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader}, 9)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("解析结果 %v，期望 [4]", got)
	}
	if sys.leaderLookups[200] != 0 {
		t.Fatalf("命中 201 后不应继续向上查询 200 的负责人，实际查询 %d 次", sys.leaderLookups[200])
	}
}

// TestResolveAssignees_GatewayErrorPropagates 基础设施错误不吞掉、原样传播
// （只有「解析为空」翻译为 400 业务错误）。
func TestResolveAssignees_GatewayErrorPropagates(t *testing.T) {
	sys := newFakeAssigneeSys()
	boom := xerr.Internal(strconv.ErrSyntax)
	sys.deptVisitErr[202] = boom
	sys.userDept[9] = 202

	_, err := ResolveAssignees(context.Background(), sys,
		model.Node{Name: "主管审批", AssigneeType: model.AssigneeInitiatorDeptLeader}, 9)
	if err == nil {
		t.Fatal("期望基础设施错误原样传播")
	}
	if xe, ok := xerr.As(err); ok && xe.Status == 400 {
		t.Fatalf("基础设施错误不应被翻译为 400：%v", err)
	}
}
