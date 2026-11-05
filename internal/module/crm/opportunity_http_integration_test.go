//go:build integration

// 商机 HTTP 集成测试（issue #15）：真实 router + 生产中间件（JWT/权限/租户插件）
// + 真实 MySQL，在 HTTP 契约上验证 CRUD 全链路、SJ 编号、阶段状态机、行项目、
// 数据范围矩阵、产品引用约束与商机维度跟进。
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

// crmOpportunityMenuID 是种子中的商机管理菜单 ID（含 crm:opportunity:query 权限码）。
const crmOpportunityMenuID = int64(4080)

// opportunityNoRe 校验编号形状：SJ + yyyyMMdd + 至少 4 位序号（超 9999 自然扩位）。
var opportunityNoRe = regexp.MustCompile(`^SJ\d{8}\d{4,}$`)

// createOpportunity 创建商机并断言成功，返回落库后的完整商机 JSON。
func createOpportunity(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/opportunity", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建商机 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建商机响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageOpportunities 查询商机分页并返回 list 与 total。
func pageOpportunities(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/opportunity/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询商机分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析商机分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getOpportunity 查询详情，返回 HTTP 状态、业务码与原始 data。
func getOpportunity(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/opportunity/%d", id), token, nil)
}

// advanceOpportunityStage 推进阶段，返回 HTTP 状态与业务响应。
func advanceOpportunityStage(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, stage int8) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/opportunity/stage", token, map[string]any{"id": id, "stage": stage})
}

// visibleOpportunityNames 按观察者视角查商机分页，返回可见商机名称集合。
func visibleOpportunityNames(t *testing.T, env *testutil.IntegrationEnv, token string) map[string]bool {
	t.Helper()
	list, _ := pageOpportunities(t, env, token, "")
	got := make(map[string]bool, len(list))
	for _, row := range list {
		got[row["name"].(string)] = true
	}
	return got
}

// assertOpportunitySet 断言可见集合与期望完全一致（want 全可见，notWant 全不可见）。
func assertOpportunitySet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：商机 %q 应可见，实际不可见（可见集合 %v）", phase, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：商机 %q 不应可见，实际可见", phase, name)
		}
	}
}

// createLineItemProduct 创建可被行项目引用的产品。不复用 product_http_integration_test.go
// 的 createProduct/deleteProduct：codegen 编译检查（compile_check_test）会裁剪该文件，
// 商机测试文件须对产品 API 自足（自带分类创建）。
func createLineItemProduct(t *testing.T, env *testutil.IntegrationEnv, token, name, price string) map[string]any {
	t.Helper()
	cat := func() map[string]any {
		status, api := env.Request(t, http.MethodPost, "/crm/product-category", token, map[string]any{"name": "行项目分类-" + name})
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("创建产品分类：HTTP %d code %d msg %q", status, api.Code, api.Msg)
		}
		var row map[string]any
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析分类响应: %v（data=%s）", err, string(api.Data))
		}
		return row
	}()
	categoryID := int64(cat["id"].(float64))
	status, api := env.Request(t, http.MethodPost, "/crm/product", token, map[string]any{
		"categoryId": categoryID, "name": name, "code": "SKU-" + name, "price": price,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建产品 %q：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建产品响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// deleteLineItemProduct 删除产品并断言成功（自足原因见 createLineItemProduct）。
func deleteLineItemProduct(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", id), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除产品 %d：HTTP %d code %d msg %q", id, status, api.Code, api.Msg)
	}
}

