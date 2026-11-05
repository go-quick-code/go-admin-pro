//go:build integration

// 跟进记录 HTTP 集成测试（issue #12）：走 #3 基座的真实 router + 生产中间件
// （JWT/权限/租户插件）+ 真实 MySQL，在 HTTP 契约上验证 CRUD、类型/内容/下次跟进
// 时间校验、可见性跟随父客户（spec §4.1：join 父记录后套用父谓词）、
// 新增跟进刷新客户 last_follow_time（spec §4.2）与租户隔离。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// followUpMenuIDs 是跟进 API 需要的权限码菜单：客户页面 401（含 crm:customer:query）
// + 跟进按钮 4040 查询 / 4041 新增 / 4042 修改 / 4043 删除。
var followUpMenuIDs = []int64{401, 4040, 4041, 4042, 4043}

// createFollowUp 在指定客户下创建跟进并断言成功，返回落库后的完整 JSON。
func createFollowUp(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64, content string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"customerId": customerID, "content": content}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/follow-up", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建跟进 %q：HTTP %d code %d msg %q，期望 HTTP 200 code 0", content, status, api.Code, api.Msg)
	}
	var followUp map[string]any
	if err := json.Unmarshal(api.Data, &followUp); err != nil {
		t.Fatalf("解析创建跟进响应: %v（data=%s）", err, string(api.Data))
	}
	return followUp
}

// pageFollowUps 查询跟进分页并返回 list 与 total。
func pageFollowUps(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/follow-up/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询跟进分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析跟进分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getFollowUp 查询跟进详情，返回 HTTP 状态与业务响应。
func getFollowUp(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/crm/follow-up/%d", id), token, nil)
}

// customerLastFollowTime 读客户详情，返回 lastFollowTime 的 JSON 值（无则 nil）。
func customerLastFollowTime(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64) any {
	t.Helper()
	status, api := getCustomer(t, env, token, customerID)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询客户详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析客户详情: %v（data=%s）", err, string(api.Data))
	}
	return detail["lastFollowTime"]
}

