//go:build integration

// 联系人 HTTP 集成测试（issue #9）：走 #3 基座的真实 router + 生产中间件
// （JWT/权限/租户插件）+ 真实 MySQL，在 HTTP 契约上验证 CRUD、格式校验、
// 可见性跟随父客户（spec §4.1：join 父记录后套用父谓词）与租户隔离。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// contactMenuIDs 是联系人 API 需要的权限码菜单：客户页面 401（含 crm:customer:query）
// + 联系人按钮 4014 查询 / 4015 新增 / 4016 修改 / 4017 删除。
var contactMenuIDs = []int64{401, 4014, 4015, 4016, 4017}

// createContact 在指定客户下创建联系人并断言成功，返回落库后的完整 JSON。
func createContact(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"customerId": customerID, "name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/contact", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建联系人 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var contact map[string]any
	if err := json.Unmarshal(api.Data, &contact); err != nil {
		t.Fatalf("解析创建联系人响应: %v（data=%s）", err, string(api.Data))
	}
	return contact
}

// pageContacts 查询联系人分页并返回 list 与 total。
func pageContacts(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/contact/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询联系人分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析联系人分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getContact 查询联系人详情，返回 HTTP 状态与业务响应。
func getContact(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/contact/%d", id), token, nil)
}

// visibleContactNames 以指定账号登录并拉取联系人分页，返回可见联系人姓名集合。
func visibleContactNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	list, _ := pageContacts(t, env, token, "")
	set := make(map[string]bool, len(list))
	for _, row := range list {
		set[row["name"].(string)] = true
	}
	return set
}

