//go:build integration

// 流程定义生命周期（issue #44，spec §4.1/§4.2）：草稿 CRUD、发布/停用
// 状态机、同 key 单发布版本、版本历史、删除约束。测试库 _t44。
package bpm_test

import (
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationDefinitionDraftLifecycle 草稿可创建/编辑/删除；
// 创建落版本号与草稿状态；节点数组经 JSON 列往返无损。
func TestIntegrationDefinitionDraftLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	nodes := []map[string]any{
		node("部门负责人审批", "initiator_dept_leader", "", 0),
		node("合同审批", "permission", "crm:contract:approve", 0),
	}
	created := createDefinition(t, env, admin, "lifecycle_flow", demoBusinessType, nodes)
	id := int64(created["id"].(float64))
	if created["version"].(float64) != 1 {
		t.Fatalf("首个版本应为 1，实际 %v", created["version"])
	}
	if created["status"].(float64) != 0 {
		t.Fatalf("新定义应为草稿(0)，实际 %v", created["status"])
	}
	if created["key"].(string) != "lifecycle_flow" || created["businessType"].(string) != demoBusinessType {
		t.Fatalf("创建响应 key/businessType 不符: %v", created)
	}

	// 详情：节点往返无损（顺序与字段一致）。
	status, detail, msg := getDefinition(t, env, admin, id)
	if status != http.StatusOK {
		t.Fatalf("查询详情：HTTP %d msg %q", status, msg)
	}
	gotNodes := nodesOf(t, detail)
	if len(gotNodes) != 2 {
		t.Fatalf("详情应有 2 个节点，实际 %d", len(gotNodes))
	}
	if gotNodes[0]["assigneeType"] != "initiator_dept_leader" || gotNodes[1]["assigneeValue"] != "crm:contract:approve" {
		t.Fatalf("节点内容往返不一致: %v", gotNodes)
	}

	// 草稿可编辑：改名称/节点/备注。
	status, msg = updateDefinition(t, env, admin, id, map[string]any{
		"name":   "流程-改名",
		"nodes":  []map[string]any{node("唯一节点", "initiator", "", 0)},
		"remark": "改过",
	})
	if status != http.StatusOK {
		t.Fatalf("编辑草稿：HTTP %d msg %q", status, msg)
	}
	_, detail, _ = getDefinition(t, env, admin, id)
	if detail["name"] != "流程-改名" || detail["remark"] != "改过" {
		t.Fatalf("草稿编辑未生效: name=%v remark=%v", detail["name"], detail["remark"])
	}
	if got := nodesOf(t, detail); len(got) != 1 || got[0]["assigneeType"] != "initiator" {
		t.Fatalf("草稿节点编辑未生效: %v", got)
	}

	// 分页：key 模糊筛选命中。
	_, total := pageDefinitions(t, env, admin, "&key=lifecycle_flow")
	if total != 1 {
		t.Fatalf("按 key 筛选 total=1，实际 %d", total)
	}

	// 删除草稿 → 详情 404、列表为空。
	if status, msg = deleteDefinition(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("删除草稿：HTTP %d msg %q", status, msg)
	}
	status, _, msg = getDefinition(t, env, admin, id)
	mustFail(t, "删除后查详情", status, http.StatusNotFound, msg, "")
	if _, total := pageDefinitions(t, env, admin, "&key=lifecycle_flow"); total != 0 {
		t.Fatalf("删除后列表应为空，实际 %d", total)
	}
}