// TestIntegrationOpportunityCRUDAndNumber 商机 CRUD 全链路与编号规则：
// 编号 SJ+yyyyMMdd+4 位序号、租户内唯一、创建时自动生成且不可由客户端指定、
// 默认负责人与部门快照、编辑不动编号与阶段、软删除后不可见。
func TestIntegrationOpportunityCRUDAndNumber(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "商机宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 创建：编号自动生成，负责人缺省归属当前用户并快照其部门。
	first := createOpportunity(t, env, admin, "商机甲", map[string]any{
		"customerId": customerID, "expectedAmount": "5000.00",
		"expectedCloseDate": "2026-12-31", "remark": "第一条商机纵切",
	})
	no1, _ := first["opportunityNo"].(string)
	if !opportunityNoRe.MatchString(no1) {
		t.Fatalf("商机编号 %q 不符合 SJ+yyyyMMdd+4位序号", no1)
	}
	if want := "SJ" + time.Now().Format("20060102") + "0001"; no1 != want {
		t.Fatalf("租户当日首号应为 %s，实际 %s", want, no1)
	}
	if id := first["ownerUserId"]; id != float64(1) {
		t.Fatalf("缺省负责人应归属当前用户 admin(id=1)，实际 %v", id)
	}
	if dept := first["ownerDeptId"]; dept != float64(100) {
		t.Fatalf("负责人部门快照应为 100（总公司），实际 %v", dept)
	}
	if first["stage"] != float64(1) {
		t.Fatalf("缺省阶段应为 1（初步），实际 %v", first["stage"])
	}
	if first["customerName"] != "商机宿主客户" {
		t.Fatalf("创建响应应带客户名称，实际 customerName=%v", first["customerName"])
	}
	if first["expectedAmount"] != "5000.00" {
		t.Fatalf("预计金额应回显十进制字符串，实际 %v", first["expectedAmount"])
	}

	second := createOpportunity(t, env, admin, "商机乙", map[string]any{"customerId": customerID})
	no2, _ := second["opportunityNo"].(string)
	if no2 == no1 || !opportunityNoRe.MatchString(no2) {
		t.Fatalf("第二个编号 %s 应与首号 %s 不同且符合形状", no2, no1)
	}

	// 客户端传入 opportunityNo 无效：编号只能由服务端发放。
	forged := createOpportunity(t, env, admin, "商机丙", map[string]any{
		"customerId": customerID, "opportunityNo": "SJ999999999999",
	})
	if forged["opportunityNo"] == "SJ999999999999" {
		t.Fatal("客户端指定的 opportunityNo 不应被采纳")
	}

	// 详情回读：客户名称、负责人昵称、阶段。
	status, api := getOpportunity(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询商机详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "商机甲" || detail["stage"] != float64(1) {
		t.Fatalf("详情字段不符：name=%v stage=%v", detail["name"], detail["stage"])
	}
	if detail["ownerUserName"] != "超级管理员" || detail["ownerDeptName"] != "总公司" {
		t.Fatalf("详情应带负责人昵称与部门名称，实际 %v / %v", detail["ownerUserName"], detail["ownerDeptName"])
	}

	// 更新：名称/预计金额生效；编号不变；编辑接口不带阶段字段，阶段不被改动。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": first["id"], "name": "商机甲改", "expectedAmount": "6000.50",
		"expectedCloseDate": "", "remark": "改过备注", "opportunityNo": "SJ000000000000", "stage": 6,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新商机：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getOpportunity(t, env, admin, int64(first["id"].(float64)))
	mustOK(t, "更新后查询详情", status, api)
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "商机甲改" || detail["expectedAmount"] != "6000.50" {
		t.Fatalf("更新未生效：name=%v expectedAmount=%v", detail["name"], detail["expectedAmount"])
	}
	if detail["opportunityNo"] != no1 {
		t.Fatalf("更新不应改动编号：期望 %s 实际 %v", no1, detail["opportunityNo"])
	}
	if detail["stage"] != float64(1) {
		t.Fatalf("编辑接口不应改阶段：期望 1，实际 %v", detail["stage"])
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/opportunity/%d", int64(second["id"].(float64))), admin, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除商机：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getOpportunity(t, env, admin, int64(second["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total := pageOpportunities(t, env, admin, "")
	if total != 2 {
		t.Fatalf("删除后列表 total=%d，期望 2（软删除不可见）", total)
	}
}

// createOpportunityOnce 在独立 goroutine 中发一次创建请求：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），错误带回主 goroutine 断言。
func createOpportunityOnce(env *testutil.IntegrationEnv, token string, customerID int64, name string) (string, error) {
	raw, err := json.Marshal(map[string]any{"customerId": customerID, "name": name})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/crm/opportunity"), bytes.NewReader(raw))
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
	var o struct {
		OpportunityNo string `json:"opportunityNo"`
	}
	if err := json.Unmarshal(api.Data, &o); err != nil {
		return "", err
	}
	return o.OpportunityNo, nil
}

// TestIntegrationOpportunityNumberUniqueUnderConcurrency 并发创建不重号：
// 同租户同日 N 个并发请求，编号两两不同且按序发放（crm_sequence 原子发放，照 #8）。
func TestIntegrationOpportunityNumberUniqueUnderConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "并发商机宿主", nil)
	customerID := int64(customer["id"].(float64))

	const n = 20
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			no, err := createOpportunityOnce(env, admin, customerID, fmt.Sprintf("并发商机%d", i))
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
	_, total := pageOpportunities(t, env, admin, "&name=并发商机")
	if total != n {
		t.Fatalf("并发创建后按名称筛选 total=%d，期望 %d", total, n)
	}
}