// TestIntegrationFollowUpCRUDAndRefresh 跟进 CRUD 全链路 + last_follow_time 刷新：
// 在客户下创建（类型缺省「其他」）、字段回读、按客户筛选、更新、软删除后不可见；
// 新增跟进在同一事务里刷新客户 last_follow_time（spec §4.2），
// 编辑/删除不回退刷新（取舍见提交说明：last_follow_time 语义是「最后一次跟进时刻」，
// 编辑内容或删除记录不改变「联系过」这个事实）。
func TestIntegrationFollowUpCRUDAndRefresh(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "跟进宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 新客户从未跟进：lastFollowTime 为空。
	if got := customerLastFollowTime(t, env, admin, customerID); got != nil {
		t.Fatalf("新客户 lastFollowTime 应为空，实际 %v", got)
	}

	// 创建：全字段回读。
	first := createFollowUp(t, env, admin, customerID, "电话沟通年度续约意向", map[string]any{
		"type": 1, "nextFollowTime": "2026-01-02 15:04:05",
	})
	if first["customerId"] != float64(customerID) {
		t.Fatalf("跟进 customerId=%v，期望 %d", first["customerId"], customerID)
	}
	if first["type"] != float64(1) || first["content"] != "电话沟通年度续约意向" {
		t.Fatalf("跟进字段不符：type=%v content=%v", first["type"], first["content"])
	}
	if first["nextFollowTime"] == nil || !strings.Contains(fmt.Sprint(first["nextFollowTime"]), "2026-01-02") {
		t.Fatalf("跟进 nextFollowTime=%v，期望含 2026-01-02", first["nextFollowTime"])
	}
	if first["customerName"] != "跟进宿主客户" {
		t.Fatalf("跟进应带所属客户名称，实际 customerName=%v", first["customerName"])
	}

	// 新增刷新 last_follow_time（读客户详情断言）。
	refreshed := customerLastFollowTime(t, env, admin, customerID)
	if refreshed == nil {
		t.Fatal("新增跟进后客户 lastFollowTime 仍为空，期望刷新")
	}

	// 类型缺省「其他」；第二次新增再次刷新。
	second := createFollowUp(t, env, admin, customerID, "微信发送报价资料", nil)
	if second["type"] != float64(5) {
		t.Fatalf("类型缺省应为 5（其他），实际 %v", second["type"])
	}
	again := customerLastFollowTime(t, env, admin, customerID)
	if again == nil || fmt.Sprint(again) == fmt.Sprint(refreshed) {
		t.Fatalf("第二次新增应再次刷新 lastFollowTime：%v → %v", refreshed, again)
	}
	afterCreates := again

	// 详情回读。
	status, api := getFollowUp(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询跟进详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["content"] != "电话沟通年度续约意向" || detail["type"] != float64(1) {
		t.Fatalf("详情字段不符：content=%v type=%v", detail["content"], detail["type"])
	}

	// 列表：按客户筛选出两条；列表行也带客户名称。
	if list, total := pageFollowUps(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 2 || len(list) != 2 {
		t.Fatalf("按客户筛选 total=%d list=%d，期望 2", total, len(list))
	}
	list, _ := pageFollowUps(t, env, admin, "")
	for _, row := range list {
		if row["customerName"] == "" {
			t.Fatalf("列表行缺 customerName：%v", row["content"])
		}
	}

	// 更新：内容与类型生效；customerId 不可漂移（更新接口不接受换客户）；
	// last_follow_time 不因编辑而变化（取舍：编辑不回退刷新）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/follow-up", admin, map[string]any{
		"id": first["id"], "content": "电话沟通改约下周", "type": 2, "nextFollowTime": "2026-02-01 10:00:00",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新跟进：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	status, api = getFollowUp(t, env, admin, int64(first["id"].(float64)))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("更新后查询详情：HTTP %d code %d", status, api.Code)
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["content"] != "电话沟通改约下周" || detail["type"] != float64(2) {
		t.Fatalf("更新未生效：content=%v type=%v", detail["content"], detail["type"])
	}
	if detail["customerId"] != float64(customerID) {
		t.Fatalf("更新不应改所属客户：customerId=%v", detail["customerId"])
	}
	if got := customerLastFollowTime(t, env, admin, customerID); fmt.Sprint(got) != fmt.Sprint(afterCreates) {
		t.Fatalf("编辑跟进不应刷新 last_follow_time：%v → %v", afterCreates, got)
	}

	// 软删除：详情 404、列表不可见；last_follow_time 同样不回退。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/follow-up/%d", int64(second["id"].(float64))), admin, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除跟进：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getFollowUp(t, env, admin, int64(second["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	if _, total := pageFollowUps(t, env, admin, fmt.Sprintf("&customerId=%d", customerID)); total != 1 {
		t.Fatalf("删除后列表 total=1，期望 1（软删除不可见），实际 total=%d", total)
	}
	if got := customerLastFollowTime(t, env, admin, customerID); fmt.Sprint(got) != fmt.Sprint(afterCreates) {
		t.Fatalf("删除跟进不应回退 last_follow_time：%v → %v", afterCreates, got)
	}
}

// TestIntegrationFollowUpValidation 非法输入 400：内容必填/超长、类型枚举、
// 下次跟进时间格式；客户不存在时建跟进 404；非法值一律不落库。
func TestIntegrationFollowUpValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "跟进校验宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "内容为空", payload: map[string]any{"customerId": customerID, "content": ""}},
		{name: "内容缺失", payload: map[string]any{"customerId": customerID}},
		{name: "内容超长", payload: map[string]any{"customerId": customerID, "content": strings.Repeat("长", 1001)}},
		{name: "类型为 0", payload: map[string]any{"customerId": customerID, "content": "类型非法", "type": 0}},
		{name: "类型为 6", payload: map[string]any{"customerId": customerID, "content": "类型非法", "type": 6}},
		{name: "下次跟进时间非法", payload: map[string]any{"customerId": customerID, "content": "时间非法", "nextFollowTime": "明天"}},
		{name: "下次跟进时间非日期", payload: map[string]any{"customerId": customerID, "content": "时间非法", "nextFollowTime": "2026/01/02"}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/crm/follow-up", admin, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if api.Msg == "" {
			t.Fatalf("%s：应返回中文错误信息", tc.name)
		}
	}

	// 更新同样拒绝非法输入：先建一个合法跟进，再以非法类型更新。
	updatable := createFollowUp(t, env, admin, customerID, "更新校验跟进", nil)
	upStatus, _ := env.Request(t, http.MethodPut, "/crm/follow-up", admin, map[string]any{
		"id": updatable["id"], "content": "更新校验跟进", "type": 9,
	})
	if upStatus != http.StatusBadRequest {
		t.Fatalf("更新类型非法：HTTP %d，期望 400", upStatus)
	}

	// 非法值不落库。
	_, total := pageFollowUps(t, env, admin, "&customerId="+fmt.Sprint(customerID))
	if total != 1 {
		t.Fatalf("非法输入后应只剩 1 条合法跟进，实际 total=%d", total)
	}

	// 合法边界：类型未传（缺省其他）、下次跟进时间留空、内容恰好 1000 字。
	createFollowUp(t, env, admin, customerID, strings.Repeat("边", 1000), map[string]any{"nextFollowTime": ""})

	// 客户不存在：404，不落库。
	orphStatus, orphAPI := env.Request(t, http.MethodPost, "/crm/follow-up", admin, map[string]any{
		"customerId": 999999, "content": "孤儿跟进",
	})
	if orphStatus != http.StatusNotFound {
		t.Fatalf("客户不存在建跟进：HTTP %d code %d msg %q，期望 404", orphStatus, orphAPI.Code, orphAPI.Msg)
	}
	if _, total := pageFollowUps(t, env, admin, "&customerId=999999"); total != 0 {
		t.Fatal("孤儿跟进被落库")
	}
}

