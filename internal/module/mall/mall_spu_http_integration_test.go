//go:build integration

// mall 商品域 SPU/SKU HTTP 集成测试（issue #177，docs/mall-mvp-spec.md
// §3.2/§4）：
//   - SPU 全生命周期：创建多规格（SPU+SKU 同事务）→ 详情 SKU 齐（主 SKU
//     恒首位、规格快照名称服务端写定）→ 更新全量替换 SKU 集 → 上下架往返
//     （PUT :id/status）→ 删除约束（被 SPU 引用的分类 409——T4 接线 T3 预留
//     谓词；被 SPU 引用的品牌 409）→ SPU 删除成功（级联软删 SKU，直查镜像）
//     → 引用清空后分类/品牌可删；
//   - 校验矩阵：分类非叶子 400 / 分类不存在 404 / 品牌不存在 404 / 规格引用
//     不存在属性值 400 / 规格值不归属 400 / 负价格与负库存 400 / 多规格缺
//     SKU 400 / 单规格多 SKU 400 / 同租户同名 409；
//   - 越权矩阵：跨租户 SPU 业务 ID 一律 404（不泄露存在性），同名 SPU 跨
//     租户互不冲突（唯一键是租户内）。
package mall_test

import (
	"net/http"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mallCreateSPU 创建 SPU（断言成功返回响应 JSON）。
func mallCreateSPU(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/product/spu", token, body)
	mustOK(t, "创建 SPU", status, api)
	return parseJSON(t, string(api.Data))
}

// mallCreateProperty 创建属性名（断言成功返回 ID）。
func mallCreateProperty(t *testing.T, env *testutil.IntegrationEnv, token, propName string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/product/property", token, map[string]any{"name": propName})
	mustOK(t, "创建属性 "+propName, status, api)
	property := parseJSON(t, string(api.Data))
	return int64(property["id"].(float64))
}

// mallCreatePropertyValue 在既有属性名下创建属性值（断言成功返回 ID）。
func mallCreatePropertyValue(t *testing.T, env *testutil.IntegrationEnv, token string, propertyID int64, valueName string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": propertyID, "name": valueName,
	})
	mustOK(t, "创建属性值 "+valueName, status, api)
	value := parseJSON(t, string(api.Data))
	return int64(value["id"].(float64))
}