// TestIntegrationDefinitionPublishStateMachine 发布后不可编辑/删除；
// 停用后不可再停用；非草稿不可发布（409 语义）。
func TestIntegrationDefinitionPublishStateMachine(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	created := createDefinition(t, env, admin, "state_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	id := int64(created["id"].(float64))

	// 停用草稿 → 409（只有发布版本可停用）。
	status, msg := disableDefinition(t, env, admin, id)
	mustFail(t, "停用草稿", status, http.StatusConflict, msg, "草稿")

	// 发布草稿 → OK，状态 1，publishedAt 非空。
	if status, msg = publishDefinition(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("发布定义：HTTP %d msg %q", status, msg)
	}
	_, detail, _ := getDefinition(t, env, admin, id)
	if detail["status"].(float64) != 1 {
		t.Fatalf("发布后状态应为 1，实际 %v", detail["status"])
	}
	if detail["publishedAt"] == nil || detail["publishedAt"] == "" {
		t.Fatalf("发布后 publishedAt 应非空: %v", detail["publishedAt"])
	}

	// 再次发布已发布版本 → 409。
	status, msg = publishDefinition(t, env, admin, id)
	mustFail(t, "重复发布", status, http.StatusConflict, msg, "草稿")

	// 发布后编辑 → 409；发布后删除 → 409。
	status, msg = updateDefinition(t, env, admin, id, map[string]any{"name": "改不动"})
	mustFail(t, "发布后编辑", status, http.StatusConflict, msg, "不可编辑")
	status, msg = deleteDefinition(t, env, admin, id)
	mustFail(t, "发布后删除", status, http.StatusConflict, msg, "")

	// 停用 → 状态 2；再次停用 → 409；停用后编辑/删除仍 409。
	if status, msg = disableDefinition(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("停用发布版本：HTTP %d msg %q", status, msg)
	}
	_, detail, _ = getDefinition(t, env, admin, id)
	if detail["status"].(float64) != 2 {
		t.Fatalf("停用后状态应为 2，实际 %v", detail["status"])
	}
	status, msg = disableDefinition(t, env, admin, id)
	mustFail(t, "重复停用", status, http.StatusConflict, msg, "发布")
	status, msg = updateDefinition(t, env, admin, id, map[string]any{"name": "还是改不动"})
	mustFail(t, "停用后编辑", status, http.StatusConflict, msg, "不可编辑")
	status, msg = deleteDefinition(t, env, admin, id)
	mustFail(t, "停用后删除", status, http.StatusConflict, msg, "")
}

// TestIntegrationDefinitionVersioning 同 key 发新版本：新草稿版本号递增；
// 发布新版本自动停用旧发布版本，同 key 任一时刻只有一个发布版本；
// 版本历史可按 key 查询。
func TestIntegrationDefinitionVersioning(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	v1 := createDefinition(t, env, admin, "version_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	v1ID := int64(v1["id"].(float64))
	if status, msg := publishDefinition(t, env, admin, v1ID); status != http.StatusOK {
		t.Fatalf("发布 v1：HTTP %d msg %q", status, msg)
	}

	// 同 key 再建草稿 → 版本号 2；不同 key → 版本号重新从 1 计。
	v2 := createDefinition(t, env, admin, "version_flow", demoBusinessType, []map[string]any{
		node("新审批", "permission", "bpm:definition:query", 0),
	})
	v2ID := int64(v2["id"].(float64))
	if v2["version"].(float64) != 2 {
		t.Fatalf("同 key 第二个版本应为 2，实际 %v", v2["version"])
	}
	other := createDefinition(t, env, admin, "other_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	if other["version"].(float64) != 1 {
		t.Fatalf("不同 key 版本应从 1 起，实际 %v", other["version"])
	}

	// 发布 v2 → v1 自动停用；同 key 只有 v2 是发布态。
	if status, msg := publishDefinition(t, env, admin, v2ID); status != http.StatusOK {
		t.Fatalf("发布 v2：HTTP %d msg %q", status, msg)
	}
	_, v1Detail, _ := getDefinition(t, env, admin, v1ID)
	_, v2Detail, _ := getDefinition(t, env, admin, v2ID)
	if v1Detail["status"].(float64) != 2 {
		t.Fatalf("发布 v2 后 v1 应自动停用(2)，实际 %v", v1Detail["status"])
	}
	if v2Detail["status"].(float64) != 1 {
		t.Fatalf("v2 应为发布态(1)，实际 %v", v2Detail["status"])
	}
	rows, total := pageDefinitions(t, env, admin, "&key=version_flow&status=1")
	if total != 1 || len(rows) != 1 || rows[0]["id"].(float64) != float64(v2ID) {
		t.Fatalf("同 key 发布版本应唯一且为 v2，实际 total=%d rows=%v", total, rows)
	}

	// 版本历史：按 key 查询返回全部版本（含停用/草稿）。
	_, total = pageDefinitions(t, env, admin, "&key=version_flow")
	if total != 2 {
		t.Fatalf("版本历史应有 2 个版本，实际 %d", total)
	}

	// 删除已发布/停用版本 → 409（有版本历史的定义不可删，只能停用）。
	status, msg := deleteDefinition(t, env, admin, v1ID)
	mustFail(t, "删除停用版本", status, http.StatusConflict, msg, "")
	status, msg = deleteDefinition(t, env, admin, v2ID)
	mustFail(t, "删除发布版本", status, http.StatusConflict, msg, "")

	// 软删行占用版本号：删一个草稿再建，版本号跳过被占的号（唯一键不区分软删）。
	draft := createDefinition(t, env, admin, "version_flow", demoBusinessType, []map[string]any{
		node("草稿", "initiator", "", 0),
	})
	draftID := int64(draft["id"].(float64))
	if draft["version"].(float64) != 3 {
		t.Fatalf("新草稿应为版本 3，实际 %v", draft["version"])
	}
	if status, msg = deleteDefinition(t, env, admin, draftID); status != http.StatusOK {
		t.Fatalf("删除草稿 v3：HTTP %d msg %q", status, msg)
	}
	draft2 := createDefinition(t, env, admin, "version_flow", demoBusinessType, []map[string]any{
		node("草稿", "initiator", "", 0),
	})
	if draft2["version"].(float64) != 4 {
		t.Fatalf("软删行占用版本号，新草稿应为版本 4，实际 %v", draft2["version"])
	}
}

// TestIntegrationDefinitionCreateStructureValidation 节点结构非法 → 创建即 400
// （结构校验是纯函数，保存前把关；发布期再校验的是 DB 引用存在性）。
func TestIntegrationDefinitionCreateStructureValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cases := []struct {
		name    string
		nodes   []map[string]any
		wantSub string
	}{
		{"空节点", nil, "至少需要一个审批节点"},
		{"未知规则", []map[string]any{node("审批", "boss", "", 0)}, "未知的审批人规则"},
		{"用户缺 ID", []map[string]any{node("审批", "user", "", 0)}, "需要填写用户 ID"},
		{"角色非数字", []map[string]any{node("审批", "role", "abc", 0)}, "必须是数字"},
		{"权限码为空", []map[string]any{node("审批", "permission", "", 0)}, "需要填写权限码"},
		{"部门负责人缺部门", []map[string]any{node("审批", "dept_leader", "", 0)}, "需要选择部门"},
		{"节点缺名称", []map[string]any{node("", "initiator", "", 0)}, "节点名称不能为空"},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/bpm/definition", admin, map[string]any{
			"key": "invalid_flow", "name": "结构非法", "businessType": demoBusinessType, "nodes": tc.nodes,
		})
		mustFail(t, tc.name, status, http.StatusBadRequest, api.Msg, tc.wantSub)
	}
}