// TestIntegrationFollowUpScopeFollowsCustomer 可见性跟随父客户（spec §4.1）：
// 跟进自身没有负责人，5 种数据范围经客户传导——客户可见则其跟进可见。
// 复用 #8 的 5 范围观察场景（setupScopeEnv）：owner_rnd(101)/owner_mkt(102)/
// owner_mkt2(102)/owner_sub(市场二组)/观察者(102) 各有一家客户。
func TestIntegrationFollowUpScopeFollowsCustomer(t *testing.T) {
	// setup 在每家客户下建一条跟进（内容 = 跟进_ + 客户名），返回
	// 观察者用户名与其可见跟进内容全集，供各数据范围子测试取交集断言。
	setup := func(t *testing.T, roleCode string, dataScope int, customDeptIDs []int64) (*testutil.IntegrationEnv, string, map[string]bool) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, observerCustomer := setupScopeEnv(t, env, admin, roleCode, dataScope, customDeptIDs, followUpMenuIDs...)

		list, _ := pageCustomers(t, env, admin, "")
		all := make(map[string]bool, len(list))
		for _, row := range list {
			content := "跟进_" + row["name"].(string)
			createFollowUp(t, env, admin, int64(row["id"].(float64)), content, nil)
			all[content] = true
		}
		if !all["跟进_"+observerCustomer] {
			t.Fatalf("场景自检：观察者客户 %q 下应已建跟进，全集 %v", observerCustomer, keysOf(all))
		}
		return env, observer, all
	}

	// 客户可见性期望与 #8 客户矩阵一致（跟进内容 = 跟进_客户名，精确匹配）。
	// own 占位观察者自己客户下的跟进（内容依赖观察者用户名，运行时填入）。
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
			roleCode: "fu_all",
			scope:    1,
			want:     []string{"跟进_客户_of_owner_rnd", "跟进_客户_of_owner_mkt", "跟进_客户_of_owner_mkt2", "跟进_客户_of_owner_sub", "own"},
			notWant:  nil,
		},
		{
			name:      "自定义部门_精确集合",
			roleCode:  "fu_custom",
			scope:     2,
			customIDs: []int64{101, 103},
			want:      []string{"跟进_客户_of_owner_rnd"},
			notWant:   []string{"跟进_客户_of_owner_mkt", "跟进_客户_of_owner_mkt2", "跟进_客户_of_owner_sub", "own"},
		},
		{
			name:     "本部门",
			roleCode: "fu_dept",
			scope:    3,
			want:     []string{"跟进_客户_of_owner_mkt", "跟进_客户_of_owner_mkt2", "own"},
			notWant:  []string{"跟进_客户_of_owner_rnd", "跟进_客户_of_owner_sub"},
		},
		{
			name:     "本部门及以下",
			roleCode: "fu_deptsub",
			scope:    4,
			want:     []string{"跟进_客户_of_owner_mkt", "跟进_客户_of_owner_mkt2", "跟进_客户_of_owner_sub", "own"},
			notWant:  []string{"跟进_客户_of_owner_rnd"},
		},
		{
			name:     "仅本人",
			roleCode: "fu_self",
			scope:    5,
			want:     []string{"own"},
			notWant:  []string{"跟进_客户_of_owner_rnd", "跟进_客户_of_owner_mkt", "跟进_客户_of_owner_mkt2", "跟进_客户_of_owner_sub"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, observer, all := setup(t, tc.roleCode, tc.scope, tc.customIDs)
			own := "跟进_客户_of_" + observer
			token := env.Login(t, observer, "123456")
			gotList, _ := pageFollowUps(t, env, token, "")
			got := make(map[string]bool, len(gotList))
			for _, row := range gotList {
				got[row["content"].(string)] = true
			}
			for content := range got {
				if !all[content] {
					t.Fatalf("%s：出现未知跟进 %q，全集 %v", tc.name, content, keysOf(all))
				}
			}
			for _, want := range tc.want {
				if want == "own" {
					want = own
				}
				if !got[want] {
					t.Fatalf("%s：应可见跟进 %q，实际 %v", tc.name, want, keysOf(got))
				}
			}
			for _, notWant := range tc.notWant {
				if notWant == "own" {
					notWant = own
				}
				if got[notWant] {
					t.Fatalf("%s：不应可见跟进 %q，实际 %v", tc.name, notWant, keysOf(got))
				}
			}
		})
	}
}