// TestIntegrationSPULifecycle SPU 全生命周期：多规格创建 → 详情 SKU 齐 →
// 更新替换 SKU → 上下架 → 删除约束（分类/品牌被引用 409）→ 删除成功 →
// 引用清空后分类/品牌可删。
func TestIntegrationSPULifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 主数据：叶子分类 + 品牌 + 颜色（红/蓝）与版本（标准）属性值。
	categoryID := mallCreateCategory(t, env, token, map[string]any{"name": "手机"})
	phone := mallCreateCategory(t, env, token, map[string]any{"name": "旗舰机", "parentId": categoryID})
	status, api := env.Request(t, http.MethodPost, "/mall/product/brand", token, map[string]any{"name": "Sky"})
	mustOK(t, "创建品牌 Sky", status, api)
	brand := parseJSON(t, string(api.Data))
	// 属性主数据：颜色（红/蓝两值）与版本（标准）。属性名只建一次（租户内
	// 唯一），两个值挂在同一属性下。
	colorProp := mallCreateProperty(t, env, token, "颜色")
	red := mallCreatePropertyValue(t, env, token, colorProp, "红色")
	blue := mallCreatePropertyValue(t, env, token, colorProp, "蓝色")
	versionProp := mallCreateProperty(t, env, token, "版本")
	std := mallCreatePropertyValue(t, env, token, versionProp, "标准")

	// 创建多规格 SPU（SPU+SKU 同事务）：两条 SKU，首条显式主 SKU。
	marketPrice := "4999.00"
	spu := mallCreateSPU(t, env, token, map[string]any{
		"categoryId": phone, "brandId": brand["id"], "name": "Sky X1",
		"picUrl":        "https://cdn.example.com/x1.png",
		"sliderPicUrls": []string{"https://cdn.example.com/x1-1.png", "https://cdn.example.com/x1-2.png"},
		"detail":        "<p>Sky X1 旗舰机</p>",
		"specType":      1, "marketPrice": marketPrice, "sort": 1,
		"skus": []map[string]any{
			{"properties": []map[string]any{{"propertyId": colorProp, "valueId": blue}},
				"price": "4299.00", "marketPrice": "4699.00", "stock": 10},
			{"properties": []map[string]any{{"propertyId": colorProp, "valueId": red}},
				"price": "4999.00", "stock": 5, "first": true},
		},
	})
	spuID := int64(spu["id"].(float64))

	// 详情：SKU 齐（2 条，主 SKU 恒首位），规格快照名称服务端写定（ID 反查），
	// properties 形状 [{propertyId,propertyName,valueId,valueName}]。
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), token, nil)
	mustOK(t, "SPU 详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["name"] != "Sky X1" || detail["specType"] != float64(1) || detail["marketPrice"] != marketPrice {
		t.Fatalf("SPU 详情字段不符：%s", api.Data)
	}
	if len(detail["sliderPicUrls"].([]any)) != 2 {
		t.Fatalf("轮播图应 2 张：%s", api.Data)
	}
	skus, ok := detail["skus"].([]any)
	if !ok || len(skus) != 2 {
		t.Fatalf("详情应含 2 条 SKU：%s", api.Data)
	}
	first := skus[0].(map[string]any)
	if first["first"] != float64(1) {
		t.Fatalf("主 SKU 应恒在首位（first=1）：%s", api.Data)
	}
	// 主 SKU 规格快照：红色（客户端故意只传 ID，名称由服务端反查）。
	firstProps := first["properties"].([]any)
	if len(firstProps) != 1 {
		t.Fatalf("主 SKU 应 1 个规格项：%s", api.Data)
	}
	prop := firstProps[0].(map[string]any)
	if prop["valueName"] != "红色" || prop["propertyName"] != "颜色" {
		t.Fatalf("规格快照名称应由服务端按主数据反查：%s", api.Data)
	}
	// 价格 Money 口径：十进制字符串往返。
	if first["price"] != "4999.00" {
		t.Fatalf("SKU 售价应为十进制字符串 \"4999.00\"：%s", api.Data)
	}

	// 同租户同名 409（唯一键 UNIQUE(tenant_id, name)）。
	status, api = env.Request(t, http.MethodPost, "/mall/product/spu", token, map[string]any{
		"categoryId": phone, "name": "Sky X1", "specType": 0,
		"skus": []map[string]any{{"price": "1.00"}},
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("SPU 撞名：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 校验矩阵 400/404。
	for _, tc := range []struct {
		name   string
		status int
		code   int
		body   map[string]any
	}{
		{"分类非叶子（挂在有子级的根）", http.StatusBadRequest, 400, map[string]any{
			"categoryId": categoryID, "name": "BadLeaf", "specType": 0,
			"skus": []map[string]any{{"price": "1.00"}}}},
		{"分类不存在", http.StatusNotFound, 404, map[string]any{
			"categoryId": 999999, "name": "BadCategory", "specType": 0,
			"skus": []map[string]any{{"price": "1.00"}}}},
		{"品牌不存在", http.StatusNotFound, 404, map[string]any{
			"categoryId": phone, "brandId": 999999, "name": "BadBrand", "specType": 0,
			"skus": []map[string]any{{"price": "1.00"}}}},
		{"规格引用不存在属性值", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "BadValue", "specType": 1,
			"skus": []map[string]any{{"properties": []map[string]any{{"propertyId": colorProp, "valueId": 999999}},
				"price": "1.00"}}}},
		{"规格值不归属属性", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "BadOwner", "specType": 1,
			"skus": []map[string]any{{"properties": []map[string]any{{"propertyId": versionProp, "valueId": red}},
				"price": "1.00"}}}},
		{"负价格", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "NegPrice", "specType": 0,
			"skus": []map[string]any{{"price": "-0.01"}}}},
		{"负库存", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "NegStock", "specType": 0,
			"skus": []map[string]any{{"price": "1.00", "stock": -1}}}},
		{"多规格缺 SKU", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "NoSku", "specType": 1, "skus": []map[string]any{}}},
		{"单规格多条 SKU", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "TwoSku", "specType": 0,
			"skus": []map[string]any{{"price": "1.00"}, {"price": "2.00"}}}},
		{"单规格带规格项", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "SingleWithProps", "specType": 0,
			"skus": []map[string]any{{"price": "1.00",
				"properties": []map[string]any{{"propertyId": colorProp, "valueId": red}}}}}},
		{"多规格主 SKU 多个", http.StatusBadRequest, 400, map[string]any{
			"categoryId": phone, "name": "TwoFirst", "specType": 1,
			"skus": []map[string]any{
				{"properties": []map[string]any{{"propertyId": colorProp, "valueId": red}}, "price": "1.00", "first": true},
				{"properties": []map[string]any{{"propertyId": colorProp, "valueId": blue}}, "price": "2.00", "first": true},
			}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ap := env.Request(t, http.MethodPost, "/mall/product/spu", token, tc.body)
			if st != tc.status || ap.Code != tc.code {
				t.Fatalf("%s：HTTP %d code %d msg %q，期望 %d/%d", tc.name, st, ap.Code, ap.Msg, tc.status, tc.code)
			}
		})
	}

	// 更新（全量替换 SKU 集）：多规格 2 条 → 2 条新组合；不指定 first（服务端
	// 默认首条）。
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu", token, map[string]any{
		"id": spuID, "categoryId": phone, "brandId": brand["id"], "name": "Sky X1 Pro",
		"picUrl":        "https://cdn.example.com/x1p.png",
		"sliderPicUrls": []string{"https://cdn.example.com/x1p-1.png"},
		"detail":        "<p>Sky X1 Pro</p>",
		"specType":      1, "marketPrice": "5999.00", "sort": 2,
		"skus": []map[string]any{
			{"properties": []map[string]any{{"propertyId": versionProp, "valueId": std}},
				"price": "5999.00", "stock": 8},
			{"properties": []map[string]any{{"propertyId": colorProp, "valueId": red}},
				"price": "6499.00", "stock": 3},
		},
	})
	mustOK(t, "更新 SPU", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), token, nil)
	mustOK(t, "更新后详情", status, api)
	detail = parseJSON(t, string(api.Data))
	if detail["name"] != "Sky X1 Pro" {
		t.Fatalf("更新后名称应为 Sky X1 Pro：%s", api.Data)
	}
	skus = detail["skus"].([]any)
	if len(skus) != 2 || skus[0].(map[string]any)["first"] != float64(1) {
		t.Fatalf("替换后应 2 条 SKU 且首条为主 SKU（未指定时服务端默认）：%s", api.Data)
	}
	if skus[0].(map[string]any)["price"] != "5999.00" {
		t.Fatalf("替换后首条 SKU 售价应 5999.00：%s", api.Data)
	}

	// 上下架往返（PUT :id/status）：下架 → 上架 → 状态越界 400。
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(spuID)+"/status", token,
		map[string]any{"status": 0})
	mustOK(t, "下架", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), token, nil)
	if parseJSON(t, string(api.Data))["status"] != float64(0) {
		t.Fatalf("下架后 status 应 0：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(spuID)+"/status", token,
		map[string]any{"status": 1})
	mustOK(t, "上架", status, api)
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(spuID)+"/status", token,
		map[string]any{"status": 2})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("上下架状态越界：HTTP %d code %d，期望 400", status, api.Code)
	}

	// 分页过滤：status=1 命中、categoryId/名称过滤命中。
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/page?pageNo=1&pageSize=50&status=1", token, nil)
	mustOK(t, "SPU 分页 status=1", status, api)
	if len(mallPageList(t, api)) != 1 {
		t.Fatalf("status=1 应只命中 Sky X1 Pro：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/page?pageNo=1&pageSize=50&categoryId="+itoa(phone)+"&name=Pro", token, nil)
	mustOK(t, "SPU 分页 分类+关键词", status, api)
	if len(mallPageList(t, api)) != 1 {
		t.Fatalf("分类+关键词过滤应命中 1 条：%s", api.Data)
	}

	// 删除约束（T4 接线 T3 预留谓词）：分类被 SPU 引用禁删 409、品牌被 SPU
	// 引用禁删 409。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/category/"+itoa(phone), token, nil)
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("分类被 SPU 引用删除：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete, "/mall/product/brand/"+itoa(int64(brand["id"].(float64))), token, nil)
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("品牌被 SPU 引用删除：HTTP %d code %d，期望 409", status, api.Code)
	}

	// SPU 删除成功：级联软删其 SKU（直查镜像——未删 SKU 残留必须为 0）。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/spu/"+itoa(spuID), token, nil)
	mustOK(t, "删除 SPU", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("删除后 SPU 详情：HTTP %d code %d，期望 404", status, api.Code)
	}
	var liveSKUs int64
	if err := tenant.Skip(env.DB).Model(&mallmodel.ProductSKU{}).
		Where("spu_id = ? AND deleted_at IS NULL", spuID).
		Count(&liveSKUs).Error; err != nil {
		t.Fatalf("统计级联软删 SKU: %v", err)
	}
	if liveSKUs != 0 {
		t.Fatalf("删 SPU 应级联软删其 SKU，残留 %d 行未删", liveSKUs)
	}

	// 引用清空后分类/品牌可删。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/category/"+itoa(phone), token, nil)
	mustOK(t, "删除引用清空后的分类", status, api)
	status, api = env.Request(t, http.MethodDelete, "/mall/product/brand/"+itoa(int64(brand["id"].(float64))), token, nil)
	mustOK(t, "删除引用清空后的品牌", status, api)
}