// TestIntegrationDefinitionPublishRefValidation 发布期校验引用存在性
// （spec §4.2）：不存在的用户/角色/部门 → 400 且文案可读；
// 校验失败不改变草稿状态（可修正后重发）。
func TestIntegrationDefinitionPublishRefValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	// 结构合法（数字 ID 能过结构校验）但引用不存在的定义可以落草稿，
	// 在发布时被引用校验拦截。
	cases := []struct {
		name    string
		nodes   []map[string]any
		wantSub string
	}{
		{"用户不存在", []map[string]any{node("审批", "user", "99999", 0)}, "用户"},
		{"角色不存在", []map[string]any{node("审批", "role", "99999", 0)}, "角色"},
		{"部门不存在", []map[string]any{node("审批", "dept_leader", "", 99999)}, "部门"},
	}
	for _, tc := range cases {
		created := createDefinition(t, env, admin, "ref_flow", demoBusinessType, tc.nodes)
		id := int64(created["id"].(float64))
		status, msg := publishDefinition(t, env, admin, id)
		mustFail(t, tc.name, status, http.StatusBadRequest, msg, tc.wantSub)

		// 校验失败不改变草稿状态：仍按草稿返回，可编辑修正。
		st, detail, msg := getDefinition(t, env, admin, id)
		if st != http.StatusOK || detail["status"].(float64) != 0 {
			t.Fatalf("%s：发布校验失败后应保持草稿态，HTTP %d status=%v msg=%q", tc.name, st, detail["status"], msg)
		}
		if status, msg = deleteDefinition(t, env, admin, id); status != http.StatusOK {
			t.Fatalf("%s：清理草稿失败 HTTP %d msg %q", tc.name, status, msg)
		}
	}
}

// TestIntegrationDefinitionPublishDisabledUserRejected 指定用户规则指向
// 已停用用户 → 发布 400（spec §4.2：候选人排除停用用户，发布期即可校验）。
func TestIntegrationDefinitionPublishDisabledUserRejected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	// 建一个用户再停用（走系统管理 API）。
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": "bpm_disabled_user", "nickname": "停用用户", "password": "123456",
		"deptId": 101, "status": 0, "roleIds": []int64{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/user/page?username=bpm_disabled_user&pageNo=1&pageSize=10", admin, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := jsonUnmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询用户: %v（data=%s）", err, string(api.Data))
	}
	uid := page.List[0].ID
	if status, api = env.Request(t, http.MethodPut, "/system/user/status", admin, map[string]any{
		"id": uid, "status": 1,
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("停用用户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	created := createDefinition(t, env, admin, "disabled_user_flow", demoBusinessType, []map[string]any{
		node("审批", "user", int64StringOf(uid), 0),
	})
	id := int64(created["id"].(float64))
	pStatus, msg := publishDefinition(t, env, admin, id)
	mustFail(t, "停用用户作为审批人", pStatus, http.StatusBadRequest, msg, "停用")
}
