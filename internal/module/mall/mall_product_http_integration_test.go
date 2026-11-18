//go:build integration

// mall 商品域基础三件 HTTP 集成测试（issue #176，docs/mall-mvp-spec.md §3.2/§4）：
//   - 分类树建删：树查询返回两级形态（sort, id 升序）、CRUD、父级校验
//     （父必须是根级 400、父不存在 400、挂子级的根不可降级 400）；
//   - 删除约束：有子级 409（SPU 引用计数随 T4 接线，本票该分支恒放行）；
//   - 同级唯一 409（跨父级同名允许）；品牌/属性名租户内唯一 409；属性值
//     (property_id, name) 唯一 409；删属性名级联删值（直插直查镜像）；
//   - 越权矩阵：跨租户分类/品牌/属性/属性值业务 ID 一律 404（不泄露存在性），
//     同名资源跨租户互不冲突（唯一键是租户内）。
package mall_test

import (
	"encoding/json"
	"net/http"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mallCreateCategory 创建分类（断言成功返回 ID）。
func mallCreateCategory(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/product/category", token, body)
	mustOK(t, "创建分类", status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析分类响应: %v（data=%s）", err, api.Data)
	}
	return row.ID
}

// TestIntegrationCategoryTreeAndDelete 分类树建删 + 父级校验 + 删除约束。
func TestIntegrationCategoryTreeAndDelete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	digital := mallCreateCategory(t, env, token, map[string]any{"name": "数码", "sort": 2})
	apparel := mallCreateCategory(t, env, token, map[string]any{"name": "服饰", "sort": 1})
	phone := mallCreateCategory(t, env, token, map[string]any{"name": "手机", "parentId": digital, "sort": 2})
	laptop := mallCreateCategory(t, env, token, map[string]any{"name": "电脑", "parentId": digital, "sort": 1})
	tshirt := mallCreateCategory(t, env, token, map[string]any{"name": "T恤", "parentId": apparel})

	// 树查询：2 个根、按 (sort, id) 升序（服饰 1 → 数码 2）；数码下 2 个子级。
	status, api := env.Request(t, http.MethodGet, "/mall/product/category/tree", token, nil)
	mustOK(t, "分类树", status, api)
	var tree []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Children []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"children"`
	}
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析分类树: %v（data=%s）", err, api.Data)
	}
	if len(tree) != 2 {
		t.Fatalf("根分类应 2 个，实际 %d：%s", len(tree), api.Data)
	}
	if tree[0].ID != apparel || tree[1].ID != digital {
		t.Fatalf("根排序应为 sort 升序（服饰/数码），实际 %d/%d", tree[0].ID, tree[1].ID)
	}
	if len(tree[1].Children) != 2 || tree[1].Children[0].ID != laptop || tree[1].Children[1].ID != phone {
		t.Fatalf("数码子级应为 (sort,id) 升序 电脑/手机：%s", api.Data)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].ID != tshirt {
		t.Fatalf("服饰子级应为 T恤：%s", api.Data)
	}

	// 同级唯一 409：数码下再建「手机」。
	status, api = env.Request(t, http.MethodPost, "/mall/product/category", token, map[string]any{
		"name": "手机", "parentId": digital,
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("同级同名：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	// 跨父级同名允许（唯一键是 (tenant_id, parent_id, name)）。
	extraPhone := mallCreateCategory(t, env, token, map[string]any{"name": "手机", "parentId": apparel})

	// 父级校验 400：父是子分类（三级挂载）。
	status, api = env.Request(t, http.MethodPost, "/mall/product/category", token, map[string]any{
		"name": "旗舰机", "parentId": phone,
	})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("三级挂载：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 父不存在 400。
	status, api = env.Request(t, http.MethodPost, "/mall/product/category", token, map[string]any{
		"name": "孤儿", "parentId": 999999,
	})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("父不存在：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 自指 400（更新把自己挂自己下）。
	status, api = env.Request(t, http.MethodPut, "/mall/product/category", token, map[string]any{
		"id": digital, "name": "数码", "parentId": digital,
	})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("自指父级：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 挂子级的根不可降级（服饰有子 T恤）。
	status, api = env.Request(t, http.MethodPut, "/mall/product/category", token, map[string]any{
		"id": apparel, "name": "服饰", "parentId": digital,
	})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("挂子级的根降级：HTTP %d code %d，期望 400", status, api.Code)
	}

	// 更新：改名 + 改排序 + 停用。
	status, api = env.Request(t, http.MethodPut, "/mall/product/category", token, map[string]any{
		"id": tshirt, "name": "基础T恤", "parentId": apparel, "sort": 9, "status": 1,
	})
	mustOK(t, "更新分类", status, api)

	// 删除约束 409：数码有子级。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/category/"+itoa(digital), token, nil)
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("有子级删除：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	// 叶子可删：数码/服饰下的全部子级清空后，根也能删。
	for _, id := range []int64{laptop, phone, tshirt, extraPhone, digital, apparel} {
		status, api = env.Request(t, http.MethodDelete, "/mall/product/category/"+itoa(id), token, nil)
		mustOK(t, "删除分类 "+itoa(id), status, api)
	}
	// 删除后详情 404。
	status, api = env.Request(t, http.MethodGet, "/mall/product/category/"+itoa(digital), token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("删除后详情：HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationBrandCRUDUnique 品牌 CRUD + name 租户内唯一 409。
func TestIntegrationBrandCRUDUnique(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/mall/product/brand", token, map[string]any{
		"name": "Acme", "picUrl": "https://cdn.example.com/acme.png", "remark": "测试品牌",
	})
	mustOK(t, "创建品牌", status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析品牌响应: %v（data=%s）", err, api.Data)
	}
	id := row.ID

	// 撞名 409。
	status, api = env.Request(t, http.MethodPost, "/mall/product/brand", token, map[string]any{"name": "Acme"})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("品牌撞名：HTTP %d code %d，期望 409", status, api.Code)
	}
	// 更新撞名 409（先建第二品牌，再把第二品牌改成第一品牌的名字）。
	status, api = env.Request(t, http.MethodPost, "/mall/product/brand", token, map[string]any{"name": "Beta"})
	mustOK(t, "创建第二品牌", status, api)
	var second struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &second)
	status, api = env.Request(t, http.MethodPut, "/mall/product/brand", token, map[string]any{
		"id": second.ID, "name": "Acme",
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("更新品牌撞名：HTTP %d code %d，期望 409", status, api.Code)
	}

	// 分页 + name 过滤。
	status, api = env.Request(t, http.MethodGet, "/mall/product/brand/page?pageNo=1&pageSize=50&name=Acme", token, nil)
	mustOK(t, "品牌分页", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != id {
		t.Fatalf("name=Acme 过滤应命中 1 条，实际 %v", list)
	}

	// 未引用可删（SPU 引用守卫随 T4）；删除后详情 404。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/brand/"+itoa(id), token, nil)
	mustOK(t, "删除品牌", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/product/brand/"+itoa(id), token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("删除后品牌详情：HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationPropertyAndValues 属性名 + 属性值 CRUD：租户内唯一/属性内
// 唯一 409、跨属性同名值允许、删属性名级联删值。
func TestIntegrationPropertyAndValues(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 属性名：撞名 409。
	status, api := env.Request(t, http.MethodPost, "/mall/product/property", token, map[string]any{"name": "颜色"})
	mustOK(t, "创建属性 颜色", status, api)
	var color struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &color)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property", token, map[string]any{"name": "颜色"})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("属性撞名：HTTP %d code %d，期望 409", status, api.Code)
	}
	// 改名撞名 409（第二属性改成「颜色」）。
	status, api = env.Request(t, http.MethodPost, "/mall/product/property", token, map[string]any{"name": "版本"})
	mustOK(t, "创建属性 版本", status, api)
	var version struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &version)
	status, api = env.Request(t, http.MethodPut, "/mall/product/property", token, map[string]any{
		"id": version.ID, "name": "颜色",
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("更新属性撞名：HTTP %d code %d，期望 409", status, api.Code)
	}

	// 属性值：颜色→红色/蓝色；版本→红色（跨属性同名值允许）。
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": color.ID, "name": "红色",
	})
	mustOK(t, "创建属性值 红色", status, api)
	var red struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &red)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": color.ID, "name": "蓝色",
	})
	mustOK(t, "创建属性值 蓝色", status, api)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": version.ID, "name": "红色",
	})
	mustOK(t, "跨属性同名值", status, api)
	// 同属性同名 409。
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": color.ID, "name": "红色",
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("同属性同名值：HTTP %d code %d，期望 409", status, api.Code)
	}
	// 归属属性不存在 404。
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", token, map[string]any{
		"propertyId": 999999, "name": "绿色",
	})
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("归属属性不存在：HTTP %d code %d，期望 404", status, api.Code)
	}

	// 属性值清单：颜色下 2 个值（id 升序）。
	status, api = env.Request(t, http.MethodGet, "/mall/product/property/"+itoa(color.ID)+"/value", token, nil)
	mustOK(t, "属性值清单", status, api)
	var values []map[string]any
	if err := json.Unmarshal(api.Data, &values); err != nil {
		t.Fatalf("解析属性值清单: %v（data=%s）", err, api.Data)
	}
	if len(values) != 2 {
		t.Fatalf("颜色下应 2 个值，实际 %d：%s", len(values), api.Data)
	}
	// 改名：蓝色 → 藏青；再撞同名 409。
	status, api = env.Request(t, http.MethodPut, "/mall/product/property/value", token, map[string]any{
		"id": values[1]["id"], "name": "藏青",
	})
	mustOK(t, "修改属性值", status, api)
	status, api = env.Request(t, http.MethodPut, "/mall/product/property/value", token, map[string]any{
		"id": values[1]["id"], "name": "红色",
	})
	if status != http.StatusConflict || api.Code != 409 {
		t.Fatalf("属性值改名撞同名：HTTP %d code %d，期望 409", status, api.Code)
	}
	// 单值可删。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/property/value/"+itoa(int64(values[1]["id"].(float64))), token, nil)
	mustOK(t, "删除属性值", status, api)

	// 删属性名级联删值：删颜色后其值经直查确认已软删（HTTP 侧颜色详情 404）。
	status, api = env.Request(t, http.MethodDelete, "/mall/product/property/"+itoa(color.ID), token, nil)
	mustOK(t, "删除属性名", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/product/property/"+itoa(color.ID), token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("删除后属性详情：HTTP %d code %d，期望 404", status, api.Code)
	}
	var cascade int64
	if err := tenant.Skip(env.DB).Model(&mallmodel.ProductPropertyValue{}).
		Where("property_id = ? AND deleted_at IS NULL", color.ID).
		Count(&cascade).Error; err != nil {
		t.Fatalf("统计级联软删值: %v", err)
	}
	if cascade != 0 {
		t.Fatalf("删属性名应级联软删其属性值，残留 %d 行", cascade)
	}
}

// TestIntegrationProductCrossTenant 商品域越权矩阵：跨租户业务 ID 一律 404
// （不泄露存在性）；同名资源跨租户互不冲突（唯一键是租户内）。
func TestIntegrationProductCrossTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	categoryID := mallCreateCategory(t, env, adminToken, map[string]any{"name": "跨境数码"})
	status, api := env.Request(t, http.MethodPost, "/mall/product/brand", adminToken, map[string]any{"name": "跨境品牌"})
	mustOK(t, "创建品牌", status, api)
	var brand struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &brand)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property", adminToken, map[string]any{"name": "跨境属性"})
	mustOK(t, "创建属性", status, api)
	var property struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &property)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property/value", adminToken, map[string]any{
		"propertyId": property.ID, "name": "跨境值",
	})
	mustOK(t, "创建属性值", status, api)
	var value struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(api.Data, &value)

	tenant2 := createTenantByAPI(t, env, adminToken, "mall-product-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 读/删租户 1 的分类/品牌/属性/属性值 → 404。
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"分类详情", http.MethodGet, "/mall/product/category/" + itoa(categoryID)},
		{"分类删除", http.MethodDelete, "/mall/product/category/" + itoa(categoryID)},
		{"品牌详情", http.MethodGet, "/mall/product/brand/" + itoa(brand.ID)},
		{"品牌删除", http.MethodDelete, "/mall/product/brand/" + itoa(brand.ID)},
		{"属性详情", http.MethodGet, "/mall/product/property/" + itoa(property.ID)},
		{"属性删除", http.MethodDelete, "/mall/product/property/" + itoa(property.ID)},
		{"属性值清单", http.MethodGet, "/mall/product/property/" + itoa(property.ID) + "/value"},
		{"属性值删除", http.MethodDelete, "/mall/product/property/value/" + itoa(value.ID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, tc.method, tc.path, token2, nil)
			if status != http.StatusNotFound || api.Code != 404 {
				t.Fatalf("%s（跨租户）：HTTP %d code %d，期望 404", tc.name, status, api.Code)
			}
		})
	}

	// 租户 2 建同名分类/品牌/属性 → 成功（唯一键是租户内，跨租户不冲突）。
	mallCreateCategory(t, env, token2, map[string]any{"name": "跨境数码"})
	status, api = env.Request(t, http.MethodPost, "/mall/product/brand", token2, map[string]any{"name": "跨境品牌"})
	mustOK(t, "租户2 同名品牌", status, api)
	status, api = env.Request(t, http.MethodPost, "/mall/product/property", token2, map[string]any{"name": "跨境属性"})
	mustOK(t, "租户2 同名属性", status, api)
	// 租户 2 分页看不到租户 1 的品牌。
	status, api = env.Request(t, http.MethodGet, "/mall/product/brand/page?pageNo=1&pageSize=50", token2, nil)
	mustOK(t, "租户2 品牌分页", status, api)
	for _, row := range mallPageList(t, api) {
		if row["name"] == "跨境品牌" && int64(row["tenantId"].(float64)) != tenant2 {
			t.Fatalf("租户 2 分页不应出现租户 1 的品牌：%v", row)
		}
	}
}
