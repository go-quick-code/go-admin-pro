//go:build integration

// 六种审批人规则的保存与发布校验、BusinessHandler 注册 guard
// （issue #44，spec §4.2/§4.4）。测试库 _t44。
package bpm_test

import (
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationDefinitionPublishRequiresHandler 未注册 BusinessHandler 的
// business_type 不允许发布（spec §4.4）；注册后同一草稿可发布。
func TestIntegrationDefinitionPublishRequiresHandler(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	const unregistered = "no_handler_business"
	created := createDefinition(t, env, admin, "guard_flow", unregistered, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	id := int64(created["id"].(float64))

	status, msg := publishDefinition(t, env, admin, id)
	mustFail(t, "未注册业务类型发布", status, http.StatusBadRequest, msg, "未注册处理器")

	// 注册后可发布；发布前定义保持草稿态（guard 不吞草稿）。
	registerTestHandler(t, env, unregistered)
	if _, detail, _ := getDefinition(t, env, admin, id); detail["status"].(float64) != 0 {
		t.Fatalf("发布失败后应保持草稿态，实际 %v", detail["status"])
	}
	if status, msg = publishDefinition(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("注册后发布：HTTP %d msg %q", status, msg)
	}
}

// TestIntegrationDefinitionSixRulesPublish 六种审批人规则可保存并发布通过
// （spec §4.2）：指定用户/角色/权限码/发起人/发起人部门负责人/指定部门负责人；
// 另验一条六规则混合线性链。
func TestIntegrationDefinitionSixRulesPublish(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	// 准备引用对象：启用用户、角色、部门（种子部门 101 研发部）。
	createUserByAPI := func(username string) int64 {
		t.Helper()
		status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
			"username": username, "nickname": username + "-昵称", "password": "123456",
			"deptId": 101, "status": 0, "roleIds": []int64{},
		})
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
		}
		status, api = env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", admin, nil)
		var page struct {
			List []struct {
				ID int64 `json:"id"`
			} `json:"list"`
		}
		if err := jsonUnmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
			t.Fatalf("查询用户 %s: %v（data=%s）", username, err, string(api.Data))
		}
		return page.List[0].ID
	}
	approverID := createUserByAPI("bpm_approver1")

	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": "bpm 审批角色", "code": "bpm_test_role", "dataScope": 5, "status": 0, "menuIds": []int64{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建角色：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/role/page?code=bpm_test_role&pageNo=1&pageSize=10", admin, nil)
	var rolePage struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := jsonUnmarshal(api.Data, &rolePage); err != nil || len(rolePage.List) != 1 {
		t.Fatalf("查询角色: %v（data=%s）", err, string(api.Data))
	}
	roleID := rolePage.List[0].ID

	// 六种规则逐个成定义发布。
	cases := []struct {
		name string
		node map[string]any
	}{
		{"指定用户", node("审批", "user", int64StringOf(approverID), 0)},
		{"指定角色", node("审批", "role", int64StringOf(roleID), 0)},
		{"权限码", node("审批", "permission", "crm:contract:approve", 0)},
		{"发起人", node("审批", "initiator", "", 0)},
		{"发起人部门负责人", node("审批", "initiator_dept_leader", "", 0)},
		{"指定部门负责人", node("审批", "dept_leader", "", 101)},
	}
	for i, tc := range cases {
		key := "rule_" + tc.node["assigneeType"].(string)
		created := createDefinition(t, env, admin, key, demoBusinessType, []map[string]any{tc.node})
		id := int64(created["id"].(float64))

		// 规则按原样保存（assigneeType/assigneeValue/deptId 往返一致）。
		if _, detail, _ := getDefinition(t, env, admin, id); len(nodesOf(t, detail)) != 1 {
			t.Fatalf("%s：节点保存异常", tc.name)
		}
		if status, msg := publishDefinition(t, env, admin, id); status != http.StatusOK {
			t.Fatalf("%s（case %d）发布：HTTP %d msg %q", tc.name, i, status, msg)
		}
	}

	// 六规则混合线性链（指定用户 → 角色 → 权限码 → 发起人 → 发起人部门负责人 → 指定部门负责人）。
	chain := createDefinition(t, env, admin, "chain_flow", demoBusinessType, []map[string]any{
		node("用户审批", "user", int64StringOf(approverID), 0),
		node("角色审批", "role", int64StringOf(roleID), 0),
		node("权限码审批", "permission", "crm:contract:approve", 0),
		node("发起人确认", "initiator", "", 0),
		node("部门负责人审批", "initiator_dept_leader", "", 0),
		node("指定部门负责人审批", "dept_leader", "", 102),
	})
	if got := len(nodesOf(t, chain)); got != 6 {
		t.Fatalf("链式定义应保存 6 个节点，实际 %d", got)
	}
	if status, msg := publishDefinition(t, env, admin, int64(chain["id"].(float64))); status != http.StatusOK {
		t.Fatalf("链式定义发布：HTTP %d msg %q", status, msg)
	}
}
