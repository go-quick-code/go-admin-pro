//go:build integration

// 公海纵切 HTTP 集成测试（issue #13，docs/crm-mvp-spec.md §4.1/§4.2）：
// 释放/认领/转移全链路、认领上限、并发认领互斥、公海列表可见性
// （受权限码控制、不受数据范围过滤）、负责人历史完整性、租户隔离。
// 走 #3 基座的真实 router + 生产中间件 + 真实 MySQL，复用 #8 的客户测试助手。
package crm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---------- 公海接口助手（原始返回，供成功/失败双向断言） ----------

// releaseCustomerRaw 调释放接口，返回 HTTP 状态与响应信封。
func releaseCustomerRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, reason string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/customer/release", token, map[string]any{"id": id, "reason": reason})
}

// claimCustomerRaw 调认领接口，返回 HTTP 状态与响应信封。
func claimCustomerRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/customer/claim", token, map[string]any{"id": id})
}

// transferCustomerRaw 调转移接口，返回 HTTP 状态与响应信封。
func transferCustomerRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id, toOwnerUserID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/customer/transfer", token, map[string]any{"id": id, "toOwnerUserId": toOwnerUserID})
}

// pagePoolCustomers 查询公海分页并返回 list 与 total。
func pagePoolCustomers(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/customer/pool/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询公海分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析公海分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// pageOwnerHistory 查询客户负责人历史分页（新→旧）并返回 list 与 total。
func pageOwnerHistory(t *testing.T, env *testutil.IntegrationEnv, token string, customerID int64) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		fmt.Sprintf("/crm/customer/%d/owner-history?pageNo=1&pageSize=50", customerID), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询负责人历史：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析负责人历史: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// poolCustomerMenuID 是种子中的公海菜单 ID（含 crm:customer:pool:view 权限码）。
const poolCustomerMenuID = 402

// poolButtonMenuID 是公海页认领按钮菜单 ID（crm:customer:claim）。
const poolButtonMenuID = 4021

// claimRoleFor 创建仅绑定公海菜单（pool:view + claim）的角色并返回角色 ID。
func claimRoleFor(t *testing.T, env *testutil.IntegrationEnv, admin, code string, dataScope int) int64 {
	t.Helper()
	return createRoleByAPI(t, env, admin, map[string]any{
		"name": "公海认领角色", "code": code, "dataScope": dataScope,
		"menuIds": []int64{poolCustomerMenuID, poolButtonMenuID},
	})
}

// ---------- 释放 + 公海列表 ----------

// TestIntegrationCustomerReleaseAndPoolList 释放全链路：owner/部门置空、池字段落值、
// 普通客户列表排除公海客户、公海列表可见、重复释放拒绝、历史记录 create+release。
func TestIntegrationCustomerReleaseAndPoolList(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	c := createCustomer(t, env, admin, "公海流转客户", nil)
	id := int64(c["id"].(float64))

	// 释放（admin 持通配权限码，属于「有权限者」路径）。
	status, api := releaseCustomerRaw(t, env, admin, id, "暂不跟进")
	mustOK(t, "释放客户", status, api)

	// 详情回读：负责人与部门快照清空，池字段落值。
	st, api := getCustomer(t, env, admin, id)
	mustOK(t, "释放后查详情", st, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(0) || detail["ownerDeptId"] != float64(0) {
		t.Fatalf("释放后 owner 应为空：ownerUserId=%v ownerDeptId=%v", detail["ownerUserId"], detail["ownerDeptId"])
	}
	if detail["poolEnterTime"] == nil || detail["poolEnterTime"] == "" {
		t.Fatal("释放后应落 pool_enter_time")
	}
	if detail["releaseReason"] != "暂不跟进" {
		t.Fatalf("释放原因应落库，实际 %v", detail["releaseReason"])
	}

	// 普通客户列表排除公海客户——即使数据范围是「全部」。
	_, total := pageCustomers(t, env, admin, "&name=公海流转客户")
	if total != 0 {
		t.Fatalf("普通客户列表应排除公海客户：total=%d，期望 0", total)
	}

	// 公海列表可见，池字段随行返回。
	poolList, poolTotal := pagePoolCustomers(t, env, admin, "&name=公海流转客户")
	if poolTotal != 1 || len(poolList) != 1 {
		t.Fatalf("公海列表 total=%d，期望 1", poolTotal)
	}
	if poolList[0]["poolEnterTime"] == nil || poolList[0]["poolEnterTime"] == "" {
		t.Fatal("公海列表行应带 pool_enter_time")
	}
	if poolList[0]["releaseReason"] != "暂不跟进" {
		t.Fatalf("公海列表行应带释放原因，实际 %v", poolList[0]["releaseReason"])
	}

	// 重复释放 → 400。
	status, api = releaseCustomerRaw(t, env, admin, id, "再释放")
	if status != http.StatusBadRequest {
		t.Fatalf("重复释放：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 历史完整（新→旧）：release（from=admin，to=0）+ create（to=admin）。
	rows, histTotal := pageOwnerHistory(t, env, admin, id)
	if histTotal != 2 || len(rows) != 2 {
		t.Fatalf("负责人历史应 2 行（create+release），实际 %d", histTotal)
	}
	if rows[0]["action"] != "release" || rows[0]["fromOwnerUserId"] != float64(1) || rows[0]["toOwnerUserId"] != float64(0) {
		t.Fatalf("最新历史应为 release（from=admin,to=0），实际 %v", rows[0])
	}
	if rows[0]["fromOwnerUserName"] != "超级管理员" {
		t.Fatalf("release 历史应带原负责人昵称，实际 %v", rows[0]["fromOwnerUserName"])
	}
	if rows[0]["operatorUserName"] != "超级管理员" {
		t.Fatalf("release 历史应带操作人昵称，实际 %v", rows[0]["operatorUserName"])
	}
	if rows[1]["action"] != "create" || rows[1]["toOwnerUserId"] != float64(1) || rows[1]["fromOwnerUserId"] != float64(0) {
		t.Fatalf("次新历史应为 create（to=admin），实际 %v", rows[1])
	}
}

// ---------- 认领：上限 + 并发互斥 ----------

// claimOnce 在独立 goroutine 中发一次认领请求：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），结果带回主 goroutine 断言。
func claimOnce(env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse, error) {
	raw, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPut, env.APIPrefixURL("/crm/customer/claim"), bytes.NewReader(raw))
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return 0, testutil.APIResponse{}, err
	}
	return resp.StatusCode, api, nil
}

// TestIntegrationCustomerClaimLimitAndFlow 认领上限（规格 §4.2：名下客户总数
// 达 50 拒绝；释放一家降到 49 后可认领）：认领写历史并清空池字段。
func TestIntegrationCustomerClaimLimitAndFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	claimRole := claimRoleFor(t, env, admin, "claim_limit", 5)
	createUserByAPI(t, env, admin, "claimant", 101, []int64{claimRole})
	sellerID := findUserIDByUsername(t, env, admin, "claimant")
	seller := env.Login(t, "claimant", "123456")

	// 名下 50 家：认领上限打满。
	for i := 0; i < 50; i++ {
		createCustomer(t, env, admin, fmt.Sprintf("限量客户%02d", i), map[string]any{"ownerUserId": sellerID})
	}
	// 再备一家入公海。
	pool := createCustomer(t, env, admin, "上限边界客户", nil)
	poolID := int64(pool["id"].(float64))
	status, api := releaseCustomerRaw(t, env, admin, poolID, "上限测试")
	mustOK(t, "释放上限边界客户", status, api)

	// 名下 50 家时认领 → 400，文案含上限值。
	status, api = claimCustomerRaw(t, env, seller, poolID)
	if status != http.StatusBadRequest {
		t.Fatalf("满额认领：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if msg := api.Msg; !strings.Contains(msg, "认领上限") || !strings.Contains(msg, "50") {
		t.Fatalf("认领上限错误文案应含「认领上限」与 50，实际 %q", msg)
	}

	// 释放一家降到 49 → 认领成功，负责人/部门/池字段全部就位。
	sellerList, _ := pageCustomers(t, env, admin, fmt.Sprintf("&ownerUserId=%d", sellerID))
	if len(sellerList) != 50 {
		t.Fatalf("claimant 名下应 50 家，实际 %d", len(sellerList))
	}
	victimID := int64(sellerList[0]["id"].(float64))
	status, api = releaseCustomerRaw(t, env, admin, victimID, "腾出名额")
	mustOK(t, "释放腾名额", status, api)

	status, api = claimCustomerRaw(t, env, seller, poolID)
	mustOK(t, "降额后认领", status, api)
	var claimed map[string]any
	if err := json.Unmarshal(api.Data, &claimed); err != nil {
		t.Fatalf("解析认领响应: %v", err)
	}
	if claimed["ownerUserId"] != float64(sellerID) || claimed["ownerDeptId"] != float64(101) {
		t.Fatalf("认领后 owner 应为 claimant(部门 101)，实际 %v/%v", claimed["ownerUserId"], claimed["ownerDeptId"])
	}
	if claimed["poolEnterTime"] != nil {
		t.Fatalf("认领后应清空池字段，实际 poolEnterTime=%v", claimed["poolEnterTime"])
	}
	if claimed["releaseReason"] != "" {
		t.Fatalf("认领后应清空释放原因，实际 %v", claimed["releaseReason"])
	}

	// 历史链完整：create → release → release(腾名额) → claim。
	rows, histTotal := pageOwnerHistory(t, env, admin, poolID)
	if histTotal != 3 || len(rows) != 3 {
		t.Fatalf("上限边界客户历史应 3 行（create+release+claim），实际 %d", histTotal)
	}
	if rows[0]["action"] != "claim" || rows[0]["toOwnerUserId"] != float64(sellerID) || rows[0]["operatorUserName"] != "claimant" {
		t.Fatalf("最新历史应为 claim（to=claimant，操作人 claimant），实际 %v", rows[0])
	}

	// 公海不再出现该客户。
	_, poolTotal := pagePoolCustomers(t, env, seller, "&name=上限边界客户")
	if poolTotal != 0 {
		t.Fatalf("认领后公海 total=%d，期望 0", poolTotal)
	}
}

// TestIntegrationCustomerConcurrentClaim 并发认领互斥：同一公海客户两个用户
// 同时认领，恰好一个成功一个冲突，历史只落一条 claim。
func TestIntegrationCustomerConcurrentClaim(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	roleA := claimRoleFor(t, env, admin, "claim_race_a", 5)
	roleB := claimRoleFor(t, env, admin, "claim_race_b", 5)
	createUserByAPI(t, env, admin, "racer_a", 101, []int64{roleA})
	createUserByAPI(t, env, admin, "racer_b", 102, []int64{roleB})
	tokenA := env.Login(t, "racer_a", "123456")
	tokenB := env.Login(t, "racer_b", "123456")

	c := createCustomer(t, env, admin, "争抢客户", nil)
	id := int64(c["id"].(float64))
	status, api := releaseCustomerRaw(t, env, admin, id, "并发认领")
	mustOK(t, "释放争抢客户", status, api)

	type claimResult struct {
		status int
		api    testutil.APIResponse
		err    error
	}
	results := make([]claimResult, 2)
	var wg sync.WaitGroup
	tokens := []string{tokenA, tokenB}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, ap, err := claimOnce(env, tokens[i], id)
			results[i] = claimResult{st, ap, err}
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("并发认领 #%d 请求失败: %v", i, r.err)
		}
	}
	okCount, conflictCount := 0, 0
	for _, r := range results {
		switch {
		case r.status == http.StatusOK && r.api.Code == 0:
			okCount++
		case r.status == http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("并发认领出现意外结果：HTTP %d code %d msg %q", r.status, r.api.Code, r.api.Msg)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("并发认领应 1 成功 1 冲突，实际成功 %d 冲突 %d", okCount, conflictCount)
	}

	// 落库归属唯一：owner 是胜者，历史 claim 只有一条。
	st, api := getCustomer(t, env, admin, id)
	mustOK(t, "认领后查详情", st, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	winnerA := detail["ownerUserId"] == float64(findUserIDByUsername(t, env, admin, "racer_a"))
	winnerB := detail["ownerUserId"] == float64(findUserIDByUsername(t, env, admin, "racer_b"))
	if !winnerA && !winnerB {
		t.Fatalf("认领后 owner 应为两位竞争者之一，实际 %v", detail["ownerUserId"])
	}
	rows, _ := pageOwnerHistory(t, env, admin, id)
	claimRows := 0
	for _, row := range rows {
		if row["action"] == "claim" {
			claimRows++
		}
	}
	if claimRows != 1 {
		t.Fatalf("历史 claim 行应恰 1 条，实际 %d", claimRows)
	}
}

// ---------- 转移 + 操作权限矩阵 ----------

// TestIntegrationCustomerTransferAndPermissions 转移全链路与释放/转移的操作权限：
// 负责人本人放行（无按钮码）、同租户他人须持码、无码无归属 403；
// 公海客户不可转移、目标用户必须存在；编辑路径改负责人同样落 transfer 历史。
func TestIntegrationCustomerTransferAndPermissions(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	createUserByAPI(t, env, admin, "owner_a", 101, nil)
	createUserByAPI(t, env, admin, "owner_b", 102, nil)
	aID := findUserIDByUsername(t, env, admin, "owner_a")
	bID := findUserIDByUsername(t, env, admin, "owner_b")
	tokenA := env.Login(t, "owner_a", "123456")

	// 观察者：数据范围「全部」+ 客户查询码，但无释放/转移按钮码 —— 能看见、不能动。
	lookRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "只读观察角色", "code": "pool_look", "dataScope": 1,
		"menuIds": []int64{crmCustomerMenuID},
	})
	createUserByAPI(t, env, admin, "looker", 103, []int64{lookRole})
	tokenLooker := env.Login(t, "looker", "123456")

	c := createCustomer(t, env, admin, "流转转移客户", map[string]any{"ownerUserId": aID})
	id := int64(c["id"].(float64))

	// 无归属无码的观察者：释放/转移一律 403（可见但越权）。
	for _, tc := range []struct {
		name string
		call func() (int, testutil.APIResponse)
	}{
		{"释放", func() (int, testutil.APIResponse) { return releaseCustomerRaw(t, env, tokenLooker, id, "越权释放") }},
		{"转移", func() (int, testutil.APIResponse) { return transferCustomerRaw(t, env, tokenLooker, id, bID) }},
	} {
		status, api := tc.call()
		if status != http.StatusForbidden {
			t.Fatalf("观察者%s：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// 负责人本人（无任何按钮码）转移给同租户其他用户：放行。
	status, api := transferCustomerRaw(t, env, tokenA, id, bID)
	mustOK(t, "负责人本人转移", status, api)
	st, api := getCustomer(t, env, admin, id)
	mustOK(t, "转移后查详情", st, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(bID) || detail["ownerDeptId"] != float64(102) {
		t.Fatalf("转移后 owner 应为 owner_b(部门 102)，实际 %v/%v", detail["ownerUserId"], detail["ownerDeptId"])
	}

	// 新负责人本人（无码）再释放：负责人路径放行，池字段落值。
	tokenB := env.Login(t, "owner_b", "123456")
	status, api = releaseCustomerRaw(t, env, tokenB, id, "负责人自释")
	mustOK(t, "负责人本人释放", status, api)

	// 公海客户不可转移。
	status, api = transferCustomerRaw(t, env, admin, id, aID)
	if status != http.StatusBadRequest {
		t.Fatalf("公海客户转移：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	// 转移目标用户不存在 → 400。
	status, api = transferCustomerRaw(t, env, admin, id, 99999)
	_ = status // 已在公海，此处直接期待 400（公海拦截在前）。
	if status != http.StatusBadRequest {
		t.Fatalf("公海客户转移不存在的用户：HTTP %d，期望 400", status)
	}

	// admin（持码）认领到名下再转移给不存在用户 → 400（目标校验）。
	status, api = claimCustomerRaw(t, env, admin, id)
	mustOK(t, "admin 认领", status, api)
	status, api = transferCustomerRaw(t, env, admin, id, 99999)
	if status != http.StatusBadRequest {
		t.Fatalf("转移到不存在用户：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 编辑路径改负责人 = 转移：历史落 transfer 行。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/customer", admin, map[string]any{
		"id": id, "name": "流转转移客户", "ownerUserId": aID,
	})
	mustOK(t, "编辑改负责人", upStatus, upAPI)
	rows, histTotal := pageOwnerHistory(t, env, admin, id)
	if histTotal < 2 {
		t.Fatalf("负责人历史应含编辑转移行，实际 %d 行", histTotal)
	}
	latest := rows[0]
	if latest["action"] != "transfer" || latest["fromOwnerUserId"] != float64(1) || latest["toOwnerUserId"] != float64(aID) {
		t.Fatalf("最新历史应为编辑产生的 transfer（admin→owner_a），实际 %v", latest)
	}
}

// ---------- 公海可见性：权限码控制 + 不受数据范围过滤 ----------

// TestIntegrationCustomerPoolVisibility 公海列表受 crm:customer:pool:view 控制
// （403），但对持码用户不受数据范围过滤：仅本人范围也能看到全部公海客户。
func TestIntegrationCustomerPoolVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// demo 无任何 CRM 菜单 → 公海分页 403。
	demo := env.Login(t, "demo", "123456")
	status, api := env.Request(t, http.MethodGet, "/crm/customer/pool/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("demo 查公海：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 三家客户全部释放入公海。
	poolNames := []string{"可见公海甲", "可见公海乙", "可见公海丙"}
	for _, name := range poolNames {
		c := createCustomer(t, env, admin, name, nil)
		status, api := releaseCustomerRaw(t, env, admin, int64(c["id"].(float64)), "可见性测试")
		mustOK(t, "释放 "+name, status, api)
	}

	// 观察者：数据范围「仅本人」+ 公海菜单（pool:view + claim），无客户查询码。
	obsRole := claimRoleFor(t, env, admin, "pool_view_obs", 5)
	createUserByAPI(t, env, admin, "pool_observer", 103, []int64{obsRole})
	observer := env.Login(t, "pool_observer", "123456")

	// 公海列表不受数据范围过滤：仅本人范围仍看到全部 3 家。
	poolList, poolTotal := pagePoolCustomers(t, env, observer, "")
	if poolTotal != 3 || len(poolList) != 3 {
		t.Fatalf("仅本人观察者公海 total=%d，期望 3（不受数据范围过滤）", poolTotal)
	}
	seen := map[string]bool{}
	for _, row := range poolList {
		seen[row["name"].(string)] = true
	}
	for _, name := range poolNames {
		if !seen[name] {
			t.Fatalf("公海列表缺少 %q，实际 %v", name, keysOf(seen))
		}
	}

	// 无客户查询码：普通客户列表 403（可见性差异来自权限码而非数据范围）。
	status, api = env.Request(t, http.MethodGet, "/crm/customer/page?pageNo=1&pageSize=10", observer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("观察者查客户列表：HTTP %d，期望 403（无 crm:customer:query）", status)
	}

	// 观察者认领一家：认领接口挂 crm:customer:claim，不依赖客户查询码。
	target := poolList[0]
	status, api = claimCustomerRaw(t, env, observer, int64(target["id"].(float64)))
	mustOK(t, "观察者认领", status, api)

	// 认领后该客户从公海消失，剩余 2 家仍全部可见（依旧不受范围过滤）。
	_, poolTotal = pagePoolCustomers(t, env, observer, "")
	if poolTotal != 2 {
		t.Fatalf("认领后公海 total=%d，期望 2", poolTotal)
	}
}

// ---------- 租户隔离 ----------

// TestIntegrationCustomerPoolTenantIsolation 公海租户隔离：租户 2 看不到
// 租户 1 的公海客户，认领/历史访问一律 404；租户 1 的数据不受影响。
func TestIntegrationCustomerPoolTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	c := createCustomer(t, env, admin, "隔离公海客户", nil)
	id := int64(c["id"].(float64))
	status, api := releaseCustomerRaw(t, env, admin, id, "租户隔离")
	mustOK(t, "释放隔离客户", status, api)

	tenant2 := createTenantByAPI(t, env, admin, "公海隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 公海列表为空；认领与历史访问均 404（租户插件过滤，看不见即不存在）。
	_, poolTotal := pagePoolCustomers(t, env, token2, "")
	if poolTotal != 0 {
		t.Fatalf("租户 2 公海 total=%d，期望 0", poolTotal)
	}
	status, api = claimCustomerRaw(t, env, token2, id)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 认领租户 1 公海客户：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/crm/customer/%d/owner-history?pageNo=1&pageSize=10", id), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 查租户 1 客户历史：HTTP %d，期望 404", status)
	}

	// 租户 1 公海不受干扰。
	_, poolTotal = pagePoolCustomers(t, env, admin, "&name=隔离公海客户")
	if poolTotal != 1 {
		t.Fatalf("租户 1 公海 total=%d，期望 1", poolTotal)
	}
}
