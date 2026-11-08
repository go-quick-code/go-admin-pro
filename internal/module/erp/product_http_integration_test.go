//go:build integration

// 产品域 HTTP 集成测试（issue #88，docs/erp-mvp-spec.md §3.1/§4.9/§5）：
// 产品 CRUD（条码/规格/三个参考价/保质期/重量）、分类与单位存在性校验、
// 分类/单位被产品引用禁删 409（spec §4.9）、qty 定点字段落库往返。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createProduct 创建产品并断言成功，返回落库后的完整产品 JSON。
func createProduct(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/product", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建产品 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建产品响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageProducts 查询产品分页并返回 list 与 total。
func pageProducts(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/product/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询产品分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析产品分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationProductCRUD 产品 CRUD 全链路：分类/单位关联、三个 Money 参考价、
// 保质期/重量展示字段、分页筛选、更新、软删除。
func TestIntegrationProductCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	category := createCategory(t, env, token, "产品域分类", nil)
	categoryID := int64(category["id"].(float64))
	unit := createUnit(t, env, token, "个", nil)
	unitID := int64(unit["id"].(float64))

	first := createProduct(t, env, token, "无线鼠标", map[string]any{
		"categoryId":    categoryID,
		"unitId":        unitID,
		"barCode":       "6901234567890",
		"standard":      "黑色/2.4G",
		"expiryDay":     730,
		"weight":        "0.150",
		"purchasePrice": "25.50",
		"salePrice":     "59.90",
		"minPrice":      "45.00",
		"remark":        "第一条纵切",
		"status":        0,
	})
	productID := int64(first["id"].(float64))
	if first["categoryId"] != float64(categoryID) || first["unitId"] != float64(unitID) {
		t.Fatalf("创建回读 categoryId/unitId 不符：%v/%v", first["categoryId"], first["unitId"])
	}
	if first["purchasePrice"] != "25.50" || first["salePrice"] != "59.90" || first["minPrice"] != "45.00" {
		t.Fatalf("参考价应输出十进制字符串：%v/%v/%v", first["purchasePrice"], first["salePrice"], first["minPrice"])
	}
	if first["weight"] != "0.150" {
		t.Fatalf("weight 应为毫单位十进制字符串，实际 %v", first["weight"])
	}

	// 分页与筛选：名称/条码/分类/状态。
	_, total := pageProducts(t, env, token, "")
	if total != 1 {
		t.Fatalf("产品分页 total=%d，期望 1", total)
	}
	_, total = pageProducts(t, env, token, "&barCode=6901234567890")
	if total != 1 {
		t.Fatalf("按条码筛选 total=%d，期望 1", total)
	}
	_, total = pageProducts(t, env, token, fmt.Sprintf("&categoryId=%d", categoryID))
	if total != 1 {
		t.Fatalf("按分类筛选 total=%d，期望 1", total)
	}
	_, total = pageProducts(t, env, token, "&status=1")
	if total != 0 {
		t.Fatalf("按状态 1 筛选 total=%d，期望 0", total)
	}

	// 更新：名称/参考价/状态生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/product", token, map[string]any{
		"id": productID, "name": "无线鼠标改", "categoryId": categoryID, "unitId": unitID,
		"barCode": "6901234567890", "salePrice": "69.90", "status": 1,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新产品：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list, _ := pageProducts(t, env, token, "&name=无线鼠标改")
	if len(list) != 1 || list[0]["salePrice"] != "69.90" || list[0]["status"] != float64(1) {
		t.Fatalf("更新未生效：list=%v", list)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", productID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除产品：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/product/%d", productID))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total = pageProducts(t, env, token, "")
	if total != 0 {
		t.Fatalf("删除后列表 total=%d，期望 0（软删除不可见）", total)
	}
}

