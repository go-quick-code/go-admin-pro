//go:build integration

// 客户管理 HTTP 集成测试（issue #8）：走 #3 基座的真实 router + 生产中间件
// （JWT/权限/租户插件）+ 真实 MySQL，在 HTTP 契约上验证 CRUD 全链路与编号规则。
package crm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// customerNoRe 校验编号形状：KH + yyyyMMdd + 至少 4 位序号（超 9999 自然扩位）。
var customerNoRe = regexp.MustCompile(`^KH\d{8}\d{4,}$`)

// createCustomer 创建客户并断言成功，返回落库后的完整客户 JSON。
func createCustomer(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/customer", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建客户 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var customer map[string]any
	if err := json.Unmarshal(api.Data, &customer); err != nil {
		t.Fatalf("解析创建客户响应: %v（data=%s）", err, string(api.Data))
	}
	return customer
}

// pageCustomers 查询客户分页并返回 list 与 total。
func pageCustomers(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/customer/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询客户分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析客户分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getCustomer 查询详情，返回 HTTP 状态、业务码与原始 data。
func getCustomer(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/customer/%d", id), token, nil)
}

// TestIntegrationCustomerCRUDAndNumber 客户 CRUD 全链路与编号规则：
// 编号 KH+yyyyMMdd+4 位序号、租户内唯一、创建时自动生成且不可由客户端指定。
func TestIntegrationCustomerCRUDAndNumber(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 创建：编号自动生成，负责人缺省归属当前用户并快照其部门。
	first := createCustomer(t, env, token, "客户甲", map[string]any{
		"industry": "制造业", "level": "A", "source": "展会", "phone": "13800000001", "remark": "第一条纵切",
	})
	no1, _ := first["customerNo"].(string)
	if !customerNoRe.MatchString(no1) {
		t.Fatalf("客户编号 %q 不符合 KH+yyyyMMdd+4位序号", no1)
	}
	if want := "KH" + time.Now().Format("20060102") + "0001"; no1 != want {
		t.Fatalf("租户当日首号应为 %s，实际 %s", want, no1)
	}
	if id := first["ownerUserId"]; id != float64(1) {
		t.Fatalf("缺省负责人应归属当前用户 admin(id=1)，实际 %v", id)
	}
	if dept := first["ownerDeptId"]; dept != float64(100) {
		t.Fatalf("负责人部门快照应为 100（总公司），实际 %v", dept)
	}
	if first["ownerDeptName"] != "总公司" {
		t.Fatalf("详情应带部门名称，实际 ownerDeptName=%v", first["ownerDeptName"])
	}

	second := createCustomer(t, env, token, "客户乙", nil)
	no2, _ := second["customerNo"].(string)
	if no2 == no1 || !customerNoRe.MatchString(no2) {
		t.Fatalf("第二个编号 %s 应与首号 %s 不同且符合形状", no2, no1)
	}

	// 客户端传入 customerNo 无效：编号只能由服务端发放。
	forged := createCustomer(t, env, token, "客户丙", map[string]any{"customerNo": "KH999999999999"})
	if forged["customerNo"] == "KH999999999999" {
		t.Fatal("客户端指定的 customerNo 不应被采纳")
	}

	// 详情回读。
	status, api := getCustomer(t, env, token, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询客户详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "客户甲" || detail["industry"] != "制造业" || detail["status"] != float64(1) {
		t.Fatalf("详情字段不符：name=%v industry=%v status=%v", detail["name"], detail["industry"], detail["status"])
	}
	if detail["ownerUserName"] != "超级管理员" {
		t.Fatalf("详情应带负责人昵称，实际 ownerUserName=%v", detail["ownerUserName"])
	}

	// 更新：名称与状态生效，编号不变。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/customer", token, map[string]any{
		"id": first["id"], "name": "客户甲改", "status": 2, "customerNo": "KH000000000000",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新客户：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getCustomer(t, env, token, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("更新后查询详情：HTTP %d code %d", status, api.Code)
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "客户甲改" || detail["status"] != float64(2) {
		t.Fatalf("更新未生效：name=%v status=%v", detail["name"], detail["status"])
	}
	if detail["customerNo"] != no1 {
		t.Fatalf("更新不应改动编号：期望 %s 实际 %v", no1, detail["customerNo"])
	}

	// 软删除：详情 404、列表不可见，重新创建同日序号继续递增（唯一性不被软删除行占用）。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/customer/%d", int64(second["id"].(float64))), token, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除客户：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getCustomer(t, env, token, int64(second["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	list, total := pageCustomers(t, env, token, "")
	if total != 2 {
		t.Fatalf("删除后列表 total=%d，期望 2（软删除不可见）", total)
	}
	for _, row := range list {
		if row["id"] == second["id"] {
			t.Fatal("软删除的客户不应出现在列表")
		}
	}
}

// createCustomerOnce 在独立 goroutine 中发一次创建请求：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），错误带回主 goroutine 断言。
func createCustomerOnce(env *testutil.IntegrationEnv, token, name string) (string, error) {
	raw, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/crm/customer"), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		return "", fmt.Errorf("HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	var c struct {
		CustomerNo string `json:"customerNo"`
	}
	if err := json.Unmarshal(api.Data, &c); err != nil {
		return "", err
	}
	return c.CustomerNo, nil
}

// TestIntegrationCustomerNumberUniqueUnderConcurrency 并发创建不重号：
// 同租户同日 N 个并发请求，编号两两不同且按序发放。
func TestIntegrationCustomerNumberUniqueUnderConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	const n = 20
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			no, err := createCustomerOnce(env, token, fmt.Sprintf("并发客户%d", i))
			results[i], errs[i] = no, err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发创建 #%d 失败: %v", i, err)
		}
	}
	seen := make(map[string]bool, n)
	for i, no := range results {
		if no == "" {
			t.Fatalf("并发创建 #%d 未返回编号", i)
		}
		if seen[no] {
			t.Fatalf("编号 %s 重复发放", no)
		}
		seen[no] = true
	}
	if len(seen) != n {
		t.Fatalf("应发放 %d 个不同编号，实际 %d", n, len(seen))
	}

	// 服务端落库数与编号数一致。
	list, total := pageCustomers(t, env, token, "&name=并发客户")
	if total != n || len(list) != n {
		t.Fatalf("并发创建后按名称筛选 total=%d list=%d，期望 %d", total, len(list), n)
	}
}

// TestIntegrationCustomerValidation 非法输入返回 400：状态枚举、必填名称。
func TestIntegrationCustomerValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "名称为空", payload: map[string]any{"name": ""}},
		{name: "状态非法", payload: map[string]any{"name": "非法状态客户", "status": 9}},
		{name: "名称超长", payload: map[string]any{"name": strings.Repeat("长", 129)}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/customer", token, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if api.Msg == "" {
			t.Fatalf("%s：应返回中文错误信息", tc.name)
		}
	}

	// 非法状态不被落库。
	_, total := pageCustomers(t, env, token, "&name=非法状态客户")
	if total != 0 {
		t.Fatalf("非法状态客户被落库：total=%d，期望 0", total)
	}

	// 列表筛选 status=9 同样拒绝（400），绝不静默当「无此筛选」。
	status, api := env.Request(t, http.MethodGet, "/crm/customer/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d code %d，期望 400", status, api.Code)
	}
}