// TestIntegrationContactCRUD 联系人 CRUD 全链路：在客户下创建、字段回读、
// 按客户/姓名筛选、更新、软删除后不可见。
func TestIntegrationContactCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "联系人宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 创建：全字段回读；性别缺省 0（未知）。
	first := createContact(t, env, admin, customerID, "张三", map[string]any{
		"position": "采购总监", "mobile": "13800000001", "email": "zhangsan@example.com",
		"gender": 1, "decisionRole": "决策者", "remark": "主要对接人",
	})
	if first["customerId"] != float64(customerID) {
		t.Fatalf("联系人 customerId=%v，期望 %d", first["customerId"], customerID)
	}
	if first["gender"] != float64(1) || first["position"] != "采购总监" || first["decisionRole"] != "决策者" {
		t.Fatalf("联系人字段不符：gender=%v position=%v decisionRole=%v", first["gender"], first["position"], first["decisionRole"])
	}
	if first["customerName"] != "联系人宿主客户" {
		t.Fatalf("联系人应带所属客户名称，实际 customerName=%v", first["customerName"])
	}

	second := createContact(t, env, admin, customerID, "李四", nil)
	if second["gender"] != float64(0) {
		t.Fatalf("性别缺省应为 0（未知），实际 %v", second["gender"])
	}

	// 详情回读。
	status, api := getContact(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询联系人详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "张三" || detail["mobile"] != "13800000001" || detail["email"] != "zhangsan@example.com" {
		t.Fatalf("详情字段不符：name=%v mobile=%v email=%v", detail["name"], detail["mobile"], detail["email"])
	}

	// 列表：按客户筛选出两条；按姓名筛选唯一命中；列表行也带客户名称。
	if list, total := pageContacts(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 2 || len(list) != 2 {
		t.Fatalf("按客户筛选 total=%d list=%d，期望 2", total, len(list))
	}
	if list, total := pageContacts(t, env, admin, "&name=张三"); total != 1 || list[0]["name"] != "张三" {
		t.Fatalf("按姓名筛选 total=%d，期望唯一命中张三", total)
	}
	list, _ := pageContacts(t, env, admin, "")
	for _, row := range list {
		if row["customerName"] == "" {
			t.Fatalf("列表行缺 customerName：%v", row["name"])
		}
	}

	// 更新：字段生效； customerId 不可漂移（更新接口不接受换客户）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/contact", admin, map[string]any{
		"id": first["id"], "name": "张三丰", "position": "总经理", "mobile": "13900000002",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新联系人：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getContact(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("更新后查询详情：HTTP %d code %d", status, api.Code)
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "张三丰" || detail["position"] != "总经理" || detail["mobile"] != "13900000002" {
		t.Fatalf("更新未生效：name=%v position=%v mobile=%v", detail["name"], detail["position"], detail["mobile"])
	}
	if detail["customerId"] != float64(customerID) {
		t.Fatalf("更新不应改所属客户：customerId=%v", detail["customerId"])
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contact/%d", int64(second["id"].(float64))), admin, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除联系人：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getContact(t, env, admin, int64(second["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	if _, total := pageContacts(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 1 {
		t.Fatalf("删除后列表 total=1，期望 1（软删除不可见），实际 total=%d", total)
	}
}

// TestIntegrationContactValidation 非法输入 400：手机/邮箱格式、性别枚举、必填姓名；
// 客户不存在时建联系人 404；非法值一律不落库。
func TestIntegrationContactValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "校验宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "姓名为空", payload: map[string]any{"customerId": customerID, "name": ""}},
		{name: "手机号太短", payload: map[string]any{"customerId": customerID, "name": "手机非法", "mobile": "12345"}},
		{name: "手机号非数字", payload: map[string]any{"customerId": customerID, "name": "手机非法", "mobile": "abcdefghijk"}},
		{name: "手机号 12 位", payload: map[string]any{"customerId": customerID, "name": "手机非法", "mobile": "138000000012"}},
		{name: "邮箱缺顶级域", payload: map[string]any{"customerId": customerID, "name": "邮箱非法", "email": "a@b"}},
		{name: "邮箱无 @", payload: map[string]any{"customerId": customerID, "name": "邮箱非法", "email": "plain-address"}},
		{name: "性别非法", payload: map[string]any{"customerId": customerID, "name": "性别非法", "gender": 3}},
		{name: "职位超长", payload: map[string]any{"customerId": customerID, "name": "职位超长", "position": strings.Repeat("长", 65)}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/contact", admin, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if api.Msg == "" {
			t.Fatalf("%s：应返回中文错误信息", tc.name)
		}
	}

	// 更新同样拒绝非法格式：先建一个合法联系人，再以非法手机号更新。
	updatable := createContact(t, env, admin, customerID, "更新校验联系人", nil)
	upStatus, _ := env.Request(t, http.MethodPut, "/crm/contact", admin, map[string]any{
		"id": updatable["id"], "name": "更新校验联系人", "mobile": "12345",
	})
	if upStatus != http.StatusBadRequest {
		t.Fatalf("更新手机非法：HTTP %d，期望 400", upStatus)
	}

	// 非法值不落库。
	_, total := pageContacts(t, env, admin, "&name=手机非法")
	if total != 0 {
		t.Fatalf("非法手机号联系人被落库：total=%d，期望 0", total)
	}

	// 合法边界：手机/邮箱留空可建（选填字段）。
	createContact(t, env, admin, customerID, "空白联系方式的联系人", nil)

	// 客户不存在：404，不建联系人。
	orphStatus, orphAPI := env.Request(t, http.MethodPost, "/crm/contact", admin, map[string]any{
		"customerId": 999999, "name": "孤儿联系人",
	})
	if orphStatus != http.StatusNotFound {
		t.Fatalf("客户不存在建联系人：HTTP %d code %d msg %q，期望 404", orphStatus, orphAPI.Code, orphAPI.Msg)
	}
	if _, total := pageContacts(t, env, admin, "&name=孤儿联系人"); total != 0 {
		t.Fatal("孤儿联系人被落库")
	}
}

// TestIntegrationContactScopeFollowsCustomer 可见性跟随父客户（spec §4.1）：
// 联系人自身没有负责人，5 种数据范围经客户传导——客户可见则其联系人可见。
// 复用 #8 的 5 范围观察场景（setupScopeEnv）：owner_rnd(101)/owner_mkt(102)/
// owner_mkt2(102)/owner_sub(市场二组)/观察者(102) 各有一家客户。
func TestIntegrationContactScopeFollowsCustomer(t *testing.T) {
	// setup 在每家客户下建一个联系人（姓名 = 联系人_ + 客户名），返回
	// 观察者用户名与其可见联系人姓名全集，供各数据范围子测试取交集断言。
	setup := func(t *testing.T, roleCode string, dataScope int, customDeptIDs []int64) (*testutil.IntegrationEnv, string, map[string]bool) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, observerCustomer := setupScopeEnv(t, env, admin, roleCode, dataScope, customDeptIDs, contactMenuIDs...)

		list, _ := pageCustomers(t, env, admin, "")
		all := make(map[string]bool, len(list))
		for _, row := range list {
			name := "联系人_" + row["name"].(string)
			createContact(t, env, admin, int64(row["id"].(float64)), name, nil)
			all[name] = true
		}
		if !all["联系人_"+observerCustomer] {
			t.Fatalf("场景自检：观察者客户 %q 下应已建联系人，全集 %v", observerCustomer, keysOf(all))
		}
		return env, observer, all
	}

	// 客户可见性期望与 #8 客户矩阵一致（联系人姓名 = 联系人_客户名，精确匹配）。
	// own 占位观察者自己客户下的联系人（姓名依赖观察者用户名，运行时填入）。
	cases := []struct {
		name      string
		roleCode  string
		scope     int
		customIDs []int64
		want      []string
		notWant   []string
	}{
		{
			name:     "全部数据",
			roleCode: "ct_all",
			scope:    1,
			want:     []string{"联系人_客户_of_owner_rnd", "联系人_客户_of_owner_mkt", "联系人_客户_of_owner_mkt2", "联系人_客户_of_owner_sub", "own"},
			notWant:  nil,
		},
		{
			name:      "自定义部门_精确集合",
			roleCode:  "ct_custom",
			scope:     2,
			customIDs: []int64{101, 103},
			want:      []string{"联系人_客户_of_owner_rnd"},
			notWant:   []string{"联系人_客户_of_owner_mkt", "联系人_客户_of_owner_mkt2", "联系人_客户_of_owner_sub", "own"},
		},
		{
			name:     "本部门",
			roleCode: "ct_dept",
			scope:    3,
			want:     []string{"联系人_客户_of_owner_mkt", "联系人_客户_of_owner_mkt2", "own"},
			notWant:  []string{"联系人_客户_of_owner_rnd", "联系人_客户_of_owner_sub"},
		},
		{
			name:     "本部门及以下",
			roleCode: "ct_deptsub",
			scope:    4,
			want:     []string{"联系人_客户_of_owner_mkt", "联系人_客户_of_owner_mkt2", "联系人_客户_of_owner_sub", "own"},
			notWant:  []string{"联系人_客户_of_owner_rnd"},
		},
		{
			name:     "仅本人",
			roleCode: "ct_self",
			scope:    5,
			want:     []string{"own"},
			notWant:  []string{"联系人_客户_of_owner_rnd", "联系人_客户_of_owner_mkt", "联系人_客户_of_owner_mkt2", "联系人_客户_of_owner_sub"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, observer, all := setup(t, tc.roleCode, tc.scope, tc.customIDs)
			own := "联系人_客户_of_" + observer
			got := visibleContactNames(t, env, observer)
			for name := range got {
				if !all[name] {
					t.Fatalf("%s：出现未知联系人 %q，全集 %v", tc.name, name, keysOf(all))
				}
			}
			for _, want := range tc.want {
				if want == "own" {
					want = own
				}
				if !got[want] {
					t.Fatalf("%s：应可见联系人 %q，实际 %v", tc.name, want, keysOf(got))
				}
			}
			for _, notWant := range tc.notWant {
				if notWant == "own" {
					notWant = own
				}
				if got[notWant] {
					t.Fatalf("%s：不应可见联系人 %q，实际 %v", tc.name, notWant, keysOf(got))
				}
			}
		})
	}
}

// TestIntegrationContactOutsideScope404 客户范围外的联系人详情/修改/删除一律 404，
// 在不可见客户下建联系人也 404（客户必须存在且可见才能建联系人）。
// 观察者绑定完整联系人按钮权限——403（无权限码）由 TestIntegrationContactPermissionDenied 覆盖。
func TestIntegrationContactOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer, observerCustomer := setupScopeEnv(t, env, admin, "ct_404", 5, nil,
		contactMenuIDs...)
	observerToken := env.Login(t, observer, "123456")

	// 观察者自己客户下的联系人可正常访问。
	ownList, _ := pageCustomers(t, env, admin, "&name="+observerCustomer)
	ownCustomerID := int64(ownList[0]["id"].(float64))
	own := createContact(t, env, admin, ownCustomerID, "观察者的联系人", nil)
	status, _ := getContact(t, env, observerToken, int64(own["id"].(float64)))
	if status != http.StatusOK {
		t.Fatalf("范围内联系人详情：HTTP %d，期望 200", status)
	}

	// admin 在他人客户下建联系人，观察者不可见。
	rndID := findUserIDByUsername(t, env, admin, "owner_rnd")
	other := createCustomer(t, env, admin, "研发专属客户404", map[string]any{"ownerUserId": rndID})
	otherCustomerID := int64(other["id"].(float64))
	hidden := createContact(t, env, admin, otherCustomerID, "研发专属联系人", nil)
	hiddenID := int64(hidden["id"].(float64))

	// 详情 404。
	status, _ = getContact(t, env, observerToken, hiddenID)
	if status != http.StatusNotFound {
		t.Fatalf("范围外联系人详情：HTTP %d，期望 404", status)
	}
	// 修改 404。
	status, _ = env.Request(t, http.MethodPut, "/crm/contact", observerToken, map[string]any{
		"id": hiddenID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外联系人修改：HTTP %d，期望 404", status)
	}
	// 删除 404。
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contact/%d", hiddenID), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外联系人删除：HTTP %d，期望 404", status)
	}
	// 在不可见客户下建联系人 404（存在性不泄露：与详情一致）。
	status, _ = env.Request(t, http.MethodPost, "/crm/contact", observerToken, map[string]any{
		"customerId": otherCustomerID, "name": "越权新增联系人",
	})
	if status != http.StatusNotFound {
		t.Fatalf("不可见客户下建联系人：HTTP %d，期望 404", status)
	}
	// 列表也看不到（不可见客户下的联系人不在列表中）。
	got := visibleContactNames(t, env, observer)
	if got["研发专属联系人"] || got["越权新增联系人"] {
		t.Fatalf("范围外联系人不应出现在列表，实际 %v", keysOf(got))
	}
}