// TestIntegrationProductOptionalFields 可选字段缺省创建（§3.1 展示字段语义）：
// 不提交 weight 与三个参考价时整单成功（weight 为 NULL、参考价落 0.00）；同时
// 固化「空串仍 400」的 API 边界——前端产品页 beforeSubmit 负责剔除空串
// （#88 已知缺陷修复），后端不做空串容错，避免把非法十进制静默当缺省。
func TestIntegrationProductOptionalFields(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	category := createCategory(t, env, token, "可选字段分类", nil)
	unit := createUnit(t, env, token, "套", nil)
	categoryID := int64(category["id"].(float64))
	unitID := int64(unit["id"].(float64))

	status, api := env.Request(t, http.MethodPost, "/erp/product", token, map[string]any{
		"categoryId": categoryID,
		"unitId":     unitID,
		"name":       "只填必填项的产品",
		"status":     0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("缺省 weight/参考价创建产品：HTTP %d code %d msg %q，期望 HTTP 200 code 0",
			status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建产品响应: %v（data=%s）", err, string(api.Data))
	}
	if row["weight"] != nil {
		t.Fatalf("未提交 weight 应回读 null，实际 %v", row["weight"])
	}
	if row["purchasePrice"] != "0.00" || row["salePrice"] != "0.00" || row["minPrice"] != "0.00" {
		t.Fatalf("未提交参考价应回读 0.00：%v/%v/%v",
			row["purchasePrice"], row["salePrice"], row["minPrice"])
	}

	// 空串是非法十进制，后端 400（前端已剔除；这里锁定契约不被静默放宽）。
	status, api = env.Request(t, http.MethodPost, "/erp/product", token, map[string]any{
		"categoryId":    categoryID,
		"unitId":        unitID,
		"name":          "空串参考价",
		"purchasePrice": "",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("空串参考价：HTTP %d code %d，期望 400（msg=%q）", status, api.Code, api.Msg)
	}
	if _, total := pageProducts(t, env, token, "&name=空串参考价"); total != 0 {
		t.Fatalf("空串参考价不应落库：total=%d", total)
	}
}

// TestIntegrationProductRefConstraints 引用约束（spec §4.9）：
// 分类/单位被产品引用禁删 409；解除引用（删产品）后可删。
func TestIntegrationProductRefConstraints(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	category := createCategory(t, env, token, "被引用分类", nil)
	categoryID := int64(category["id"].(float64))
	unit := createUnit(t, env, token, "被引用单位", nil)
	unitID := int64(unit["id"].(float64))

	product := createProduct(t, env, token, "引用产品", map[string]any{
		"categoryId": categoryID, "unitId": unitID,
	})

	// 分类/单位被引用：409。
	status, api := deleteByID(t, env, token, fmt.Sprintf("/erp/product-category/%d", categoryID))
	if status != http.StatusConflict {
		t.Fatalf("分类被引用删除：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/product-unit/%d", unitID))
	if status != http.StatusConflict {
		t.Fatalf("单位被引用删除：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 删除产品释放引用后可删。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", int64(product["id"].(float64))))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除产品：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/product-category/%d", categoryID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("解除引用后删除分类：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/product-unit/%d", unitID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("解除引用后删除单位：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationProductValidation 非法输入 400：分类/单位必须存在、状态枚举、
// 价格十进制字符串约束（Money/qty 不接受数字字面量）。
func TestIntegrationProductValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	category := createCategory(t, env, token, "校验用分类", nil)
	categoryID := int64(category["id"].(float64))
	unit := createUnit(t, env, token, "校验用单位", nil)
	unitID := int64(unit["id"].(float64))

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "分类不存在", payload: map[string]any{"name": "分类缺失", "categoryId": 999999, "unitId": unitID}},
		{name: "单位不存在", payload: map[string]any{"name": "单位缺失", "categoryId": categoryID, "unitId": 999999}},
		{name: "状态非法", payload: map[string]any{"name": "非法状态产品", "categoryId": categoryID, "unitId": unitID, "status": 9}},
		{name: "价格为数字字面量", payload: map[string]any{"name": "数字价格", "categoryId": categoryID, "unitId": unitID, "salePrice": 59.9}},
		{name: "价格为三位小数", payload: map[string]any{"name": "三位小数价格", "categoryId": categoryID, "unitId": unitID, "salePrice": "59.999"}},
		{name: "重量超三位小数", payload: map[string]any{"name": "四位小数重量", "categoryId": categoryID, "unitId": unitID, "weight": "0.1234"}},
		{name: "重量为负", payload: map[string]any{"name": "负重量", "categoryId": categoryID, "unitId": unitID, "weight": "-1.000"}},
		{name: "重量超列域", payload: map[string]any{"name": "超域重量", "categoryId": categoryID, "unitId": unitID, "weight": "1000000000.000"}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/erp/product", token, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
	}

	// 非法输入不落库。
	_, total := pageProducts(t, env, token, "")
	if total != 0 {
		t.Fatalf("非法输入产品被落库：total=%d，期望 0", total)
	}

	// 列域边界值可落库（decimal(12,3) 上限 999,999,999.999，不得误杀）。
	boundary := createProduct(t, env, token, "边界重量产品", map[string]any{
		"categoryId": categoryID, "unitId": unitID, "weight": "999999999.999",
	})
	if boundary["weight"] != "999999999.999" {
		t.Fatalf("边界重量回读 = %v，期望 999999999.999", boundary["weight"])
	}

	// 列表筛选非法状态同样拒绝（绝不静默当「无此筛选」）。
	status, api := env.Request(t, http.MethodGet, "/erp/product/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d code %d，期望 400", status, api.Code)
	}
}