// TestIntegrationFollowUpOutsideScope404 客户范围外的跟进详情/修改/删除一律 404，
// 在不可见客户下建跟进也 404（客户必须存在且可见才能建跟进）。
// 观察者绑定完整跟进按钮权限——403（无权限码）由 TestIntegrationFollowUpPermissionDenied 覆盖。
func TestIntegrationFollowUpOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer, observerCustomer := setupScopeEnv(t, env, admin, "fu_404", 5, nil,
		followUpMenuIDs...)
	observerToken := env.Login(t, observer, "123456")

	// 观察者自己客户下的跟进可正常访问。
	ownList, _ := pageCustomers(t, env, admin, "&name="+observerCustomer)
	ownCustomerID := int64(ownList[0]["id"].(float64))
	own := createFollowUp(t, env, admin, ownCustomerID, "观察者的跟进", nil)
	status, _ := getFollowUp(t, env, observerToken, int64(own["id"].(float64)))
	if status != http.StatusOK {
		t.Fatalf("范围内跟进详情：HTTP %d，期望 200", status)
	}

	// admin 在他人客户下建跟进，观察者不可见。
	rndID := findUserIDByUsername(t, env, admin, "owner_rnd")
	other := createCustomer(t, env, admin, "研发专属客户404跟进", map[string]any{"ownerUserId": rndID})
	otherCustomerID := int64(other["id"].(float64))
	hidden := createFollowUp(t, env, admin, otherCustomerID, "研发专属跟进", nil)
	hiddenID := int64(hidden["id"].(float64))

	// 详情 404。
	status, _ = getFollowUp(t, env, observerToken, hiddenID)
	if status != http.StatusNotFound {
		t.Fatalf("范围外跟进详情：HTTP %d，期望 404", status)
	}
	// 修改 404（观察者非创建人，但这里客户范围先行，仍是 404 而非 403）。
	status, _ = env.Request(t, http.MethodPut, "/crm/follow-up", observerToken, map[string]any{
		"id": hiddenID, "content": "越权改写",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外跟进修改：HTTP %d，期望 404", status)
	}
	// 删除 404。
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/follow-up/%d", hiddenID), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外跟进删除：HTTP %d，期望 404", status)
	}
	// 在不可见客户下建跟进 404（存在性不泄露：与详情一致）。
	status, _ = env.Request(t, http.MethodPost, "/crm/follow-up", observerToken, map[string]any{
		"customerId": otherCustomerID, "content": "越权新增跟进",
	})
	if status != http.StatusNotFound {
		t.Fatalf("不可见客户下建跟进：HTTP %d，期望 404", status)
	}
	// 列表也看不到（不可见客户下的跟进不在列表中）。
	token := observerToken
	gotList, _ := pageFollowUps(t, env, token, "")
	for _, row := range gotList {
		if row["content"] == "研发专属跟进" || row["content"] == "越权新增跟进" {
			t.Fatalf("范围外跟进不应出现在列表：%v", row["content"])
		}
	}
}

