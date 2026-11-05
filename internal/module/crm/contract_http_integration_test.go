//go:build integration

// 合同 HTTP 集成测试（issue #16）：真实 router + 生产中间件（JWT/权限/租户插件）
// + 真实 MySQL，在 HTTP 契约上验证 CRUD 全链路、HT 编号、金额不变量、状态机
// （提交/审批/驳回/作废）、审批权限码、编辑与删除口径、数据范围矩阵、租户隔离、
// 产品引用约束（RefCheckerChain 生效）与合同维度跟进。
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

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmContractMenuID 是种子中的合同管理菜单 ID（含 crm:contract:query 权限码）。
const crmContractMenuID = int64(4100)

// contractNoRe 校验编号形状：HT + yyyyMMdd + 至少 4 位序号（超 9999 自然扩位）。
var contractNoRe = regexp.MustCompile(`^HT\d{8}\d{4,}$`)

// createContract 创建合同并断言成功，返回落库后的完整合同 JSON。
func createContract(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/contract", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建合同 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建合同响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageContracts 查询合同分页并返回 list 与 total。
func pageContracts(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/contract/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询合同分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析合同分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getContractRaw 查询合同详情，返回 HTTP 状态、业务码与原始 data。
func getContractRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/contract/%d", id), token, nil)
}

// changeContractStatus 状态流转（唯一状态入口），返回 HTTP 状态与业务响应。
func changeContractStatus(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, status int8, reason string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/contract/status", token, map[string]any{
		"id": id, "status": status, "reason": reason,
	})
}

// visibleContractNames 按观察者视角查合同分页，返回可见合同名称集合。
func visibleContractNames(t *testing.T, env *testutil.IntegrationEnv, token string) map[string]bool {
	t.Helper()
	list, _ := pageContracts(t, env, token, "")
	got := make(map[string]bool, len(list))
	for _, row := range list {
		got[row["name"].(string)] = true
	}
	return got
}

// assertContractSet 断言可见集合与期望完全一致（want 全可见，notWant 全不可见）。
func assertContractSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：合同 %q 应可见，实际不可见（可见集合 %v）", phase, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：合同 %q 不应可见，实际可见", phase, name)
		}
	}
}

