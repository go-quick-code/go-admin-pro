//go:build integration

// 产品 HTTP 集成测试（issue #11）：走真实 router + 生产中间件（JWT/权限/租户插件）
// + 真实 MySQL，在 HTTP 契约上验证 CRUD 与字段回读、产品编码租户内唯一（400）、
// 状态枚举 400、分类必须存在、无数据范围（自见范围也能看全租户）、租户隔离与 403，
// 以及删除约束扩展点 RefCountChecker 的服务级行为（#15/#16 接点）。
package crm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmProductMenuID 是种子中的产品菜单 ID（含 crm:product:query 权限码）；
// 4063–4066 是查询/新增/修改/删除按钮 ID，与 seed.go / codegen menus 片段一致。
const crmProductMenuID = int64(4061)

// createProduct 创建产品并返回落库后的完整 JSON（价格以十进制字符串提交）。
// extra 未带 categoryId 时先建一个宿主分类；编码缺省 SKU-<name>，价格缺省 9.90。
func createProduct(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	if extra == nil {
		extra = map[string]any{}
	}
	if _, ok := extra["categoryId"]; !ok {
		cat := createProductCategory(t, env, token, "产品分类-"+name)
		extra["categoryId"] = cat["id"]
	}
	payload := map[string]any{"name": name, "code": "SKU-" + name, "price": "9.90"}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/product", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建产品 %q：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
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
	status, api := env.Request(t, http.MethodGet, "/crm/product/page?pageNo=1&pageSize=50"+query, token, nil)
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

// deleteProduct 删除产品并断言成功。
func deleteProduct(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", id), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除产品 %d：HTTP %d code %d msg %q，期望成功", id, status, api.Code, api.Msg)
	}
}

// TestIntegrationProductCRUDLifecycle 产品 CRUD 全链路：
// 创建（字段回读，金额字符串往返）→ 分页筛选（名称/编码/状态/分类）→ 修改 → 删除 → 详情 404。
func TestIntegrationProductCRUDLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat := createProductCategory(t, env, admin, "整机")
	catID := int64(cat["id"].(float64))
	product := createProduct(t, env, admin, "云服务器", map[string]any{
		"categoryId": catID, "code": "SKU-CS-001", "unit": "台", "price": "1999.00", "status": 1,
		"description": "4C8G 入门款",
	})
	id := int64(product["id"].(float64))

	// 字段回读：金额字符串往返不过 float，状态缺省上架。
	if product["code"] != "SKU-CS-001" || product["price"] != "1999.00" ||
		product["unit"] != "台" || product["description"] != "4C8G 入门款" ||
		product["status"] != float64(1) || product["categoryId"] != float64(catID) {
		t.Fatalf("创建回读字段不符：%s", mustJSON(product))
	}

	// 分页筛选：名称模糊、编码精确、状态、分类。
	if _, total := pageProducts(t, env, admin, "&name=云服务"); total != 1 {
		t.Fatalf("按名称筛选 total=%d，期望 1", total)
	}
	if _, total := pageProducts(t, env, admin, "&code=SKU-CS-001"); total != 1 {
		t.Fatalf("按编码筛选 total=%d，期望 1", total)
	}
	if _, total := pageProducts(t, env, admin, "&status=1"); total != 1 {
		t.Fatalf("按状态筛选 total=%d，期望 1", total)
	}
	if _, total := pageProducts(t, env, admin, fmt.Sprintf("&categoryId=%d", catID)); total != 1 {
		t.Fatalf("按分类筛选 total=%d，期望 1", total)
	}
	createProduct(t, env, admin, "云数据库", map[string]any{"categoryId": catID, "code": "SKU-DB-001", "price": "599.00"})
	if _, total := pageProducts(t, env, admin, fmt.Sprintf("&categoryId=%d", catID)); total != 2 {
		t.Fatalf("同分类两个产品 total=2，实际见上")
	}

	// 修改：全量覆盖 + 状态下架。
	status, api := env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
		"id": id, "categoryId": catID, "name": "云服务器Pro", "code": "SKU-CS-001",
		"unit": "台", "price": "2599.50", "status": 0, "description": "4C16G",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("修改产品：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/product/%d", id), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("修改后详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "云服务器Pro" || detail["price"] != "2599.50" || detail["status"] != float64(0) {
		t.Fatalf("修改未生效：%s", mustJSON(detail))
	}

	// 删除 → 详情 404。
	deleteProduct(t, env, admin, id)
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/product/%d", id), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情：HTTP %d，期望 404", status)
	}
}

