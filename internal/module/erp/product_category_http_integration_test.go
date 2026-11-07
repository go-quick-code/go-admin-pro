//go:build integration

// 产品分类 HTTP 集成测试（issue #88，docs/erp-mvp-spec.md §5/§4.9）：
// GET list 全量（前端组树）、CRUD、父分类环校验、有子分类 409、被产品引用 409
// （引用用例在产品域测试文件，这里覆盖树自身约束）。
package erp_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationProductCategoryCRUD 分类 CRUD 全链路：
// list 全量返回（前端组树契约，无 children 字段）、详情、更新、软删除。
func TestIntegrationProductCategoryCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 创建根分类与子分类。
	root := createCategory(t, env, token, "电子设备", map[string]any{
		"code": "elec", "sort": 1, "status": 0,
	})
	rootID := int64(root["id"].(float64))
	child := createCategory(t, env, token, "手机", map[string]any{
		"parentId": rootID, "sort": 1,
	})
	if child["parentId"] != float64(rootID) {
		t.Fatalf("子分类 parentId=%v，期望 %d", child["parentId"], rootID)
	}

	// list 全量：扁平列表（前端组树契约，spec §5），不含 children 字段。
	list := listCategories(t, env, token)
	if len(list) != 2 {
		t.Fatalf("分类列表应含 2 条（根+子），实际 %d 条", len(list))
	}
	for _, row := range list {
		if _, has := row["children"]; has {
			t.Fatal("GET list 是前端组树契约，不应返回 children 字段")
		}
	}
	// 同 sort 按 id 升序（后端排序 sort, id），先创建的根在前。
	if list[0]["id"] != root["id"] {
		t.Fatalf("列表首条应为根分类 %v，实际 %v", root["id"], list[0]["id"])
	}

	// 详情回读。
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/product-category/%d", rootID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询分类详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 更新：改名与排序生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/product-category", token, map[string]any{
		"id": rootID, "parentId": int64(0), "name": "电子设备改", "code": "elec2", "sort": 5,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新分类：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list = listCategories(t, env, token)
	var updated map[string]any
	for _, row := range list {
		if int64(row["id"].(float64)) == rootID {
			updated = row
		}
	}
	if updated == nil || updated["name"] != "电子设备改" || updated["sort"] != float64(5) {
		t.Fatalf("更新未生效：%v", updated)
	}

	// 软删除子分类后详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product-category/%d", int64(child["id"].(float64))))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除分类：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getJSON(t, env, token, fmt.Sprintf("/erp/product-category/%d", int64(child["id"].(float64))))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	if n := len(listCategories(t, env, token)); n != 1 {
		t.Fatalf("删除后列表应剩 1 条，实际 %d 条", n)
	}
}

// TestIntegrationProductCategoryParentValidation 父分类环校验（400）：
// 父不能是自己、不能是自己的子孙；父不存在 400。
func TestIntegrationProductCategoryParentValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	root := createCategory(t, env, token, "父分类A", nil)
	rootID := int64(root["id"].(float64))
	child := createCategory(t, env, token, "子分类A1", map[string]any{"parentId": rootID})
	childID := int64(child["id"].(float64))

	cases := []struct {
		name     string
		parentID int64
		selfID   int64
	}{
		{name: "父是自己", parentID: rootID, selfID: rootID},
		{name: "父是自己的子孙", parentID: childID, selfID: rootID},
		{name: "父不存在", parentID: 999999, selfID: 0},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPut, "/erp/product-category", token, map[string]any{
			"id": tc.selfID, "parentId": tc.parentID, "name": "改名",
		})
		if tc.selfID == 0 {
			// 父不存在走创建路径。
			status, api = env.Request(t, http.MethodPost, "/erp/product-category", token, map[string]any{
				"parentId": tc.parentID, "name": "挂不存在父的分类",
			})
		}
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
	}

	// 环校验不落库：根分类名称未变。
	list := listCategories(t, env, token)
	for _, row := range list {
		if int64(row["id"].(float64)) == rootID && row["name"] != "父分类A" {
			t.Fatalf("环校验失败后根分类不应被改名，实际 %v", row["name"])
		}
	}
}

// TestIntegrationProductCategoryDeleteConstraints 删除约束（409）：
// 有子分类禁止删；分类不存在 404。
func TestIntegrationProductCategoryDeleteConstraints(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	root := createCategory(t, env, token, "有子分类的根", nil)
	rootID := int64(root["id"].(float64))
	createCategory(t, env, token, "根的子分类", map[string]any{"parentId": rootID})

	status, api := deleteByID(t, env, token, fmt.Sprintf("/erp/product-category/%d", rootID))
	if status != http.StatusConflict {
		t.Fatalf("有子分类删除：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	status, _ = deleteByID(t, env, token, "/erp/product-category/999999")
	if status != http.StatusNotFound {
		t.Fatalf("删除不存在的分类：HTTP %d，期望 404", status)
	}
}

// TestIntegrationProductCategoryValidation 非法输入 400：名称必填、状态枚举。
func TestIntegrationProductCategoryValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/erp/product-category", token, map[string]any{"name": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("空名称：HTTP %d code %d，期望 400", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/erp/product-category", token, map[string]any{"name": "非法状态", "status": 9})
	if status != http.StatusBadRequest {
		t.Fatalf("状态 9：HTTP %d code %d，期望 400", status, api.Code)
	}

	// 非法状态不落库。
	if n := len(listCategories(t, env, token)); n != 0 {
		t.Fatalf("非法状态分类被落库：列表 %d 条，期望 0", n)
	}
}