// TestIntegrationContactPermissionDenied 无 crm:contact:* 权限码一律 403：
// 观察者只绑客户页面菜单（401），未绑联系人按钮（4014–4017）。
func TestIntegrationContactPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "无权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contact := createContact(t, env, admin, customerID, "存在但无权查看的联系人", nil)
	contactID := int64(contact["id"].(float64))

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色", "code": "ct_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "分页", method: http.MethodGet, path: "/crm/contact/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: fmt.Sprintf("/crm/contact/%d", contactID)},
		{name: "新建", method: http.MethodPost, path: "/crm/contact", body: map[string]any{"customerId": customerID, "name": "越权联系人"}},
		{name: "修改", method: http.MethodPut, path: "/crm/contact", body: map[string]any{"id": contactID, "name": "越权改名"}},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/crm/contact/%d", contactID)},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// admin 有全部权限码，对照可正常访问。
	if status, api := env.Request(t, http.MethodGet, "/crm/contact/page?pageNo=1&pageSize=10", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询联系人分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationContactTenantIsolation 租户隔离：租户 2 看不到租户 1 的联系人，
// 跨租户详情/修改 404，在跨租户客户下建联系人也 404。
func TestIntegrationContactTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "租户一隔离客户", nil)
	customerID := int64(customer["id"].(float64))
	contact := createContact(t, env, admin, customerID, "租户一联系人", nil)
	contactID := int64(contact["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "联系人隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 列表为空，跨租户详情 404。
	if _, total := pageContacts(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 联系人 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := getContact(t, env, token2, contactID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 联系人详情：HTTP %d，期望 404", status)
	}

	// 租户 2 不能修改租户 1 的联系人。
	status, _ = env.Request(t, http.MethodPut, "/crm/contact", token2, map[string]any{
		"id": contactID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 联系人：HTTP %d，期望 404", status)
	}

	// 租户 2 不能在租户 1 的客户下建联系人（客户不可见）。
	status, _ = env.Request(t, http.MethodPost, "/crm/contact", token2, map[string]any{
		"customerId": customerID, "name": "跨租户联系人",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 客户下建联系人：HTTP %d，期望 404", status)
	}

	// 租户 1 的数据不受影响。
	if _, total := pageContacts(t, env, admin, ""); total != 1 {
		t.Fatalf("租户 1 联系人 total=%d，期望 1", total)
	}
}
