package service

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
)

// 审批人规则与节点结构的纯函数校验（spec #42 §3.1/§4.2，次 seam 表驱动测试）：
// 结构合法性在保存/发布前由 ValidateNodes 把关，引用存在性（用户/角色/部门）
// 依赖 DB，属于发布时的 SystemGateway 校验，不在本文件。

func TestValidateNodes(t *testing.T) {
	valid := func(assigneeType, assigneeValue string, deptID int64) model.Node {
		return model.Node{Name: "审批节点", AssigneeType: assigneeType, AssigneeValue: assigneeValue, DeptID: deptID}
	}

	cases := []struct {
		name    string
		nodes   []model.Node
		wantErr string // 空串表示期望通过
	}{
		{
			name:    "空节点列表拒绝",
			nodes:   nil,
			wantErr: "至少需要一个审批节点",
		},
		{
			name:    "指定用户规则合法",
			nodes:   []model.Node{valid(model.AssigneeUser, "1", 0)},
			wantErr: "",
		},
		{
			name:    "角色规则合法",
			nodes:   []model.Node{valid(model.AssigneeRole, "2", 0)},
			wantErr: "",
		},
		{
			name:    "权限码规则合法",
			nodes:   []model.Node{valid(model.AssigneePermission, "crm:contract:approve", 0)},
			wantErr: "",
		},
		{
			name:    "发起人规则无需取值",
			nodes:   []model.Node{valid(model.AssigneeInitiator, "", 0)},
			wantErr: "",
		},
		{
			name:    "发起人部门负责人规则无需取值",
			nodes:   []model.Node{valid(model.AssigneeInitiatorDeptLeader, "", 0)},
			wantErr: "",
		},
		{
			name:    "指定部门负责人规则带部门合法",
			nodes:   []model.Node{valid(model.AssigneeDeptLeader, "", 100)},
			wantErr: "",
		},
		{
			name:    "未知审批人规则拒绝",
			nodes:   []model.Node{valid("boss", "", 0)},
			wantErr: "未知的审批人规则",
		},
		{
			name:    "指定用户缺少用户 ID 拒绝",
			nodes:   []model.Node{valid(model.AssigneeUser, "", 0)},
			wantErr: "（指定用户）需要填写用户 ID",
		},
		{
			name:    "指定用户非数字 ID 拒绝",
			nodes:   []model.Node{valid(model.AssigneeUser, "abc", 0)},
			wantErr: "（指定用户）的用户 ID 必须是数字",
		},
		{
			name:    "角色规则缺少角色 ID 拒绝",
			nodes:   []model.Node{valid(model.AssigneeRole, "", 0)},
			wantErr: "（指定角色）需要填写角色 ID",
		},
		{
			name:    "角色规则非数字 ID 拒绝",
			nodes:   []model.Node{valid(model.AssigneeRole, "1.5", 0)},
			wantErr: "（指定角色）的角色 ID 必须是数字",
		},
		{
			name:    "权限码规则缺少权限码拒绝",
			nodes:   []model.Node{valid(model.AssigneePermission, "", 0)},
			wantErr: "（权限码）需要填写权限码",
		},
		{
			name:    "指定部门负责人缺少部门拒绝",
			nodes:   []model.Node{valid(model.AssigneeDeptLeader, "", 0)},
			wantErr: "（指定部门负责人）需要选择部门",
		},
		{
			name:    "节点缺少名称拒绝",
			nodes:   []model.Node{{AssigneeType: model.AssigneeInitiator}},
			wantErr: "节点名称不能为空",
		},
		{
			name:    "节点名称空白拒绝",
			nodes:   []model.Node{{Name: "   ", AssigneeType: model.AssigneeInitiator}},
			wantErr: "节点名称不能为空",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNodes(tc.nodes)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("期望通过，实际错误: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("期望错误含 %q，实际通过", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("期望错误含 %q，实际 %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestValidateNodesNodeNameTooLong(t *testing.T) {
	err := ValidateNodes([]model.Node{{
		Name:         strings.Repeat("节", 65),
		AssigneeType: model.AssigneeInitiator,
	}})
	if err == nil || !strings.Contains(err.Error(), "64") {
		t.Fatalf("节点名称超 64 字应拒绝且文案含 64，实际 %v", err)
	}
}

func TestValidateNodesTooManyNodes(t *testing.T) {
	nodes := make([]model.Node, MaxNodeCount+1)
	for i := range nodes {
		nodes[i] = model.Node{Name: "节点", AssigneeType: model.AssigneeInitiator}
	}
	err := ValidateNodes(nodes)
	if err == nil || !strings.Contains(err.Error(), "审批节点数") {
		t.Fatalf("节点数超上限应拒绝且文案含「审批节点数」，实际 %v", err)
	}
}

func TestValidateNodesMultiNodeChain(t *testing.T) {
	nodes := []model.Node{
		{Name: "部门负责人审批", AssigneeType: model.AssigneeInitiatorDeptLeader},
		{Name: "财务审批", AssigneeType: model.AssigneePermission, AssigneeValue: "oa:expense:manage"},
		{Name: "总经理审批", AssigneeType: model.AssigneeUser, AssigneeValue: "1"},
	}
	if err := ValidateNodes(nodes); err != nil {
		t.Fatalf("多节点线性链应通过，实际 %v", err)
	}
}

func TestIsKnownAssigneeType(t *testing.T) {
	known := map[string]bool{
		model.AssigneeUser:                true,
		model.AssigneeRole:                true,
		model.AssigneePermission:          true,
		model.AssigneeInitiator:           true,
		model.AssigneeInitiatorDeptLeader: true,
		model.AssigneeDeptLeader:          true,
		"":                                false,
		"boss":                            false,
		"USER":                            false,
	}
	for typ, want := range known {
		if got := IsKnownAssigneeType(typ); got != want {
			t.Fatalf("IsKnownAssigneeType(%q)=%v，期望 %v", typ, got, want)
		}
	}
}