// TestIntegrationProductCodeUniqueInTenant 产品编码租户内唯一（spec §3）：
// 重复创建 400、改码撞车 400、保持自身编码 200、软删后编码不释放（唯一键兜底）仍 400。
func TestIntegrationProductCodeUniqueInTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat := createProductCategory(t, env, admin, "唯一性分类")
	catID := int64(cat["id"].(float64))
	first := createProduct(t, env, admin, "产品一", map[string]any{"categoryId": catID, "code": "SKU-DUP"})
	firstID := int64(first["id"].(float64))

	// 同码新建 → 400。
	status, api := env.Request(t, http.MethodPost, "/crm/product", admin, map[string]any{
		"name": "产品二", "categoryId": catID, "code": "SKU-DUP", "price": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("同码新建：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 另一产品改码撞车 → 400。
	second := createProduct(t, env, admin, "产品三", map[string]any{"categoryId": catID, "code": "SKU-OTHER"})
	secondID := int64(second["id"].(float64))
	status, api = env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
		"id": secondID, "name": "产品三", "categoryId": catID, "code": "SKU-DUP", "price": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("改码撞车：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 保持自身编码不变 → 200。
	status, api = env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
		"id": firstID, "name": "产品一改", "categoryId": catID, "code": "SKU-DUP", "price": "2.00",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("保持自身编码：HTTP %d code %d msg %q，期望 200", status, api.Code, api.Msg)
	}

	// 软删后编码不释放：唯一键含软删行，重建同码仍 400（迁移注释里写明的口径）。
	deleteProduct(t, env, admin, firstID)
	status, api = env.Request(t, http.MethodPost, "/crm/product", admin, map[string]any{
		"name": "产品四", "categoryId": catID, "code": "SKU-DUP", "price": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("软删后重建同码：HTTP %d code %d msg %q，期望 400（编码不释放）", status, api.Code, api.Msg)
	}

	// 不同租户可用同一编码（唯一键按租户隔离）。
	tenant2 := createTenantByAPI(t, env, admin, "产品编码隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	cat2 := createProductCategory(t, env, token2, "租户二分类")
	createProduct(t, env, token2, "租户二同码产品", map[string]any{"categoryId": cat2["id"], "code": "SKU-DUP"})
}

// TestIntegrationProductStatusEnum400 状态枚举非法值一律 400：
// 新建/修改传 2、-1 拒绝；分页筛选传 9 也拒绝（绝不静默当成无此筛选）。
func TestIntegrationProductStatusEnum400(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	cat := createProductCategory(t, env, admin, "枚举分类")
	catID := int64(cat["id"].(float64))
	product := createProduct(t, env, admin, "枚举产品", map[string]any{"categoryId": catID})
	id := int64(product["id"].(float64))

	for _, v := range []int{2, -1} {
		status, api := env.Request(t, http.MethodPost, "/crm/product", admin, map[string]any{
			"name": "枚举产品" + fmt.Sprint(v), "categoryId": catID, "code": fmt.Sprintf("SKU-E%d", v),
			"price": "1.00", "status": v,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("新建状态 %d：HTTP %d code %d，期望 400", v, status, api.Code)
		}
		status, api = env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
			"id": id, "name": "枚举产品", "categoryId": catID, "code": "SKU-ENUM", "price": "1.00", "status": v,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("修改状态 %d：HTTP %d code %d，期望 400", v, status, api.Code)
		}
	}
	if status, _ := env.Request(t, http.MethodGet, "/crm/product/page?pageNo=1&pageSize=10&status=9", admin, nil); status != http.StatusBadRequest {
		t.Fatalf("分页筛选 status=9：HTTP %d，期望 400", status)
	}
}

// TestIntegrationProductCategoryMustExist 分类必须存在：
// categoryId 指向不存在的分类 → 400（新建与修改同口径）。
func TestIntegrationProductCategoryMustExist(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	cat := createProductCategory(t, env, admin, "存在性分类")
	catID := int64(cat["id"].(float64))
	product := createProduct(t, env, admin, "存在性产品", map[string]any{"categoryId": catID})
	id := int64(product["id"].(float64))

	status, api := env.Request(t, http.MethodPost, "/crm/product", admin, map[string]any{
		"name": "挂空分类", "categoryId": 999999, "code": "SKU-GHOST", "price": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("新建挂空分类：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
		"id": id, "name": "存在性产品", "categoryId": 999999, "code": "SKU-EXIST", "price": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("修改挂空分类：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
}

// TestIntegrationProductNoDataScope 产品是租户级共享资源（ownerScoped=false）：
// 数据范围「仅本人」的用户也能看到/操作全租户产品；无产品权限码的用户全部 403。
func TestIntegrationProductNoDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat := createProductCategory(t, env, admin, "共享分类")
	catID := int64(cat["id"].(float64))
	createProduct(t, env, admin, "共享产品一", map[string]any{"categoryId": catID})
	createProduct(t, env, admin, "共享产品二", map[string]any{"categoryId": catID})

	// dataScope=5（仅本人）+ 产品四按钮：产品没有 owner 列，范围不生效，看全租户。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅本人产品角色", "code": "prod_self_scope", "dataScope": 5,
		"menuIds": []int64{crmProductMenuID, 4063, 4064, 4065, 4066},
	})
	createUserByAPI(t, env, admin, "prod_self_user", 102, []int64{roleID})
	selfScope := env.Login(t, "prod_self_user", "123456")

	if _, total := pageProducts(t, env, selfScope, ""); total != 2 {
		t.Fatalf("仅本人范围用户应看到全租户 2 条产品（无数据范围），实际 total=%d", total)
	}

	// 无产品权限码（角色只绑分类查询）：产品接口全 403。
	noPermRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "无产品权限角色", "code": "prod_no_perm", "dataScope": 1, "menuIds": []int64{crmProductCategoryMenuID, 4067},
	})
	createUserByAPI(t, env, admin, "prod_no_perm_user", 102, []int64{noPermRole})
	noPerm := env.Login(t, "prod_no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"分页", http.MethodGet, "/crm/product/page?pageNo=1&pageSize=10", nil},
		{"新建", http.MethodPost, "/crm/product", map[string]any{"name": "越权产品", "categoryId": catID, "code": "SKU-403", "price": "1.00"}},
		{"删除", http.MethodDelete, "/crm/product/1", nil},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("产品%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}
}

// TestIntegrationProductTenantIsolation 产品租户隔离：
// 租户 2 列表为空、跨租户详情/修改/删除 404、租户 1 数据不受影响。
func TestIntegrationProductTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat := createProductCategory(t, env, admin, "隔离分类")
	catID := int64(cat["id"].(float64))
	product := createProduct(t, env, admin, "租户一产品", map[string]any{"categoryId": catID, "code": "SKU-T1"})
	id := int64(product["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "产品隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	if _, total := pageProducts(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 产品 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/crm/product/%d", id), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 产品详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/crm/product", token2, map[string]any{
		"id": id, "name": "越权改名", "categoryId": catID, "code": "SKU-T1", "price": "1.00",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 产品：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", id), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 产品：HTTP %d，期望 404", status)
	}
	if _, total := pageProducts(t, env, admin, ""); total != 1 {
		t.Fatalf("租户 1 产品 total=%d，期望 1（不受影响）", total)
	}
}

// fakeRefCountChecker 是删除约束扩展点的测试替身：返回注入的业务错误。
type fakeRefCountChecker struct{ err error }

func (f fakeRefCountChecker) CheckProductRefs(context.Context, int64) error { return f.err }

// TestIntegrationProductDeleteRefCountChecker 删除约束扩展点（#15/#16 接点）：
// 默认空实现可删；注册校验器后，被引用产品的删除被拦截且错误透传。
// 直接构造 Service（产品无数据范围，sys 网关在删除路径上不被触达）。
func TestIntegrationProductDeleteRefCountChecker(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	cat := createProductCategory(t, env, admin, "扩展点分类")
	catID := int64(cat["id"].(float64))
	product := createProduct(t, env, admin, "扩展点产品", map[string]any{"categoryId": catID, "code": "SKU-REF"})
	id := int64(product["id"].(float64))

	svc := service.New(repository.New(env.DB), nil)
	ctx, cancel := context.WithTimeout(tenant.WithTenantID(context.Background(), 1), 30*time.Second)
	defer cancel()

	// 默认空实现：删除放行。
	if err := svc.DeleteProduct(ctx, id); err != nil {
		t.Fatalf("默认空实现应放行删除，实际 %v", err)
	}

	// 注册「被引用」校验器：删除被拦截，错误原文透传（#15/#16 将返回 400 业务错误）。
	blocked := service.New(repository.New(env.DB), nil)
	blocked.SetProductRefCountChecker(fakeRefCountChecker{err: fmt.Errorf("产品被商机/合同行项目引用，禁止删除")})
	product2 := createProduct(t, env, admin, "扩展点产品二", map[string]any{"categoryId": catID, "code": "SKU-REF2"})
	id2 := int64(product2["id"].(float64))
	if err := blocked.DeleteProduct(ctx, id2); err == nil || err.Error() != "产品被商机/合同行项目引用，禁止删除" {
		t.Fatalf("注册校验器后删除应被拦截且错误透传，实际 %v", err)
	}
}

// mustJSON 供断言失败时打印完整 JSON。
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(raw)
}