// TestIntegrationSPUCrossTenant SPU 越权矩阵：跨租户业务 ID 一律 404（不泄
// 露存在性）；同名 SPU 跨租户互不冲突（唯一键是租户内）。
func TestIntegrationSPUCrossTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	categoryID := mallCreateCategory(t, env, adminToken, map[string]any{"name": "跨境SPU分类"})
	spu := mallCreateSPU(t, env, adminToken, map[string]any{
		"categoryId": categoryID, "name": "跨境商品", "specType": 0,
		"skus": []map[string]any{{"price": "9.90", "stock": 1}},
	})
	spuID := int64(spu["id"].(float64))

	tenant2 := createTenantByAPI(t, env, adminToken, "mall-spu-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	// 租户 2 自建同名分类（租户 1 的分类对租户 2 不可见——引用会 404）。
	t2CategoryID := mallCreateCategory(t, env, token2, map[string]any{"name": "跨境SPU分类"})

	// 读/改/上下架/删租户 1 的 SPU → 404。
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"详情", http.MethodGet, "/mall/product/spu/" + itoa(spuID), nil},
		{"更新", http.MethodPut, "/mall/product/spu", map[string]any{
			"id": spuID, "categoryId": categoryID, "name": "越权改名", "specType": 0,
			"skus": []map[string]any{{"price": "1.00"}}}},
		{"上下架", http.MethodPut, "/mall/product/spu/" + itoa(spuID) + "/status", map[string]any{"status": 1}},
		{"删除", http.MethodDelete, "/mall/product/spu/" + itoa(spuID), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, tc.method, tc.path, token2, tc.body)
			if status != http.StatusNotFound || api.Code != 404 {
				t.Fatalf("%s（跨租户）：HTTP %d code %d，期望 404", tc.name, status, api.Code)
			}
		})
	}

	// 租户 2 建同名 SPU → 成功（唯一键是租户内）；分页互不可见。
	mallCreateSPU(t, env, token2, map[string]any{
		"categoryId": t2CategoryID, "name": "跨境商品", "specType": 0,
		"skus": []map[string]any{{"price": "1.00"}},
	})
	for _, tok := range []string{adminToken, token2} {
		status, api := env.Request(t, http.MethodGet, "/mall/product/spu/page?pageNo=1&pageSize=50&name=跨境商品", tok, nil)
		mustOK(t, "同名 SPU 分页", status, api)
		if len(mallPageList(t, api)) != 1 {
			t.Fatalf("同名 SPU 各租户应只看到自己的 1 条（token %s）： %s", tok, api.Data)
		}
	}
}

// TestIntegrationSPUSoftDeleteNameHolds 软删 SPU 名额占位（唯一键不含
// deleted_at，照 member_level 先例）：同名不可重建（409）。
func TestIntegrationSPUSoftDeleteNameHolds(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	categoryID := mallCreateCategory(t, env, token, map[string]any{"name": "占位分类"})
	spu := mallCreateSPU(t, env, token, map[string]any{
		"categoryId": categoryID, "name": "占位商品", "specType": 0,
		"skus": []map[string]any{{"price": "3.00"}},
	})
	spuID := int64(spu["id"].(float64))
	status, api := env.Request(t, http.MethodDelete, "/mall/product/spu/"+itoa(spuID), token, nil)
	mustOK(t, "删除占位 SPU", status, api)

	status, api = env.Request(t, http.MethodPost, "/mall/product/spu", token, map[string]any{
		"categoryId": categoryID, "name": "占位商品", "specType": 0,
		"skus": []map[string]any{{"price": "3.00"}},
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("软删后同名重建：HTTP %d code %d msg %q，期望 409（软删行占位）", status, api.Code, api.Msg)
	}
}
