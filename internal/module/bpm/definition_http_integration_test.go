//go:build integration

// 流程定义管理的 HTTP 集成用例（issue #44，spec #42 §3.1/§4.1/§4.2）：
// 草稿 CRUD、发布/停用状态机、同 key 单发布版本与版本历史、删除约束。
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（-p 1 串行跑包）。
package bpm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// demoBusinessType 是用例专用的业务类型：BusinessHandler 注册 guard 的测试替身，
// 由测试经 env.App.BPM 注册（生产装配里真实 handler 由 #46/#47 注册）。
const demoBusinessType = "test_demo_business"

// testBusinessHandler 是 BusinessHandler 的空实现：#44 只关心发布 guard 的
// 注册与否，回调逻辑（事务内回写业务状态）由 #46/#47 的集成测试覆盖。
type testBusinessHandler struct{}

func (testBusinessHandler) OnApproved(context.Context, *model.ProcessInstance, *gorm.DB) error {
	return nil
}

func (testBusinessHandler) OnRejected(context.Context, *model.ProcessInstance, *gorm.DB, string) error {
	return nil
}

func (testBusinessHandler) OnWithdrawn(context.Context, *model.ProcessInstance, *gorm.DB) error {
	return nil
}

// registerTestHandler 注册测试用 BusinessHandler（spec §4.4：未注册业务类型
// 的定义不允许发布）。
func registerTestHandler(t *testing.T, env *testutil.IntegrationEnv, businessType string) {
	t.Helper()
	env.App.BPM.RegisterBusinessHandler(businessType, testBusinessHandler{})
}

// node 是审批节点速记。
func node(name, assigneeType, assigneeValue string, deptID int64) map[string]any {
	return map[string]any{"name": name, "assigneeType": assigneeType, "assigneeValue": assigneeValue, "deptId": deptID}
}

// createDefinition 创建流程定义草稿并回读响应 data。
func createDefinition(t *testing.T, env *testutil.IntegrationEnv, token, key, businessType string, nodes []map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/bpm/definition", token, map[string]any{
		"key": key, "name": "流程-" + key, "businessType": businessType, "nodes": nodes, "remark": "集成测试",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建定义 %s：HTTP %d code %d msg %q", key, status, api.Code, api.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析创建定义响应: %v（data=%s）", err, string(api.Data))
	}
	return data
}

// getDefinition 按 ID 取定义详情，返回 (HTTP 状态, data, msg)。
func getDefinition(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/bpm/definition/"+strconv.FormatInt(id, 10), token, nil)
	var data map[string]any
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析定义详情: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, data, api.Msg
}

// publish/disable/update/remove 的速记请求，返回 (HTTP 状态, msg)。
func publishDefinition(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/bpm/definition/"+strconv.FormatInt(id, 10)+"/publish", token, nil)
	return status, api.Msg
}

func disableDefinition(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/bpm/definition/"+strconv.FormatInt(id, 10)+"/disable", token, nil)
	return status, api.Msg
}

func updateDefinition(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, body map[string]any) (int, string) {
	t.Helper()
	body["id"] = id
	status, api := env.Request(t, http.MethodPut, "/bpm/definition", token, body)
	return status, api.Msg
}

func deleteDefinition(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodDelete, "/bpm/definition/"+strconv.FormatInt(id, 10), token, nil)
	return status, api.Msg
}

// pageDefinitions 分页查询定义，返回 (rows, total)。
func pageDefinitions(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/bpm/definition/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("分页查询定义：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析定义分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// mustFail 断言一次失败请求：HTTP 状态码匹配且错误文案含指定子串。
func mustFail(t *testing.T, what string, status int, wantHTTP int, msg, wantSub string) {
	t.Helper()
	if status != wantHTTP {
		t.Fatalf("%s：HTTP %d，期望 %d（msg=%q）", what, status, wantHTTP, msg)
	}
	if wantSub != "" && !strings.Contains(msg, wantSub) {
		t.Fatalf("%s：错误文案 %q 应包含 %q", what, msg, wantSub)
	}
}

// nodesOf 从详情 data 里取出 nodes 数组。
func nodesOf(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(data["nodes"])
	if err != nil {
		t.Fatalf("序列化 nodes: %v", err)
	}
	var nodes []map[string]any
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatalf("解析 nodes: %v（raw=%s）", err, string(raw))
	}
	return nodes
}

// jsonUnmarshal 包装 json.Unmarshal（测试文件统一走这个名字）。
func jsonUnmarshal(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}

// int64StringOf 便于把回读的 ID 拼进文案/参数。
func int64StringOf(v int64) string { return strconv.FormatInt(v, 10) }