// TestIntegrationContractCRUDAndNumber 合同 CRUD 全链路与编号规则：
// 编号 HT+yyyyMMdd+4 位序号、租户内唯一、创建时自动生成且不可由客户端指定、
// 默认负责人与部门快照、编辑不动编号与状态、软删除后不可见、来源商机须同客户。
func TestIntegrationContractCRUDAndNumber(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	opportunity := createOpportunity(t, env, admin, "合同来源商机", map[string]any{"customerId": customerID})
	opportunityID := int64(opportunity["id"].(float64))

	// 创建：编号自动生成，负责人缺省归属当前用户并快照其部门。
	first := createContract(t, env, admin, "合同甲", map[string]any{
		"customerId": customerID, "opportunityId": opportunityID,
		"amount": "5000.00", "signDate": "2026-11-01",
		"startDate": "2026-11-02", "endDate": "2027-11-01", "remark": "第一条合同纵切",
	})
	no1, _ := first["contractNo"].(string)
	if !contractNoRe.MatchString(no1) {
		t.Fatalf("合同编号 %q 不符合 HT+yyyyMMdd+4位序号", no1)
	}
	if want := "HT" + time.Now().Format("20060102") + "0001"; no1 != want {
		t.Fatalf("租户当日首号应为 %s，实际 %s", want, no1)
	}
	if id := first["ownerUserId"]; id != float64(1) {
		t.Fatalf("缺省负责人应归属当前用户 admin(id=1)，实际 %v", id)
	}
	if dept := first["ownerDeptId"]; dept != float64(100) {
		t.Fatalf("负责人部门快照应为 100（总公司），实际 %v", dept)
	}
	if first["status"] != float64(1) {
		t.Fatalf("缺省状态应为 1（草稿），实际 %v", first["status"])
	}
	if first["customerName"] != "合同宿主客户" {
		t.Fatalf("创建响应应带客户名称，实际 customerName=%v", first["customerName"])
	}
	if first["opportunityName"] != "合同来源商机" {
		t.Fatalf("创建响应应带来源商机名称，实际 opportunityName=%v", first["opportunityName"])
	}
	if first["amount"] != "5000.00" {
		t.Fatalf("金额应回显十进制字符串，实际 %v", first["amount"])
	}

	second := createContract(t, env, admin, "合同乙", map[string]any{"customerId": customerID})
	no2, _ := second["contractNo"].(string)
	if no2 == no1 || !contractNoRe.MatchString(no2) {
		t.Fatalf("第二个编号 %s 应与首号 %s 不同且符合形状", no2, no1)
	}

	// 客户端传入 contractNo 无效：编号只能由服务端发放。
	forged := createContract(t, env, admin, "合同丙", map[string]any{
		"customerId": customerID, "contractNo": "HT999999999999",
	})
	if forged["contractNo"] == "HT999999999999" {
		t.Fatal("客户端指定的 contractNo 不应被采纳")
	}

	// 来源商机不属于所选客户：400。
	otherCustomer := createCustomer(t, env, admin, "其他客户", nil)
	otherCustomerID := int64(otherCustomer["id"].(float64))
	status, api := env.Request(t, http.MethodPost, "/crm/contract", admin, map[string]any{
		"customerId": otherCustomerID, "opportunityId": opportunityID, "name": "跨客户合同",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("来源商机跨客户：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 详情回读：客户名称、负责人昵称、签订/起止日期。
	status, api = getContractRaw(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询合同详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "合同甲" || detail["status"] != float64(1) {
		t.Fatalf("详情字段不符：name=%v status=%v", detail["name"], detail["status"])
	}
	if detail["ownerUserName"] != "超级管理员" || detail["ownerDeptName"] != "总公司" {
		t.Fatalf("详情应带负责人昵称与部门名称，实际 %v / %v", detail["ownerUserName"], detail["ownerDeptName"])
	}
	// 日期字段与商机 expectedCloseDate 同一序列化口径（*time.Time → RFC3339），
	// 断言日期前缀即可（date 列无时间成分）。
	if !strings.HasPrefix(fmt.Sprint(detail["signDate"]), "2026-11-01") ||
		!strings.HasPrefix(fmt.Sprint(detail["startDate"]), "2026-11-02") ||
		!strings.HasPrefix(fmt.Sprint(detail["endDate"]), "2027-11-01") {
		t.Fatalf("签订/起止日期应落库回读，实际 %v / %v / %v", detail["signDate"], detail["startDate"], detail["endDate"])
	}

	// 更新：名称/签订日期/备注生效；编号不变；编辑接口不带状态字段，状态不被改动。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": first["id"], "name": "合同甲改", "amount": "6000.50",
		"signDate": "2026-11-05", "startDate": "", "endDate": "",
		"remark": "改过备注", "contractNo": "HT000000000000", "status": 3,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新合同：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContractRaw(t, env, admin, int64(first["id"].(float64)))
	mustOK(t, "更新后查询详情", status, api)
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "合同甲改" || !strings.HasPrefix(fmt.Sprint(detail["signDate"]), "2026-11-05") {
		t.Fatalf("更新未生效：name=%v signDate=%v", detail["name"], detail["signDate"])
	}
	if detail["contractNo"] != no1 {
		t.Fatalf("更新不应改动编号：期望 %s 实际 %v", no1, detail["contractNo"])
	}
	if detail["status"] != float64(1) {
		t.Fatalf("编辑接口不应改状态：期望 1，实际 %v", detail["status"])
	}

	// 起止日期校验：起始晚于失效 400。
	status, api = env.Request(t, http.MethodPost, "/crm/contract", admin, map[string]any{
		"customerId": customerID, "name": "日期倒挂合同",
		"startDate": "2027-01-01", "endDate": "2026-01-01",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("起止日期倒挂：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", int64(second["id"].(float64))), admin, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除草稿合同：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getContractRaw(t, env, admin, int64(second["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total := pageContracts(t, env, admin, "")
	if total != 2 {
		t.Fatalf("删除后列表 total=%d，期望 2（软删除不可见）", total)
	}
}

// createContractOnce 在独立 goroutine 中发一次创建请求：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），错误带回主 goroutine 断言。
func createContractOnce(env *testutil.IntegrationEnv, token string, customerID int64, name string) (string, error) {
	raw, err := json.Marshal(map[string]any{"customerId": customerID, "name": name})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/crm/contract"), bytes.NewReader(raw))
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
		ContractNo string `json:"contractNo"`
	}
	if err := json.Unmarshal(api.Data, &c); err != nil {
		return "", err
	}
	return c.ContractNo, nil
}

// TestIntegrationContractNumberUniqueUnderConcurrency 并发创建不重号：
// 同租户同日 N 个并发请求，编号两两不同且按序发放（crm_sequence 原子发放，照 KH/SJ）。
func TestIntegrationContractNumberUniqueUnderConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "并发合同宿主", nil)
	customerID := int64(customer["id"].(float64))

	const n = 20
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			no, err := createContractOnce(env, admin, customerID, fmt.Sprintf("并发合同%d", i))
			results[i], errs[i] = no, err
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发创建 #%d 失败: %v", i, err)
		}
		if results[i] == "" {
			t.Fatalf("并发创建 #%d 未返回编号", i)
		}
		if seen[results[i]] {
			t.Fatalf("编号 %s 重复发放", results[i])
		}
		seen[results[i]] = true
	}
	_, total := pageContracts(t, env, admin, "&name=并发合同")
	if total != n {
		t.Fatalf("并发创建后按名称筛选 total=%d，期望 %d", total, n)
	}
}

// TestIntegrationContractAmountInvariant 金额不变量（spec §4.3）：
// 有行项目时合同金额 = SUM(行项目金额)（服务端计算，客户端提交的金额忽略）；
// 无行项目时手填且 ≥ 0；整体替换/清空行项目后金额按不变量重算。
func TestIntegrationContractAmountInvariant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "金额宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	p1 := createLineItemProduct(t, env, admin, "合同行项目产品一", "1234.56")
	p2 := createLineItemProduct(t, env, admin, "合同行项目产品二", "99.99")
	p1ID := int64(p1["id"].(float64))
	p2ID := int64(p2["id"].(float64))

	// 创建带行项目：金额 = 行项目合计 2769.09（2×1234.56 + 3×99.99），
	// 客户端乱传的 amount=1.00 被忽略，行项目里乱传的 amount 同样被忽略。
	c := createContract(t, env, admin, "带行项目合同", map[string]any{
		"customerId": customerID, "amount": "1.00",
		"products": []map[string]any{
			{"productId": p1ID, "quantity": 2, "price": "1234.56", "amount": "999999.99"},
			{"productId": p2ID, "quantity": 3, "price": "99.99"},
		},
	})
	cID := int64(c["id"].(float64))
	if c["amount"] != "2769.09" {
		t.Fatalf("有行项目时金额应等于合计 2769.09，实际 %v", c["amount"])
	}
	if c["totalAmount"] != "2769.09" {
		t.Fatalf("创建响应合计 = %v，期望 2769.09", c["totalAmount"])
	}

	// 详情回读行项目：金额服务端计算、带产品名称。
	status, api := getContractRaw(t, env, admin, cID)
	mustOK(t, "查询行项目合同详情", status, api)
	var detail struct {
		Amount      string `json:"amount"`
		TotalAmount string `json:"totalAmount"`
		Products    []struct {
			ProductID   int64  `json:"productId"`
			ProductName string `json:"productName"`
			Quantity    int64  `json:"quantity"`
			Price       string `json:"price"`
			Amount      string `json:"amount"`
		} `json:"products"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v（data=%s）", err, string(api.Data))
	}
	if len(detail.Products) != 2 || detail.TotalAmount != "2769.09" {
		t.Fatalf("详情行项目数=%d 合计=%q，期望 2 行合计 2769.09", len(detail.Products), detail.TotalAmount)
	}
	if detail.Products[0].ProductID == p1ID && detail.Products[0].Amount != "2469.12" {
		t.Fatalf("行项目金额应服务端计算 2469.12，实际 %q", detail.Products[0].Amount)
	}

	// products 不传（nil）：行项目保持不变，有行项目时手填金额被忽略（不变量优先）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "带行项目合同", "amount": "6000.00",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("不带行项目更新：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "更新后查详情", status, api)
	detail.Products = nil
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail.Amount != "2769.09" {
		t.Fatalf("有行项目时手填金额应被忽略：期望 2769.09，实际 %s", detail.Amount)
	}

	// 整体替换行项目：只留产品一数量 5 → 6172.80。
	upStatus, upAPI = env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "带行项目合同",
		"products": []map[string]any{{"productId": p1ID, "quantity": 5, "price": "1234.56"}},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("替换行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "替换后查详情", status, api)
	detail.Products = nil
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if len(detail.Products) != 1 || detail.Amount != "6172.80" {
		t.Fatalf("替换后行项目数=%d 金额=%q，期望 1 行 6172.80", len(detail.Products), detail.Amount)
	}

	// 清空行项目：金额回退手填 800.00。
	upStatus, upAPI = env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "带行项目合同", "amount": "800.00", "products": []map[string]any{},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("清空行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "清空后查详情", status, api)
	detail.Products = nil
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if len(detail.Products) != 0 || detail.Amount != "800.00" {
		t.Fatalf("清空后行项目数=%d 金额=%q，期望 0 行手填 800.00", len(detail.Products), detail.Amount)
	}

	// 无行项目手填负数 400；非法输入不落库。
	status, api = env.Request(t, http.MethodPost, "/crm/contract", admin, map[string]any{
		"customerId": customerID, "name": "负金额合同", "amount": "-0.01",
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "不能为负") {
		t.Fatalf("手填负金额：HTTP %d code %d msg %q，期望 400 且文案含「不能为负」", status, api.Code, api.Msg)
	}
	// 无行项目手填 0 与正常金额放行。
	zero := createContract(t, env, admin, "零金额合同", map[string]any{"customerId": customerID, "amount": "0.00"})
	if zero["amount"] != "0.00" {
		t.Fatalf("零金额合同应放行，实际 %v", zero["amount"])
	}
}

// TestIntegrationContractLineItemValidation 合同行项目非法输入 400：
// 数量非正整数、单价为负、产品不存在、产品重复；非法输入不落库。
func TestIntegrationContractLineItemValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同行项目校验宿主", nil)
	customerID := int64(customer["id"].(float64))
	p1 := createLineItemProduct(t, env, admin, "合同校验产品一", "1.00")
	p1ID := int64(p1["id"].(float64))

	cases := []struct {
		name     string
		products []map[string]any
		wantMsg  string
	}{
		{name: "数量为 0", products: []map[string]any{{"productId": p1ID, "quantity": 0, "price": "1.00"}}, wantMsg: "数量"},
		{name: "单价为负", products: []map[string]any{{"productId": p1ID, "quantity": 1, "price": "-0.01"}}, wantMsg: "单价"},
		{name: "产品不存在", products: []map[string]any{{"productId": 999999, "quantity": 1, "price": "1.00"}}, wantMsg: "不存在"},
		{name: "产品重复", products: []map[string]any{
			{"productId": p1ID, "quantity": 1, "price": "1.00"},
			{"productId": p1ID, "quantity": 2, "price": "1.00"},
		}, wantMsg: "重复"},
		{name: "金额越界", products: []map[string]any{{"productId": p1ID, "quantity": 100000, "price": "9999999999.99"}}, wantMsg: "金额"},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/contract", admin, map[string]any{
			"customerId": customerID, "name": "行项目校验合同", "products": tc.products,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if !strings.Contains(api.Msg, tc.wantMsg) {
			t.Fatalf("%s：文案应包含 %q，实际 %q", tc.name, tc.wantMsg, api.Msg)
		}
	}
	_, total := pageContracts(t, env, admin, "&name=行项目校验合同")
	if total != 0 {
		t.Fatalf("非法行项目合同被落库：total=%d，期望 0", total)
	}
}

// TestIntegrationContractStatusMachine 状态机全链路（spec §4.4）：
// 草稿→待审→已签；待审驳回回草稿（驳回原因落库、重新提交清空）；已签/待审/草稿
// 作废；非法流转与重复操作 400；作废终态锁定。
func TestIntegrationContractStatusMachine(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "状态机宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	c := createContract(t, env, admin, "状态机合同", map[string]any{"customerId": customerID})
	cID := int64(c["id"].(float64))

	// 草稿期的非法流转：直接审批 400、同状态 400、非法枚举 400。
	if status, api := changeContractStatus(t, env, admin, cID, 3, ""); status != http.StatusBadRequest {
		t.Fatalf("草稿直接审批：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, admin, cID, 1, ""); status != http.StatusBadRequest {
		t.Fatalf("草稿重复置草稿：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, admin, cID, 9, ""); status != http.StatusBadRequest {
		t.Fatalf("非法状态 9：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 草稿 → 待审（提交）。
	status, api := changeContractStatus(t, env, admin, cID, 2, "")
	mustOK(t, "提交审批", status, api)
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析状态流转响应: %v（data=%s）", err, string(api.Data))
	}
	if row["status"] != float64(2) {
		t.Fatalf("提交后状态应 2（待审），实际 %v", row["status"])
	}
	// 待审期重复提交 400。
	if status, api := changeContractStatus(t, env, admin, cID, 2, ""); status != http.StatusBadRequest {
		t.Fatalf("待审重复提交：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 待审驳回回草稿：驳回原因落库。
	status, api = changeContractStatus(t, env, admin, cID, 1, "金额需要重新核算")
	mustOK(t, "驳回回草稿", status, api)
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "驳回后查详情", status, api)
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if row["status"] != float64(1) || row["rejectReason"] != "金额需要重新核算" {
		t.Fatalf("驳回后应回草稿且带原因：status=%v rejectReason=%v", row["status"], row["rejectReason"])
	}

	// 重新提交：驳回原因清空。
	if status, api = changeContractStatus(t, env, admin, cID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重新提交：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "重提后查详情", status, api)
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if row["rejectReason"] != "" && row["rejectReason"] != nil {
		t.Fatalf("重新提交后驳回原因应清空，实际 %v", row["rejectReason"])
	}

	// 待审 → 已签（审批通过）。
	if status, api = changeContractStatus(t, env, admin, cID, 3, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 已签期的非法流转：回草稿/回待审/重复审批都 400。
	for _, to := range []int8{1, 2, 3} {
		if status, api := changeContractStatus(t, env, admin, cID, to, ""); status != http.StatusBadRequest {
			t.Fatalf("已签流转到 %d：HTTP %d code %d msg %q，期望 400", to, status, api.Code, api.Msg)
		}
	}

	// 已签 → 作废；作废终态锁定。
	if status, api = changeContractStatus(t, env, admin, cID, 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("已签作废：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	for _, to := range []int8{1, 2, 3, 4} {
		if status, api := changeContractStatus(t, env, admin, cID, to, ""); status != http.StatusBadRequest {
			t.Fatalf("作废后流转到 %d：HTTP %d code %d msg %q，期望 400（终态锁定）", to, status, api.Code, api.Msg)
		}
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "作废后查详情", status, api)
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if row["status"] != float64(4) {
		t.Fatalf("作废后状态应 4，实际 %v", row["status"])
	}

	// 待审/草稿作废路径：各起一单验证。
	c2 := createContract(t, env, admin, "待审作废合同", map[string]any{"customerId": customerID})
	c2ID := int64(c2["id"].(float64))
	if status, api = changeContractStatus(t, env, admin, c2ID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api = changeContractStatus(t, env, admin, c2ID, 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("待审作废：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	c3 := createContract(t, env, admin, "草稿作废合同", map[string]any{"customerId": customerID})
	if status, api = changeContractStatus(t, env, admin, int64(c3["id"].(float64)), 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("草稿作废：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationContractApprovalPermission 审批权限码（crm:contract:approve）：
// 持有更新码但无审批码的用户可提交（草稿→待审），审批通过与驳回 403；
// 持审批码的用户审批通过。提交人可自审（规格未约束提交/审批分离，见提交说明）。
func TestIntegrationContractApprovalPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "审批权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 观察者角色：合同查询 + 修改（无 4105 审批按钮），仅本人数据范围。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "合同提交者角色", "code": "contract_submitter", "dataScope": 5,
		"menuIds": []int64{crmContractMenuID, 4101, 4102, 4103},
	})
	createUserByAPI(t, env, admin, "contract_submitter", 102, []int64{roleID})
	submitterID := findUserIDByUsername(t, env, admin, "contract_submitter")
	submitter := env.Login(t, "contract_submitter", "123456")

	// 合同归属提交者（仅本人范围内可见）。
	c := createContract(t, env, admin, "审批权限合同", map[string]any{
		"customerId": customerID, "ownerUserId": submitterID,
	})
	cID := int64(c["id"].(float64))

	// 提交审批：只需更新码，放行。
	if status, api := changeContractStatus(t, env, submitter, cID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("无审批码用户提交：HTTP %d code %d msg %q，期望放行", status, api.Code, api.Msg)
	}
	// 审批通过与驳回：无 crm:contract:approve，403。
	if status, api := changeContractStatus(t, env, submitter, cID, 3, ""); status != http.StatusForbidden {
		t.Fatalf("无审批码审批通过：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, submitter, cID, 1, "越权驳回"); status != http.StatusForbidden {
		t.Fatalf("无审批码驳回：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// admin 持全部权限码：审批通过。
	if status, api := changeContractStatus(t, env, admin, cID, 3, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 审批通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, _ := getContractRaw(t, env, admin, cID)
	if status != http.StatusOK {
		t.Fatalf("审批后查详情：HTTP %d", status)
	}
}

// TestIntegrationContractEditAndDeleteRules 编辑与删除口径：
// 已签仅元数据可改（金额锁定、行项目 400）；作废不可编辑；
// 待审/已签不可删（400），草稿/作废可删。
func TestIntegrationContractEditAndDeleteRules(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "编辑规则宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	p1 := createLineItemProduct(t, env, admin, "编辑规则产品", "10.00")
	p1ID := int64(p1["id"].(float64))

	c := createContract(t, env, admin, "编辑规则合同", map[string]any{
		"customerId": customerID, "amount": "50.00",
		"products": []map[string]any{{"productId": p1ID, "quantity": 5, "price": "10.00"}},
	})
	cID := int64(c["id"].(float64))

	// 提交 → 审批通过（走到已签）。
	if status, api := changeContractStatus(t, env, admin, cID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, admin, cID, 3, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 已签：带行项目编辑 400。
	status, api := env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "编辑规则合同", "amount": "99.00",
		"products": []map[string]any{{"productId": p1ID, "quantity": 1, "price": "10.00"}},
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "已签合同不可修改行项目") {
		t.Fatalf("已签改行项目：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 已签：仅元数据（名称/起止日期/备注）可改，金额锁定不变。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "已签合同改元数据", "amount": "99.00",
		"startDate": "2026-12-01", "endDate": "2027-12-01", "remark": "签订后订正",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("已签改元数据：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContractRaw(t, env, admin, cID)
	mustOK(t, "已签改后查详情", status, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "已签合同改元数据" || !strings.HasPrefix(fmt.Sprint(detail["startDate"]), "2026-12-01") {
		t.Fatalf("已签元数据修改未生效：name=%v startDate=%v", detail["name"], detail["startDate"])
	}
	if detail["amount"] != "50.00" {
		t.Fatalf("已签金额应锁定 50.00，实际 %v", detail["amount"])
	}

	// 已签不可删（400）。
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", cID), admin, nil); status != http.StatusBadRequest {
		t.Fatalf("删除已签合同：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 已签作废后不可编辑，可删。
	if status, api = changeContractStatus(t, env, admin, cID, 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("已签作废：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api = env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "作废后改名",
	}); status != http.StatusBadRequest {
		t.Fatalf("作废后编辑：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", cID), admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废后删除：HTTP %d code %d msg %q，期望放行", status, api.Code, api.Msg)
	}
	status, _ = getContractRaw(t, env, admin, cID)
	if status != http.StatusNotFound {
		t.Fatalf("作废删除后详情应 404，实际 HTTP %d", status)
	}

	// 待审不可删（400）：新单提交后尝试删除。
	c2 := createContract(t, env, admin, "待审删除合同", map[string]any{"customerId": customerID})
	c2ID := int64(c2["id"].(float64))
	if status, api = changeContractStatus(t, env, admin, c2ID, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", c2ID), admin, nil); status != http.StatusBadRequest {
		t.Fatalf("删除待审合同：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
}

// setupContractScopeEnv 搭建合同数据范围观察场景（部门布局与商机矩阵一致）：
// 各销售在部门 101/102/市场二组名下各持一单合同；观察者在 102 持本人合同。
func setupContractScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64, menuIDs ...int64) (observer string) {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	customer := createCustomer(t, env, admin, "合同范围矩阵宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	createUserByAPI(t, env, admin, "contract_owner_rnd", rndDept, nil)
	createUserByAPI(t, env, admin, "contract_owner_mkt", mktDept, nil)
	createUserByAPI(t, env, admin, "contract_owner_mkt2", mktDept, nil)
	subDept := createDeptByAPI(t, env, admin, "合同二组", mktDept)
	createUserByAPI(t, env, admin, "contract_owner_sub", subDept, nil)

	for _, u := range []string{"contract_owner_rnd", "contract_owner_mkt", "contract_owner_mkt2", "contract_owner_sub"} {
		id := findUserIDByUsername(t, env, admin, u)
		createContract(t, env, admin, fmt.Sprintf("合同_of_%s", u), map[string]any{
			"customerId": customerID, "ownerUserId": id,
		})
	}

	if len(menuIDs) == 0 {
		menuIDs = []int64{crmContractMenuID, 4101, 4102, 4103, 4104, 4105}
	}
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "合同观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": menuIDs,
	})
	observer = "contract_observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerID := findUserIDByUsername(t, env, admin, observer)
	createContract(t, env, admin, fmt.Sprintf("合同_of_%s", observer), map[string]any{
		"customerId": customerID, "ownerUserId": observerID,
	})
	return observer
}

// TestIntegrationContractDataScopeMatrix 合同列表按 5 种数据范围过滤
// （owner-scoped 正体，谓词引擎语义已由 #5/#8 覆盖）。
func TestIntegrationContractDataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupContractScopeEnv(t, env, admin, "ct_all", 1, nil)
		token := env.Login(t, observer, "123456")
		got := visibleContractNames(t, env, token)
		assertContractSet(t, "全部数据", got, []string{
			"合同_of_contract_owner_rnd", "合同_of_contract_owner_mkt", "合同_of_contract_owner_mkt2",
			"合同_of_contract_owner_sub", "合同_of_" + observer,
		})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupContractScopeEnv(t, env, admin, "ct_custom", 2, []int64{101, 103})
		token := env.Login(t, observer, "123456")
		got := visibleContractNames(t, env, token)
		assertContractSet(t, "自定义部门", got, []string{"合同_of_contract_owner_rnd"},
			"合同_of_contract_owner_mkt", "合同_of_contract_owner_mkt2", "合同_of_contract_owner_sub", "合同_of_"+observer)
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupContractScopeEnv(t, env, admin, "ct_dept", 3, nil)
		token := env.Login(t, observer, "123456")
		got := visibleContractNames(t, env, token)
		assertContractSet(t, "本部门", got, []string{
			"合同_of_contract_owner_mkt", "合同_of_contract_owner_mkt2", "合同_of_" + observer,
		}, "合同_of_contract_owner_rnd", "合同_of_contract_owner_sub")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupContractScopeEnv(t, env, admin, "ct_dept_child", 4, nil)
		token := env.Login(t, observer, "123456")
		got := visibleContractNames(t, env, token)
		assertContractSet(t, "本部门及以下", got, []string{
			"合同_of_contract_owner_mkt", "合同_of_contract_owner_mkt2", "合同_of_contract_owner_sub", "合同_of_" + observer,
		}, "合同_of_contract_owner_rnd")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupContractScopeEnv(t, env, admin, "ct_self", 5, nil)
		token := env.Login(t, observer, "123456")
		got := visibleContractNames(t, env, token)
		assertContractSet(t, "仅本人", got, []string{"合同_of_" + observer},
			"合同_of_contract_owner_rnd", "合同_of_contract_owner_mkt", "合同_of_contract_owner_mkt2", "合同_of_contract_owner_sub")
	})
}

// TestIntegrationContractOutsideScope404 范围外详情/修改/状态流转/删除一律 404
// （与列表可见性一致，不泄露存在性）。
func TestIntegrationContractOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupContractScopeEnv(t, env, admin, "ct_404", 5, nil)
	token := env.Login(t, observer, "123456")

	// 取一条观察者范围外的合同（他人的）。
	list, _ := pageContracts(t, env, admin, "&name=合同_of_contract_owner_rnd")
	if len(list) != 1 {
		t.Fatalf("应恰好有一条他人合同，实际 %d", len(list))
	}
	otherID := int64(list[0]["id"].(float64))

	status, _ := getContractRaw(t, env, token, otherID)
	if status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/crm/contract", token, map[string]any{
		"id": otherID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	status, _ = changeContractStatus(t, env, token, otherID, 2, "")
	if status != http.StatusNotFound {
		t.Fatalf("范围外状态流转：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", otherID), token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}
}

// TestIntegrationContractTenantIsolation 租户隔离：租户 2 看不到租户 1 的合同
// 与行项目引用，跨租户详情/修改/状态流转 404，跨租户客户下建合同 404。
func TestIntegrationContractTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	product := createLineItemProduct(t, env, admin, "合同隔离产品", "9.90")
	productID := int64(product["id"].(float64))
	customer := createCustomer(t, env, admin, "合同隔离宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	c := createContract(t, env, admin, "租户一合同", map[string]any{
		"customerId": customerID,
		"products":   []map[string]any{{"productId": productID, "quantity": 1, "price": "9.90"}},
	})
	cID := int64(c["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "合同隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	if _, total := pageContracts(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 合同 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := getContractRaw(t, env, token2, cID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 合同详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/crm/contract", token2, map[string]any{"id": cID, "name": "越权改名"})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 合同：HTTP %d，期望 404", status)
	}
	status, _ = changeContractStatus(t, env, token2, cID, 2, "")
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 流转租户 1 合同状态：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/crm/contract", token2, map[string]any{
		"customerId": customerID, "name": "跨租户合同",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 客户下建合同：HTTP %d，期望 404", status)
	}

	// 跨租户行项目引用不计入：租户 1 的产品仍被引用（删除 400）。
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", productID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("租户 1 产品仍被引用：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 租户 1 数据不受影响。
	_, total := pageContracts(t, env, admin, "")
	if total != 1 {
		t.Fatalf("租户 1 合同 total=%d，期望 1", total)
	}
}

// TestIntegrationContractPermissionDenied 无 crm:contract:* 权限码一律 403：
// 观察者只绑客户页面菜单（401），未绑合同按钮（4100–4105）。
func TestIntegrationContractPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	c := createContract(t, env, admin, "存在但无权查看的合同", map[string]any{"customerId": customerID})
	cID := int64(c["id"].(float64))

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色三", "code": "ct_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "ct_no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "ct_no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "分页", method: http.MethodGet, path: "/crm/contract/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: fmt.Sprintf("/crm/contract/%d", cID)},
		{name: "新建", method: http.MethodPost, path: "/crm/contract", body: map[string]any{"customerId": customerID, "name": "越权合同"}},
		{name: "修改", method: http.MethodPut, path: "/crm/contract", body: map[string]any{"id": cID, "name": "越权改名"}},
		{name: "状态流转", method: http.MethodPut, path: "/crm/contract/status", body: map[string]any{"id": cID, "status": 2}},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/crm/contract/%d", cID)},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// admin 有全部权限码，对照可正常访问。
	if status, api := env.Request(t, http.MethodGet, "/crm/contract/page?pageNo=1&pageSize=10", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询合同分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationContractProductRefBlocksDelete 产品被合同行项目引用后禁止删除（400，
// RefCheckerChain 的合同侧实现生效），合同行项目清空后引用解除、产品可删。
func TestIntegrationContractProductRefBlocksDelete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同引用宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	product := createLineItemProduct(t, env, admin, "合同被引用产品", "9.90")
	productID := int64(product["id"].(float64))

	c := createContract(t, env, admin, "引用合同", map[string]any{
		"customerId": customerID,
		"products":   []map[string]any{{"productId": productID, "quantity": 1, "price": "9.90"}},
	})
	cID := int64(c["id"].(float64))

	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", productID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("删除被合同引用产品：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if !strings.Contains(api.Msg, "合同行项目") {
		t.Fatalf("错误原文应透传（点名合同行项目），实际 %q", api.Msg)
	}

	// 清空行项目后引用解除，产品可删。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/contract", admin, map[string]any{
		"id": cID, "name": "引用合同", "amount": "0.00", "products": []map[string]any{},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("清空合同行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	deleteLineItemProduct(t, env, admin, productID)
}

// TestIntegrationContractFollowUp 合同维度跟进（#16 对 #12 的扩展）：
// 创建挂合同（客户/商机/合同三选一）、可见性跟随合同负责人、合同详情 tab 按
// contractId 筛选、客户 last_follow_time 联动刷新、三选一约束 400、
// 客户/商机维度跟进不回归。
func TestIntegrationContractFollowUp(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同跟进宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 合同归属 mkt 销售（部门 102），观察者（仅本人）看不到。
	// owner 绑仅本人角色（含跟进按钮码）：既验证「合同 owner 可见自己的合同跟进」，
	// 也保证其调用跟进接口有按钮权限。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "合同跟进owner角色", "code": "ct_fu_owner_role", "dataScope": 5,
		"menuIds": []int64{401, 4040, 4041, 4042, 4043},
	})
	createUserByAPI(t, env, admin, "ct_fu_owner", 102, []int64{ownerRoleID})
	ownerID := findUserIDByUsername(t, env, admin, "ct_fu_owner")
	c := createContract(t, env, admin, "跟进合同", map[string]any{
		"customerId": customerID, "ownerUserId": ownerID,
	})
	cID := int64(c["id"].(float64))

	// admin 在合同下建跟进：返回带合同名称。
	status, api := env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"contractId": cID, "content": "合同跟进一", "type": 2,
	})
	mustOK(t, "合同维度建跟进", status, api)
	var fu map[string]any
	if err := json.Unmarshal(api.Data, &fu); err != nil {
		t.Fatalf("解析跟进响应: %v（data=%s）", err, string(api.Data))
	}
	fuID := int64(fu["id"].(float64))
	if fu["contractId"] != float64(cID) {
		t.Fatalf("跟进 contractId=%v，期望 %d", fu["contractId"], cID)
	}
	if fu["contractName"] != "跟进合同" {
		t.Fatalf("跟进应带合同名称，实际 %v", fu["contractName"])
	}

	// 合同跟进刷新所属客户 last_follow_time（spec §4.2：跟进任一子记录都刷新）。
	status, api = getCustomer(t, env, admin, customerID)
	mustOK(t, "查客户详情", status, api)
	var cd map[string]any
	if err := json.Unmarshal(api.Data, &cd); err != nil {
		t.Fatalf("解析客户详情: %v", err)
	}
	if cd["lastFollowTime"] == nil {
		t.Fatal("合同跟进应刷新客户 last_follow_time")
	}

	// 按合同筛选可见；合同 owner（仅本人语义下）可见自己的合同跟进。
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&contractId=%d", cID)); total != 1 {
		t.Fatalf("按合同筛选跟进 total=%d，期望 1", total)
	}
	ownerToken := env.Login(t, "ct_fu_owner", "123456")
	list, total := pageFollowUps(t, env, ownerToken, fmt.Sprintf("&contractId=%d", cID))
	if total != 1 || len(list) != 1 {
		t.Fatalf("合同 owner 视角跟进 total=%d，期望 1", total)
	}

	// 合同下跟进的可见性跟随合同 owner：部门 101 的他人角色（仅本人）404。
	createUserByAPI(t, env, admin, "ct_fu_stranger", 101, nil)
	observerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "合同跟进观察者角色", "code": "ct_fu_view", "dataScope": 5,
		"menuIds": []int64{401, 4040, 4041},
	})
	createUserByAPI(t, env, admin, "ct_fu_observer", 101, []int64{observerRoleID})
	observerToken := env.Login(t, "ct_fu_observer", "123456")
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/follow-up/%d", fuID), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外合同跟进详情：HTTP %d，期望 404", status)
	}
	if _, total := pageFollowUps(t, env, observerToken, fmt.Sprintf("&contractId=%d", cID)); total != 0 {
		t.Fatalf("范围外合同跟进列表 total=%d，期望 0", total)
	}

	// 三选一约束：多挂（客户+合同 / 商机+合同 / 三全）400；都不挂 400。
	opportunity := createOpportunity(t, env, admin, "跟进用商机", map[string]any{"customerId": customerID})
	opID := int64(opportunity["id"].(float64))
	for name, payload := range map[string]map[string]any{
		"客户+合同": {"customerId": customerID, "contractId": cID, "content": "多挂跟进"},
		"商机+合同": {"opportunityId": opID, "contractId": cID, "content": "多挂跟进"},
		"三全挂":   {"customerId": customerID, "opportunityId": opID, "contractId": cID, "content": "多挂跟进"},
		"全不挂":   {"content": "无挂跟进"},
	} {
		status, api := env.Request(t, http.MethodPost, "/crm/follow-up", admin, payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s建跟进：HTTP %d code %d msg %q，期望 400", name, status, api.Code, api.Msg)
		}
	}

	// 客户/商机维度不回归：各自建跟进照常，与合同维度筛选互不串扰。
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": customerID, "content": "客户跟进一",
	})
	mustOK(t, "客户维度建跟进", status, api)
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"opportunityId": opID, "content": "商机跟进一",
	})
	mustOK(t, "商机维度建跟进", status, api)
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 1 {
		t.Fatalf("客户维度筛选 total=%d，期望 1（合同跟进不混入客户筛选）", total)
	}
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&opportunityId=%d", opID)); total != 1 {
		t.Fatalf("商机维度筛选 total=%d，期望 1", total)
	}
	// 合同跟进删除照常（创建人本人放行）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/follow-up/%d", fuID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除合同跟进：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}