// TestIntegrationOpportunityLineItems 行项目全链路：创建带行项目、详情返回行项目
// 与合计（金额服务端计算）、整体替换、清空、客户端提交的 amount 不被采纳。
func TestIntegrationOpportunityLineItems(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "行项目宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	p1 := createLineItemProduct(t, env, admin, "行项目产品一", "1234.56")
	p2 := createLineItemProduct(t, env, admin, "行项目产品二", "99.99")
	p1ID := int64(p1["id"].(float64))
	p2ID := int64(p2["id"].(float64))

	// 创建带行项目：金额 = 数量 × 单价（2 × 1234.56 = 2469.12；3 × 99.99 = 299.97），合计 2769.09。
	o := createOpportunity(t, env, admin, "带行项目商机", map[string]any{
		"customerId": customerID,
		"products": []map[string]any{
			{"productId": p1ID, "quantity": 2, "price": "1234.56", "amount": "999999.99"},
			{"productId": p2ID, "quantity": 3, "price": "99.99"},
		},
	})
	opID := int64(o["id"].(float64))
	if o["totalAmount"] != "2769.09" {
		t.Fatalf("创建响应合计 = %v，期望 2769.09", o["totalAmount"])
	}

	// 详情回读行项目与合计；客户端乱传的 amount 不被采纳。
	status, api := getOpportunity(t, env, admin, opID)
	mustOK(t, "查询行项目商机详情", status, api)
	var detail struct {
		Products []struct {
			ProductID   int64  `json:"productId"`
			ProductName string `json:"productName"`
			Quantity    int64  `json:"quantity"`
			Price       string `json:"price"`
			Amount      string `json:"amount"`
		} `json:"products"`
		TotalAmount string `json:"totalAmount"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v（data=%s）", err, string(api.Data))
	}
	if len(detail.Products) != 2 || detail.TotalAmount != "2769.09" {
		t.Fatalf("详情行项目数=%d 合计=%q，期望 2 行合计 2769.09", len(detail.Products), detail.TotalAmount)
	}
	for _, p := range detail.Products {
		if p.ProductName == "" {
			t.Fatalf("行项目应带产品名称：%+v", p)
		}
	}
	if detail.Products[0].ProductID == p1ID && detail.Products[0].Amount != "2469.12" {
		t.Fatalf("行项目金额应服务端计算 2469.12，实际 %q", detail.Products[0].Amount)
	}

	// 整体替换行项目：只留产品一，数量 5（5 × 1234.56 = 6172.80）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": opID, "name": "带行项目商机",
		"products": []map[string]any{{"productId": p1ID, "quantity": 5, "price": "1234.56"}},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("替换行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getOpportunity(t, env, admin, opID)
	mustOK(t, "替换后查详情", status, api)
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if len(detail.Products) != 1 || detail.TotalAmount != "6172.80" {
		t.Fatalf("替换后行项目数=%d 合计=%q，期望 1 行合计 6172.80", len(detail.Products), detail.TotalAmount)
	}

	// 不传 products（nil）：行项目保持不变。
	upStatus, upAPI = env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": opID, "name": "带行项目商机改名",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("不带行项目更新：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getOpportunity(t, env, admin, opID)
	mustOK(t, "更新后查详情", status, api)
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if len(detail.Products) != 1 {
		t.Fatalf("products 缺省不应清空行项目：实际 %d 行", len(detail.Products))
	}

	// 传空数组：清空行项目，合计 0.00（无行项目允许，预计金额手填）。
	upStatus, upAPI = env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": opID, "name": "带行项目商机", "products": []map[string]any{},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("清空行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getOpportunity(t, env, admin, opID)
	mustOK(t, "清空后查详情", status, api)
	// 复用解码结构体：0 行时 products 被 omitempty 省略，须先清掉上一轮残留再解。
	detail.Products = nil
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if len(detail.Products) != 0 || detail.TotalAmount != "0.00" {
		t.Fatalf("清空后行项目数=%d 合计=%q，期望 0 行合计 0.00", len(detail.Products), detail.TotalAmount)
	}
}

// TestIntegrationOpportunityLineItemValidation 行项目非法输入 400：
// 数量非正整数、单价为负、产品不存在、产品重复；非法输入不落库。
func TestIntegrationOpportunityLineItemValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "行项目校验宿主", nil)
	customerID := int64(customer["id"].(float64))
	p1 := createLineItemProduct(t, env, admin, "校验产品一", "1.00")
	p1ID := int64(p1["id"].(float64))

	cases := []struct {
		name     string
		products []map[string]any
		wantMsg  string
	}{
		{name: "数量为 0", products: []map[string]any{{"productId": p1ID, "quantity": 0, "price": "1.00"}}, wantMsg: "数量"},
		{name: "数量为负", products: []map[string]any{{"productId": p1ID, "quantity": -2, "price": "1.00"}}, wantMsg: "数量"},
		{name: "单价为负", products: []map[string]any{{"productId": p1ID, "quantity": 1, "price": "-0.01"}}, wantMsg: "单价"},
		{name: "产品不存在", products: []map[string]any{{"productId": 999999, "quantity": 1, "price": "1.00"}}, wantMsg: "不存在"},
		{name: "产品重复", products: []map[string]any{
			{"productId": p1ID, "quantity": 1, "price": "1.00"},
			{"productId": p1ID, "quantity": 2, "price": "1.00"},
		}, wantMsg: "重复"},
		{name: "金额越界", products: []map[string]any{{"productId": p1ID, "quantity": 100000, "price": "9999999999.99"}}, wantMsg: "金额"},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/opportunity", admin, map[string]any{
			"customerId": customerID, "name": "行项目校验商机", "products": tc.products,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if !strings.Contains(api.Msg, tc.wantMsg) {
			t.Fatalf("%s：文案应包含 %q，实际 %q", tc.name, tc.wantMsg, api.Msg)
		}
	}
	// 非法输入不落库。
	_, total := pageOpportunities(t, env, admin, "&name=行项目校验商机")
	if total != 0 {
		t.Fatalf("非法行项目商机被落库：total=%d，期望 0", total)
	}
}

// TestIntegrationOpportunityStageMachine 阶段状态机：进行中可互相推进（含回退）、
// 推进到赢单/输单、终态锁定（400）、非法阶段 400、编辑接口不携带阶段。
func TestIntegrationOpportunityStageMachine(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "阶段宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	o := createOpportunity(t, env, admin, "阶段商机", map[string]any{"customerId": customerID})
	opID := int64(o["id"].(float64))

	// 1→3→2 回退合法。
	for _, stage := range []int8{3, 2} {
		status, api := advanceOpportunityStage(t, env, admin, opID, stage)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("推进到阶段 %d：HTTP %d code %d msg %q", stage, status, api.Code, api.Msg)
		}
	}
	// 2→5 赢单：终态。
	status, api := advanceOpportunityStage(t, env, admin, opID, 5)
	mustOK(t, "推进到赢单", status, api)

	// 终态锁定：赢单后不可再变（同阶段幂等放行）。
	for _, stage := range []int8{1, 2, 3, 4, 6} {
		status, api := advanceOpportunityStage(t, env, admin, opID, stage)
		if status != http.StatusBadRequest {
			t.Fatalf("赢单后推进到 %d：HTTP %d code %d msg %q，期望 400", stage, status, api.Code, api.Msg)
		}
		if !strings.Contains(api.Msg, "赢单") {
			t.Fatalf("终态文案应点名赢单，实际 %q", api.Msg)
		}
	}
	status, api = advanceOpportunityStage(t, env, admin, opID, 5)
	mustOK(t, "终态同阶段幂等放行", status, api)

	// 详情阶段已更新为 5。
	status, api = getOpportunity(t, env, admin, opID)
	mustOK(t, "推进后查详情", status, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["stage"] != float64(5) {
		t.Fatalf("推进后阶段应 5，实际 %v", detail["stage"])
	}

	// 输单同理：另一商机推进到 6 后锁定。
	o2 := createOpportunity(t, env, admin, "输单商机", map[string]any{"customerId": customerID})
	op2ID := int64(o2["id"].(float64))
	if status, api = advanceOpportunityStage(t, env, admin, op2ID, 6); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("推进到输单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api = advanceOpportunityStage(t, env, admin, op2ID, 1); status != http.StatusBadRequest {
		t.Fatalf("输单后推进：HTTP %d，期望 400", status)
	}

	// 非法阶段 400（不落库）。
	if status, api = advanceOpportunityStage(t, env, admin, opID, 9); status != http.StatusBadRequest {
		t.Fatalf("非法阶段 9：HTTP %d，期望 400", status)
	}

	// 终态后其他字段仍可编辑（口径：终态锁定阶段归属，不冻结事后订正）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": opID, "name": "赢单商机改备注", "remark": "赢单后订正",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("终态后编辑其他字段：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
}

// TestIntegrationOpportunityValidation 商机基础校验：名称必填/超长、阶段非法、
// 客户必填、创建即终态拒绝、列表筛选非法阶段 400。
func TestIntegrationOpportunityValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "校验宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "名称为空", payload: map[string]any{"customerId": customerID, "name": ""}},
		{name: "客户缺失", payload: map[string]any{"name": "无客户商机"}},
		{name: "名称超长", payload: map[string]any{"customerId": customerID, "name": strings.Repeat("长", 129)}},
		{name: "阶段非法", payload: map[string]any{"customerId": customerID, "name": "非法阶段商机", "stage": 9}},
		{name: "创建即赢单", payload: map[string]any{"customerId": customerID, "name": "直建赢单", "stage": 5}},
		{name: "预计成交日期非法", payload: map[string]any{"customerId": customerID, "name": "日期非法", "expectedCloseDate": "明年"}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/opportunity", admin, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if api.Msg == "" {
			t.Fatalf("%s：应返回中文错误信息", tc.name)
		}
	}

	// 客户不存在/不可见：404（owner-scoped 口径——看不见的即不存在，不泄露存在性）。
	status, api := env.Request(t, http.MethodPost, "/crm/opportunity", admin, map[string]any{
		"customerId": 999999, "name": "孤儿商机",
	})
	if status != http.StatusNotFound {
		t.Fatalf("客户不存在建商机：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 列表筛选 stage=9 同样拒绝（400），绝不静默当「无此筛选」。
	status, api = env.Request(t, http.MethodGet, "/crm/opportunity/page?pageNo=1&pageSize=10&stage=9", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 stage=9：HTTP %d code %d，期望 400", status, api.Code)
	}
}

// setupOpportunityScopeEnv 搭建商机数据范围观察场景（部门布局与客户矩阵测试一致）：
// 各销售在部门 101/102/市场二组名下各持一单商机；观察者在 102 持本人商机。
func setupOpportunityScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64, menuIDs ...int64) (observer string) {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	customer := createCustomer(t, env, admin, "范围矩阵宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	createUserByAPI(t, env, admin, "opp_owner_rnd", rndDept, nil)
	createUserByAPI(t, env, admin, "opp_owner_mkt", mktDept, nil)
	createUserByAPI(t, env, admin, "opp_owner_mkt2", mktDept, nil)
	subDept := createDeptByAPI(t, env, admin, "商机二组", mktDept)
	createUserByAPI(t, env, admin, "opp_owner_sub", subDept, nil)

	for _, u := range []string{"opp_owner_rnd", "opp_owner_mkt", "opp_owner_mkt2", "opp_owner_sub"} {
		id := findUserIDByUsername(t, env, admin, u)
		createOpportunity(t, env, admin, fmt.Sprintf("商机_of_%s", u), map[string]any{
			"customerId": customerID, "ownerUserId": id,
		})
	}

	if len(menuIDs) == 0 {
		menuIDs = []int64{crmOpportunityMenuID, 4081, 4082, 4083, 4084, 4085}
	}
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "商机观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": menuIDs,
	})
	observer = "opp_observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerID := findUserIDByUsername(t, env, admin, observer)
	createOpportunity(t, env, admin, fmt.Sprintf("商机_of_%s", observer), map[string]any{
		"customerId": customerID, "ownerUserId": observerID,
	})
	return observer
}

// TestIntegrationOpportunityDataScopeMatrix 商机列表按 5 种数据范围过滤
// （owner-scoped 正体，谓词引擎语义已由 #5/#8 覆盖）。
func TestIntegrationOpportunityDataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupOpportunityScopeEnv(t, env, admin, "opp_all", 1, nil)
		token := env.Login(t, observer, "123456")
		got := visibleOpportunityNames(t, env, token)
		assertOpportunitySet(t, "全部数据", got, []string{
			"商机_of_opp_owner_rnd", "商机_of_opp_owner_mkt", "商机_of_opp_owner_mkt2",
			"商机_of_opp_owner_sub", "商机_of_" + observer,
		})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupOpportunityScopeEnv(t, env, admin, "opp_custom", 2, []int64{101, 103})
		token := env.Login(t, observer, "123456")
		got := visibleOpportunityNames(t, env, token)
		assertOpportunitySet(t, "自定义部门", got, []string{"商机_of_opp_owner_rnd"},
			"商机_of_opp_owner_mkt", "商机_of_opp_owner_mkt2", "商机_of_opp_owner_sub", "商机_of_"+observer)
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupOpportunityScopeEnv(t, env, admin, "opp_dept", 3, nil)
		token := env.Login(t, observer, "123456")
		got := visibleOpportunityNames(t, env, token)
		assertOpportunitySet(t, "本部门", got, []string{
			"商机_of_opp_owner_mkt", "商机_of_opp_owner_mkt2", "商机_of_" + observer,
		}, "商机_of_opp_owner_rnd", "商机_of_opp_owner_sub")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupOpportunityScopeEnv(t, env, admin, "opp_dept_child", 4, nil)
		token := env.Login(t, observer, "123456")
		got := visibleOpportunityNames(t, env, token)
		assertOpportunitySet(t, "本部门及以下", got, []string{
			"商机_of_opp_owner_mkt", "商机_of_opp_owner_mkt2", "商机_of_opp_owner_sub", "商机_of_" + observer,
		}, "商机_of_opp_owner_rnd")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupOpportunityScopeEnv(t, env, admin, "opp_self", 5, nil)
		token := env.Login(t, observer, "123456")
		got := visibleOpportunityNames(t, env, token)
		assertOpportunitySet(t, "仅本人", got, []string{"商机_of_" + observer},
			"商机_of_opp_owner_rnd", "商机_of_opp_owner_mkt", "商机_of_opp_owner_mkt2", "商机_of_opp_owner_sub")
	})
}

// TestIntegrationOpportunityOutsideScope404 范围外详情/修改/删除/阶段推进一律 404
// （与列表可见性一致，不泄露存在性）。
func TestIntegrationOpportunityOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupOpportunityScopeEnv(t, env, admin, "opp_404", 5, nil)
	token := env.Login(t, observer, "123456")

	// 取一条观察者范围外的商机（他人的）。
	list, _ := pageOpportunities(t, env, admin, "&name=商机_of_opp_owner_rnd")
	if len(list) != 1 {
		t.Fatalf("应恰好有一条他人商机，实际 %d", len(list))
	}
	otherID := int64(list[0]["id"].(float64))

	status, _ := getOpportunity(t, env, token, otherID)
	if status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/crm/opportunity", token, map[string]any{
		"id": otherID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	status, _ = advanceOpportunityStage(t, env, token, otherID, 5)
	if status != http.StatusNotFound {
		t.Fatalf("范围外阶段推进：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/opportunity/%d", otherID), token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}
}

// TestIntegrationOpportunityProductRefBlocksDelete 产品被商机行项目引用后禁止删除（400），
// 商机删除后引用解除、产品可删（ProductRefCountChecker，#11 扩展点的 #15 实现）。
func TestIntegrationOpportunityProductRefBlocksDelete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "引用宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	product := createLineItemProduct(t, env, admin, "被引用产品", "9.90")
	productID := int64(product["id"].(float64))

	o := createOpportunity(t, env, admin, "引用商机", map[string]any{
		"customerId": customerID,
		"products":   []map[string]any{{"productId": productID, "quantity": 1, "price": "9.90"}},
	})
	opID := int64(o["id"].(float64))

	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", productID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("删除被引用产品：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if !strings.Contains(api.Msg, "商机行项目") {
		t.Fatalf("错误原文应透传（点名商机行项目），实际 %q", api.Msg)
	}

	// 替换行项目后引用解除，产品可删。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/opportunity", admin, map[string]any{
		"id": opID, "name": "引用商机", "products": []map[string]any{},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("清空行项目：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	deleteLineItemProduct(t, env, admin, productID)
}

// TestIntegrationOpportunityTenantIsolation 租户隔离：租户 2 看不到租户 1 的商机
// 与行项目引用，跨租户详情/修改 404，跨租户客户下建商机 404。
func TestIntegrationOpportunityTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	product := createLineItemProduct(t, env, admin, "隔离产品", "9.90")
	productID := int64(product["id"].(float64))
	customer := createCustomer(t, env, admin, "隔离宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	o := createOpportunity(t, env, admin, "租户一商机", map[string]any{
		"customerId": customerID,
		"products":   []map[string]any{{"productId": productID, "quantity": 1, "price": "9.90"}},
	})
	opID := int64(o["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "商机隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	if _, total := pageOpportunities(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 商机 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := getOpportunity(t, env, token2, opID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 商机详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/crm/opportunity", token2, map[string]any{"id": opID, "name": "越权改名"})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 商机：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/crm/opportunity", token2, map[string]any{
		"customerId": customerID, "name": "跨租户商机",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 客户下建商机：HTTP %d，期望 404", status)
	}

	// 跨租户行项目引用不计入：租户 2 可建同 ID 产品行项目不受影响、
	// 租户 1 的产品仍被引用（删除 400），租户 2 建自己的产品+行项目后可正常删除。
	tenant2Product := createLineItemProduct(t, env, token2, "租户二产品", "1.00")
	t2ProductID := int64(tenant2Product["id"].(float64))
	tenant2Customer := createCustomer(t, env, token2, "租户二客户", nil)
	t2CustomerID := int64(tenant2Customer["id"].(float64))
	createOpportunity(t, env, token2, "租户二商机", map[string]any{
		"customerId": t2CustomerID,
		"products":   []map[string]any{{"productId": t2ProductID, "quantity": 1, "price": "1.00"}},
	})
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", productID), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("租户 1 产品仍被引用：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 租户 1 数据不受影响。
	_, total := pageOpportunities(t, env, admin, "")
	if total != 1 {
		t.Fatalf("租户 1 商机 total=%d，期望 1", total)
	}
}

// TestIntegrationOpportunityPermissionDenied 无 crm:opportunity:* 权限码一律 403：
// 观察者只绑客户页面菜单（401），未绑商机按钮（4080–4085）。
func TestIntegrationOpportunityPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	o := createOpportunity(t, env, admin, "存在但无权查看的商机", map[string]any{"customerId": customerID})
	opID := int64(o["id"].(float64))

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色二", "code": "opp_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "opp_no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "opp_no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "分页", method: http.MethodGet, path: "/crm/opportunity/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: fmt.Sprintf("/crm/opportunity/%d", opID)},
		{name: "新建", method: http.MethodPost, path: "/crm/opportunity", body: map[string]any{"customerId": customerID, "name": "越权商机"}},
		{name: "修改", method: http.MethodPut, path: "/crm/opportunity", body: map[string]any{"id": opID, "name": "越权改名"}},
		{name: "阶段推进", method: http.MethodPut, path: "/crm/opportunity/stage", body: map[string]any{"id": opID, "stage": 2}},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/crm/opportunity/%d", opID)},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// admin 有全部权限码，对照可正常访问。
	if status, api := env.Request(t, http.MethodGet, "/crm/opportunity/page?pageNo=1&pageSize=10", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询商机分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationOpportunityFollowUp 商机维度跟进（#15 对 #12 的扩展）：
// 创建挂商机（二选一）、可见性跟随商机负责人、商机详情 tab 按 opportunityId 筛选、
// 客户 last_follow_time 联动刷新、二选一约束 400、客户维度跟进不回归。
func TestIntegrationOpportunityFollowUp(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "跟进宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 商机归属 mkt 销售（部门 102），观察者（仅本人）看不到。
	// owner 绑仅本人角色（含跟进按钮码）：既验证「商机 owner 可见自己的商机跟进」，
	// 也保证其调用跟进接口有按钮权限。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "跟进owner角色", "code": "opp_fu_owner_role", "dataScope": 5,
		"menuIds": []int64{401, 4040, 4041, 4042, 4043},
	})
	createUserByAPI(t, env, admin, "opp_fu_owner", 102, []int64{ownerRoleID})
	ownerID := findUserIDByUsername(t, env, admin, "opp_fu_owner")
	o := createOpportunity(t, env, admin, "跟进商机", map[string]any{
		"customerId": customerID, "ownerUserId": ownerID,
	})
	opID := int64(o["id"].(float64))

	// admin 在商机下建跟进：返回带商机名称。
	status, api := env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"opportunityId": opID, "content": "商机跟进一", "type": 2,
	})
	mustOK(t, "商机维度建跟进", status, api)
	var fu map[string]any
	if err := json.Unmarshal(api.Data, &fu); err != nil {
		t.Fatalf("解析跟进响应: %v（data=%s）", err, string(api.Data))
	}
	fuID := int64(fu["id"].(float64))
	if fu["opportunityId"] != float64(opID) {
		t.Fatalf("跟进 opportunityId=%v，期望 %d", fu["opportunityId"], opID)
	}
	if fu["opportunityName"] != "跟进商机" {
		t.Fatalf("跟进应带商机名称，实际 %v", fu["opportunityName"])
	}
	if fu["type"] != float64(2) {
		t.Fatalf("跟进类型应 2（拜访），实际 %v", fu["type"])
	}

	// 商机跟进刷新所属客户 last_follow_time（spec §4.2：跟进任一子记录都刷新）。
	status, api = getCustomer(t, env, admin, customerID)
	mustOK(t, "查客户详情", status, api)
	var cd map[string]any
	if err := json.Unmarshal(api.Data, &cd); err != nil {
		t.Fatalf("解析客户详情: %v", err)
	}
	if cd["lastFollowTime"] == nil {
		t.Fatal("商机跟进应刷新客户 last_follow_time")
	}

	// 按商机筛选可见；商机 owner（仅本人语义下）可见自己的商机跟进。
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&opportunityId=%d", opID)); total != 1 {
		t.Fatalf("按商机筛选跟进 total=%d，期望 1", total)
	}
	ownerToken := env.Login(t, "opp_fu_owner", "123456")
	list, total := pageFollowUps(t, env, ownerToken, fmt.Sprintf("&opportunityId=%d", opID))
	if total != 1 || len(list) != 1 {
		t.Fatalf("商机 owner 视角跟进 total=%d，期望 1", total)
	}

	// 商机下跟进的可见性跟随商机 owner：部门 101 的他人角色（仅本人）404。
	createUserByAPI(t, env, admin, "opp_fu_stranger", 101, nil)
	observerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "跟进观察者角色", "code": "opp_fu_view", "dataScope": 5,
		"menuIds": []int64{401, 4040, 4041},
	})
	createUserByAPI(t, env, admin, "opp_fu_observer", 101, []int64{observerRoleID})
	observerToken := env.Login(t, "opp_fu_observer", "123456")
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/follow-up/%d", fuID), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外商机跟进详情：HTTP %d，期望 404", status)
	}
	if _, total := pageFollowUps(t, env, observerToken, fmt.Sprintf("&opportunityId=%d", opID)); total != 0 {
		t.Fatalf("范围外商机跟进列表 total=%d，期望 0", total)
	}

	// 二选一约束：同时挂客户与商机 400；都不挂 400。
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": customerID, "opportunityId": opID, "content": "双挂跟进",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("双挂跟进：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"content": "无挂跟进",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("无挂跟进：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 客户维度不回归：customerId 建跟进照常，且与商机维度筛选互不串扰。
	status, api = env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": customerID, "content": "客户跟进一",
	})
	mustOK(t, "客户维度建跟进", status, api)
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 1 {
		t.Fatalf("客户维度筛选 total=%d，期望 1（商机跟进不混入客户筛选）", total)
	}
	// 商机跟进删除照常（创建人本人放行）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/follow-up/%d", fuID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除商机跟进：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}
