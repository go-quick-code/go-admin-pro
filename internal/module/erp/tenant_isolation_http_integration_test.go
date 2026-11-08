//go:build integration

// ERP 租户隔离集成测试（issue #88，spec §4.10/§8.2 + 租户插件纪律）：
// 六张主数据表已登记 TenantTables——跨租户业务 ID 一律 404（不泄露存在性），
// 租户间列表互不可见。
package erp_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationErpTenantIsolation 租户隔离：租户 2 看不到租户 1 的分类/单位/产品，
// 跨租户详情/修改/删除一律 404；租户 2 的 admin 同样不能改租户 1 的数据。
func TestIntegrationErpTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 租户 1 建全链路主数据。
	category := createCategory(t, env, admin, "隔离分类", nil)
	categoryID := int64(category["id"].(float64))
	unit := createUnit(t, env, admin, "隔离单位", nil)
	unitID := int64(unit["id"].(float64))
	product := createProduct(t, env, admin, "隔离产品", map[string]any{
		"categoryId": categoryID, "unitId": unitID,
	})
	productID := int64(product["id"].(float64))

	// 租户 2（内置 admin/123456）。
	tenant2 := createTenantByAPI(t, env, admin, "ERP隔离测试租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表互不可见。
	if list := listCategories(t, env, token2); len(list) != 0 {
		t.Fatalf("租户 2 分类列表应空，实际 %d 条", len(list))
	}
	status, api := env.Request(t, http.MethodGet, "/erp/product-unit/page?pageNo=1&pageSize=10", token2, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 2 查询单位分页：HTTP %d code %d", status, api.Code)
	}
	_, total := pageProducts(t, env, token2, "")
	if total != 0 {
		t.Fatalf("租户 2 产品分页 total=%d，期望 0（租户隔离）", total)
	}

	// 跨租户详情一律 404（不泄露存在性）。
	for _, path := range []string{
		fmt.Sprintf("/erp/product-category/%d", categoryID),
		fmt.Sprintf("/erp/product-unit/%d", unitID),
		fmt.Sprintf("/erp/product/%d", productID),
	} {
		status, _ := getJSON(t, env, token2, path)
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 访问租户 1 详情 %s：HTTP %d，期望 404", path, status)
		}
	}

	// 跨租户修改/删除一律 404（更新走租户过滤，找不到即 404）。
	status, _ = env.Request(t, http.MethodPut, "/erp/product", token2, map[string]any{
		"id": productID, "name": "越权改名", "categoryId": categoryID, "unitId": unitID,
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 产品：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/product/%d", productID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 产品：HTTP %d，期望 404", status)
	}

	// 租户 2 自己建同主数据不受租户 1 影响（无跨租户唯一键冲突）。
	t2Unit := createUnit(t, env, token2, "隔离单位", nil)
	t2Category := createCategory(t, env, token2, "隔离分类", nil)
	own := createProduct(t, env, token2, "隔离产品", map[string]any{
		"categoryId": int64(t2Category["id"].(float64)), "unitId": int64(t2Unit["id"].(float64)),
	})
	if own["id"] == product["id"] {
		t.Fatal("租户 2 产品不应复用租户 1 的 ID 行")
	}

	// 租户 1 仍只看到自己的数据。
	_, total = pageProducts(t, env, admin, "")
	if total != 1 {
		t.Fatalf("租户 1 产品 total=%d，期望 1", total)
	}
}