// TestIntegrationFollowUpPermissionDenied 权限口径（issue #12）：
//   - 查询/新增：无 crm:follow-up:query/create 权限码一律 403（路由中间件）；
//   - 修改/删除：创建人本人放行（即使不持按钮码）；非创建人无权限码 403。
func TestIntegrationFollowUpPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "无权限宿主客户跟进", nil)
	customerID := int64(customer["id"].(float64))
	followUp := createFollowUp(t, env, admin, customerID, "存在但无权操作的跟进", nil)
	followUpID := int64(followUp["id"].(float64))

	// 观察者只绑「跟进查询+新增」（4040/4041），无修改/删除按钮码（4042/4043）。
	creatorRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "跟进创建者角色", "code": "fu_creator", "dataScope": 1, "menuIds": []int64{401, 4040, 4041},
	})
	createUserByAPI(t, env, admin, "fu_creator_user", 102, []int64{creatorRole})
	creatorToken := env.Login(t, "fu_creator_user", "123456")

	// 创建人本人在可见客户下建跟进（有 create 码），随后无 4042/4043 也能改/删自己的记录。
	own := createFollowUp(t, env, creatorToken, customerID, "创建人自己的跟进", nil)
	ownID := int64(own["id"].(float64))
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/follow-up", creatorToken, map[string]any{
		"id": ownID, "content": "创建人改自己的跟进",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("创建人修改自己的跟进：HTTP %d code %d msg %q，期望 200（创建人本人放行）", upStatus, upAPI.Code, upAPI.Msg)
	}
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/follow-up/%d", ownID), creatorToken, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("创建人删除自己的跟进：HTTP %d code %d msg %q，期望 200（创建人本人放行）", delStatus, delAPI.Code, delAPI.Msg)
	}

	// 无任何跟进权限码的用户（角色只绑客户页面 401）：
	// 分页/详情/新建 403（路由中间件）；修改/删除 admin 的跟进 403（非创建人且无权限码）。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色跟进", "code": "fu_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "fu_no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "fu_no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "分页", method: http.MethodGet, path: "/crm/follow-up/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: fmt.Sprintf("/crm/follow-up/%d", followUpID)},
		{name: "新建", method: http.MethodPost, path: "/crm/follow-up", body: map[string]any{"customerId": customerID, "content": "越权跟进"}},
		{name: "修改", method: http.MethodPut, path: "/crm/follow-up", body: map[string]any{"id": followUpID, "content": "越权改写"}},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/crm/follow-up/%d", followUpID)},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// admin 有全部权限码，对照可正常访问。
	if status, api := env.Request(t, http.MethodGet, "/crm/follow-up/page?pageNo=1&pageSize=10", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询跟进分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationFollowUpTenantIsolation 租户隔离：租户 2 看不到租户 1 的跟进，
// 跨租户详情/修改 404，在跨租户客户下建跟进也 404。
func TestIntegrationFollowUpTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	customer := createCustomer(t, env, admin, "租户一隔离客户跟进", nil)
	customerID := int64(customer["id"].(float64))
	followUp := createFollowUp(t, env, admin, customerID, "租户一跟进", nil)
	followUpID := int64(followUp["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "跟进隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 列表为空，跨租户详情 404。
	if _, total := pageFollowUps(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 跟进 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := getFollowUp(t, env, token2, followUpID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 跟进详情：HTTP %d，期望 404", status)
	}

	// 租户 2 不能修改租户 1 的跟进。
	status, _ = env.Request(t, http.MethodPut, "/crm/follow-up", token2, map[string]any{
		"id": followUpID, "content": "越权改写",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 跟进：HTTP %d，期望 404", status)
	}

	// 租户 2 不能在租户 1 的客户下建跟进（客户不可见）。
	status, _ = env.Request(t, http.MethodPost, "/crm/follow-up", token2, map[string]any{
		"customerId": customerID, "content": "跨租户跟进",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 客户下建跟进：HTTP %d，期望 404", status)
	}

	// 租户 1 的数据不受影响。
	if _, total := pageFollowUps(t, env, admin, ""); total != 1 {
		t.Fatalf("租户 1 跟进 total=%d，期望 1", total)
	}
}
