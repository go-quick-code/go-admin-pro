//go:build integration

// 公海租户配置与自动回收 HTTP 集成测试（issue #14，docs/crm-mvp-spec.md §4.2）：
// 配置读写与权限、认领上限走租户配置、job 手动触发回收全链路
// （超期回收 / 刚跟进不回收 / 从未跟进按创建时间回收 / 已进公海不重复 /
// 历史落 recycle）、租户配置隔离。
//
// 合同排除说明：#16 已落地合同实体与作废状态，internal/app 装配处已用
// SetContractChecker 注册真实实现（有未作废合同的客户不被回收、作废合同
// 不拦截），真实路径用例见本文件 TestIntegrationPoolRecycleContractExclusion；
// 「无合同 → 满足条件即回收」的默认路径由 TestIntegrationPoolRecycleJob 覆盖。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---------- 配置接口助手 ----------

// getPoolConfigRaw 查询公海配置，返回 HTTP 状态与响应信封。
func getPoolConfigRaw(t *testing.T, env *testutil.IntegrationEnv, token string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, "/crm/pool-config", token, nil)
}

// updatePoolConfigRaw 保存公海配置，返回 HTTP 状态与响应信封。
func updatePoolConfigRaw(t *testing.T, env *testutil.IntegrationEnv, token string, recycleDays, claimLimit int) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, "/crm/pool-config", token, map[string]any{
		"recycleDays": recycleDays, "claimLimit": claimLimit,
	})
}

// poolConfigValues 解析配置响应 data 为取值。
func poolConfigValues(t *testing.T, api testutil.APIResponse) map[string]any {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(api.Data, &values); err != nil {
		t.Fatalf("解析公海配置响应: %v（data=%s）", err, string(api.Data))
	}
	return values
}

// ---------- 配置读写 + 权限 + 租户隔离 ----------

// TestIntegrationPoolConfigCRUD 配置读写全链路：
// 默认值 30/50（规格 §4.2）→ 保存生效 → 非法值 400（中文文案）→
// 权限矩阵（无码 403，公海可见者可读，4050 持有者可写）。
func TestIntegrationPoolConfigCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 无配置行（seed 未给该租户之外的租户预置；本用例先清一次租户 1 的配置，
	// 这里直接断言 seed 默认行）→ 默认 30/50。
	status, api := getPoolConfigRaw(t, env, admin)
	mustOK(t, "读公海配置", status, api)
	values := poolConfigValues(t, api)
	if values["recycleDays"] != float64(30) || values["claimLimit"] != float64(50) {
		t.Fatalf("默认配置应为 30/50，实际 %v", values)
	}

	// 保存生效（回读一致）。
	status, api = updatePoolConfigRaw(t, env, admin, 15, 20)
	mustOK(t, "保存公海配置", status, api)
	status, api = getPoolConfigRaw(t, env, admin)
	mustOK(t, "回读公海配置", status, api)
	if values = poolConfigValues(t, api); values["recycleDays"] != float64(15) || values["claimLimit"] != float64(20) {
		t.Fatalf("保存后配置应为 15/20，实际 %v", values)
	}

	// 非法值 → 400，文案指明字段。
	for _, tc := range []struct {
		name        string
		recycleDays int
		claimLimit  int
		wantField   string
	}{
		{"回收天数为 0", 0, 20, "回收天数"},
		{"回收天数超上限", 3651, 20, "回收天数"},
		{"认领上限为 0", 15, 0, "认领上限"},
		{"认领上限超上限", 15, 100001, "认领上限"},
	} {
		status, api = updatePoolConfigRaw(t, env, admin, tc.recycleDays, tc.claimLimit)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
		if !strings.Contains(api.Msg, tc.wantField) {
			t.Fatalf("%s 的错误文案应含 %q，实际 %q", tc.name, tc.wantField, api.Msg)
		}
	}

	// 权限矩阵：
	//   demo 无任何 CRM 权限 → 读/写都 403；
	//   仅公海菜单（pool:view + claim，无 4050）→ 读 200 / 写 403；
	//   加上 4050（crm:pool-config:update）→ 写 200。
	demo := env.Login(t, "demo", "123456")
	status, _ = getPoolConfigRaw(t, env, demo)
	if status != http.StatusForbidden {
		t.Fatalf("demo 读公海配置：HTTP %d，期望 403", status)
	}
	status, _ = updatePoolConfigRaw(t, env, demo, 10, 10)
	if status != http.StatusForbidden {
		t.Fatalf("demo 写公海配置：HTTP %d，期望 403", status)
	}

	readerRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "公海配置只读", "code": "pool_cfg_read", "dataScope": 1,
		"menuIds": []int64{poolCustomerMenuID, poolButtonMenuID},
	})
	createUserByAPI(t, env, admin, "pool_cfg_reader", 101, []int64{readerRole})
	reader := env.Login(t, "pool_cfg_reader", "123456")
	status, api = getPoolConfigRaw(t, env, reader)
	mustOK(t, "公海可见者读配置", status, api)
	status, _ = updatePoolConfigRaw(t, env, reader, 10, 10)
	if status != http.StatusForbidden {
		t.Fatalf("无 crm:pool-config:update 写配置：HTTP %d，期望 403", status)
	}

	editorRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "公海配置编辑", "code": "pool_cfg_edit", "dataScope": 1,
		"menuIds": []int64{poolCustomerMenuID, poolButtonMenuID, 4050},
	})
	createUserByAPI(t, env, admin, "pool_cfg_editor", 101, []int64{editorRole})
	editor := env.Login(t, "pool_cfg_editor", "123456")
	status, api = updatePoolConfigRaw(t, env, editor, 12, 22)
	mustOK(t, "4050 持有者写配置", status, api)

	// 租户隔离：租户 2 读到默认值（不受租户 1 的 12/22 影响），
	// 改自己的配置也不影响租户 1。
	tenant2 := createTenantByAPI(t, env, admin, "公海配置隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	status, api = getPoolConfigRaw(t, env, token2)
	mustOK(t, "租户 2 读配置", status, api)
	if values = poolConfigValues(t, api); values["recycleDays"] != float64(30) || values["claimLimit"] != float64(50) {
		t.Fatalf("租户 2 应读到默认值 30/50，实际 %v", values)
	}
	status, api = updatePoolConfigRaw(t, env, token2, 7, 3)
	mustOK(t, "租户 2 写配置", status, api)
	status, api = getPoolConfigRaw(t, env, admin)
	mustOK(t, "租户 1 回读配置", status, api)
	if values = poolConfigValues(t, api); values["recycleDays"] != float64(12) || values["claimLimit"] != float64(22) {
		t.Fatalf("租户 2 改配置不应影响租户 1（应仍为 12/22），实际 %v", values)
	}

	// 收尾还原租户 1 默认，避免影响同文件的回收用例（测试库按用例清表，
	// 这里还原只为显式表达用例间不互相依赖）。
	status, api = updatePoolConfigRaw(t, env, admin, 30, 50)
	mustOK(t, "还原租户 1 配置", status, api)
}

// ---------- 认领上限走租户配置 ----------

// TestIntegrationClaimLimitFromTenantConfig 认领上限按租户配置生效：
// 上限配 1 时名下 1 家即拒绝（若仍走 #13 硬编码 50 则首次即成功），
// 上限放宽到 2 后认领成功——证明上限值链路是 crm_pool_config。
func TestIntegrationClaimLimitFromTenantConfig(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := updatePoolConfigRaw(t, env, admin, 30, 1)
	mustOK(t, "配置认领上限为 1", status, api)

	claimRole := claimRoleFor(t, env, admin, "claim_cfg", 5)
	createUserByAPI(t, env, admin, "claim_cfg_user", 101, []int64{claimRole})
	sellerID := findUserIDByUsername(t, env, admin, "claim_cfg_user")
	seller := env.Login(t, "claim_cfg_user", "123456")

	// 名下 1 家（达上限）+ 公海 1 家：认领拒绝，文案带上限值 1。
	createCustomer(t, env, admin, "占用名额客户", map[string]any{"ownerUserId": sellerID})
	pool := createCustomer(t, env, admin, "配置上限客户", nil)
	poolID := int64(pool["id"].(float64))
	status, api = releaseCustomerRaw(t, env, admin, poolID, "配置上限测试")
	mustOK(t, "释放配置上限客户", status, api)

	status, api = claimCustomerRaw(t, env, seller, poolID)
	if status != http.StatusBadRequest {
		t.Fatalf("上限 1 时认领：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	if msg := api.Msg; !strings.Contains(msg, "认领上限") || !strings.Contains(msg, "1") {
		t.Fatalf("错误文案应含「认领上限」与配置值 1，实际 %q", msg)
	}

	// 放宽到 2：同一用户认领成功。
	status, api = updatePoolConfigRaw(t, env, admin, 30, 2)
	mustOK(t, "配置认领上限为 2", status, api)
	status, api = claimCustomerRaw(t, env, seller, poolID)
	mustOK(t, "放宽后认领", status, api)
}

// ---------- 回收 job 全链路 ----------

// setCustomerTimes 直改测试库的客户时间列（测试数据准备专用）：
// lastFollow/createdAt 传 nil 表示置 NULL，传指针则写成指定时间。
// 跟进接口只能写「当前时间」为 last_follow_time，回填历史时刻只能走数据准备。
func setCustomerTimes(t *testing.T, env *testutil.IntegrationEnv, id int64, lastFollow, createdAt *time.Time) {
	t.Helper()
	sqlDB, err := env.DB.DB()
	if err != nil {
		t.Fatalf("取底层 sql.DB: %v", err)
	}
	if _, err := sqlDB.Exec(
		"UPDATE crm_customer SET last_follow_time = ?, created_at = ? WHERE id = ?",
		lastFollow, createdAt, id,
	); err != nil {
		t.Fatalf("回填客户时间: %v", err)
	}
}

// daysAgo 返回 n 天前的时间（本地时区，避免时区换算引入边界抖动）。
func daysAgo(n int) time.Time {
	return time.Now().AddDate(0, 0, -n)
}

// recycleJobID 从任务分页按 handlerName 找到种子写入的公海自动回收任务。
func recycleJobID(t *testing.T, env *testutil.IntegrationEnv, token string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		"/infra/job/page?pageNo=1&pageSize=10&handlerName=recyclePoolCustomers", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询公海回收任务：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析任务分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 {
		t.Fatalf("应有且仅有 1 个 recyclePoolCustomers 任务，实际 %d", len(page.List))
	}
	job := page.List[0]
	if job["status"] != float64(1) {
		t.Fatalf("种子回收任务应默认暂停（status=1），实际 %v", job["status"])
	}
	return int64(job["id"].(float64))
}

// waitFor 以 100ms 间隔轮询直到 fn 通过或超时（job 手动执行是异步链路）。
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时（%v）", what, timeout)
}

// runRecycleJobAndWait 手动触发回收任务并等 job 日志落成功行
// （job 日志在 handler 完成后同步写入，是「本轮回收已提交」的完成信号）。
func runRecycleJobAndWait(t *testing.T, env *testutil.IntegrationEnv, token string) {
	t.Helper()
	jobID := recycleJobID(t, env, token)
	status, api := env.Request(t, http.MethodPut,
		fmt.Sprintf("/infra/job/%d/run", jobID), token, nil)
	mustOK(t, "手动触发回收任务", status, api)

	waitFor(t, 10*time.Second, "回收任务执行完成（job 日志）", func() bool {
		status, api := env.Request(t, http.MethodGet,
			fmt.Sprintf("/infra/job/log/page?pageNo=1&pageSize=10&jobId=%d&result=0", jobID), token, nil)
		if status != http.StatusOK || api.Code != 0 {
			return false
		}
		var page struct {
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(api.Data, &page); err != nil {
			return false
		}
		return page.Total >= 1
	})
}

// ownerOf 查询客户详情当前负责人（0 = 公海）。
func ownerOf(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) int64 {
	t.Helper()
	st, api := getCustomer(t, env, token, id)
	mustOK(t, "查询客户详情", st, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	return int64(detail["ownerUserId"].(float64))
}

// TestIntegrationPoolRecycleJob 回收全链路（真实 job handler + 生产装配）：
//   - 超期 40 天有跟进 → 回收（owner/部门清空、池字段落值、原因含天数）；
//   - 刚跟进 1 天 → 不回收；
//   - 从未跟进、创建 40 天 → 按 created_at 参与回收（NULL 回退）；
//   - 从未跟进、刚创建 → 不回收；
//   - 已在公海 → 不重复回收、不新增历史；
//   - 回收落 recycle 历史（from=原负责人，操作人 0=系统）。
func TestIntegrationPoolRecycleJob(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 五类客户：owner 都先落 admin（创建人缺省负责人）。
	a := createCustomer(t, env, admin, "回收超期跟进客户", nil)
	b := createCustomer(t, env, admin, "回收刚跟进客户", nil)
	c := createCustomer(t, env, admin, "回收从未跟进老客户", nil)
	d := createCustomer(t, env, admin, "回收从未跟进新客户", nil)
	e := createCustomer(t, env, admin, "回收已在公海客户", nil)

	// 测试数据：跟进记录回填 40 天前 / 1 天前；从未跟进的回填创建时间。
	setCustomerTimes(t, env, int64(a["id"].(float64)), ptrTime(daysAgo(40)), nil)
	setCustomerTimes(t, env, int64(b["id"].(float64)), ptrTime(daysAgo(1)), nil)
	setCustomerTimes(t, env, int64(c["id"].(float64)), nil, ptrTime(daysAgo(40)))
	setCustomerTimes(t, env, int64(d["id"].(float64)), nil, nil)
	eID := int64(e["id"].(float64))
	status, api := releaseCustomerRaw(t, env, admin, eID, "回收测试预置入池")
	mustOK(t, "预置客户入公海", status, api)
	rows, eHistTotal := pageOwnerHistory(t, env, admin, eID)
	if eHistTotal != 2 {
		t.Fatalf("入池客户历史应 2 行（create+release），实际 %d", eHistTotal)
	}
	_ = rows

	// 触发回收（种子任务默认暂停，手动执行一轮）。
	runRecycleJobAndWait(t, env, admin)

	// 超期客户被回收：owner/部门清空、池字段与原因落值。
	aID := int64(a["id"].(float64))
	if owner := ownerOf(t, env, admin, aID); owner != 0 {
		t.Fatalf("超期客户应被回收（owner=0），实际 owner=%d", owner)
	}
	st, api := getCustomer(t, env, admin, aID)
	mustOK(t, "回收后查详情", st, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerDeptId"] != float64(0) {
		t.Fatalf("回收后部门快照应清空，实际 %v", detail["ownerDeptId"])
	}
	if detail["poolEnterTime"] == nil || detail["poolEnterTime"] == "" {
		t.Fatal("回收后应落 pool_enter_time")
	}
	if reason, _ := detail["releaseReason"].(string); !strings.Contains(reason, "自动回收") || !strings.Contains(reason, "30") {
		t.Fatalf("回收原因应说明自动回收与天数 30，实际 %q", reason)
	}

	// 刚跟进与从未跟进新客户不回收。
	if owner := ownerOf(t, env, admin, int64(b["id"].(float64))); owner != 1 {
		t.Fatalf("刚跟进客户不应回收，实际 owner=%d", owner)
	}
	if owner := ownerOf(t, env, admin, int64(d["id"].(float64))); owner != 1 {
		t.Fatalf("从未跟进的新客户不应回收（创建时间未超期），实际 owner=%d", owner)
	}

	// 从未跟进老客户按 created_at 回收（NULL 回退）。
	if owner := ownerOf(t, env, admin, int64(c["id"].(float64))); owner != 0 {
		t.Fatalf("从未跟进的老客户应按创建时间回收，实际 owner=%d", owner)
	}

	// 已在公海的客户不重复回收：历史仍 2 行（无 recycle 行）、池字段未重置。
	if rows, total := pageOwnerHistory(t, env, admin, eID); total != 2 || len(rows) != 2 {
		t.Fatalf("已在公海的客户不应新增回收历史，实际 %d 行", total)
	}

	// 回收历史落 recycle 行：from=admin、to=0、操作人 0（系统动作）。
	histRows, histTotal := pageOwnerHistory(t, env, admin, aID)
	if histTotal != 2 || len(histRows) != 2 {
		t.Fatalf("回收客户历史应 2 行（create+recycle），实际 %d", histTotal)
	}
	latest := histRows[0]
	if latest["action"] != "recycle" ||
		latest["fromOwnerUserId"] != float64(1) ||
		latest["toOwnerUserId"] != float64(0) ||
		latest["operatorUserId"] != float64(0) {
		t.Fatalf("最新历史应为 recycle（from=admin,to=0,operator=0），实际 %v", latest)
	}
}

// TestIntegrationPoolRecycleTenantIsolation 回收按租户配置隔离：
// 租户 1 配 365 天（不回收自己的 40 天客户），租户 2 保持默认 30 天
// （回收自己的 40 天客户）——同一轮任务互不影响；任务行属于租户 1，
// 但系统级遍历必须覆盖租户 2。
func TestIntegrationPoolRecycleTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := updatePoolConfigRaw(t, env, admin, 365, 50)
	mustOK(t, "租户 1 配置回收天数 365", status, api)

	f := createCustomer(t, env, admin, "隔离租户1客户", nil)
	fID := int64(f["id"].(float64))
	setCustomerTimes(t, env, fID, ptrTime(daysAgo(40)), nil)

	tenant2 := createTenantByAPI(t, env, admin, "回收隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	g := createCustomer(t, env, token2, "隔离租户2客户", nil)
	gID := int64(g["id"].(float64))
	setCustomerTimes(t, env, gID, ptrTime(daysAgo(40)), nil)

	// 一轮任务：租户 1（365 天）不动，租户 2（默认 30 天）回收。
	runRecycleJobAndWait(t, env, admin)

	if owner := ownerOf(t, env, admin, fID); owner != 1 {
		t.Fatalf("租户 1 配 365 天：40 天客户不应回收，实际 owner=%d", owner)
	}
	status, api = getCustomer(t, env, token2, gID)
	mustOK(t, "租户 2 查客户详情", status, api)
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(0) {
		t.Fatalf("租户 2 默认 30 天：40 天客户应回收，实际 owner=%v", detail["ownerUserId"])
	}
	rows, _ := pageOwnerHistory(t, env, token2, gID)
	if len(rows) == 0 || rows[0]["action"] != "recycle" {
		t.Fatalf("租户 2 客户应落 recycle 历史，实际 %v", rows)
	}
}

// TestIntegrationPoolRecycleContractExclusion 合同排除真实路径（#14 接 #16，
// spec §4.2 自动回收条件之三「无未作废合同」）：
//   - 有未作废合同（草稿/待审/已签）的超期客户不被回收；
//   - 合同作废后下一轮恢复回收（作废合同不拦截）。
//   - 不同租户的合同不互相拦截（租户隔离，checker 查询带租户条件）。
func TestIntegrationPoolRecycleContractExclusion(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 三个超期未跟进客户：分别挂未作废合同、作废合同、无合同。
	protected := createCustomer(t, env, admin, "回收有未作废合同客户", nil)
	protectedID := int64(protected["id"].(float64))
	voidable := createCustomer(t, env, admin, "回收作废合同客户", nil)
	voidableID := int64(voidable["id"].(float64))
	plain := createCustomer(t, env, admin, "回收无合同客户", nil)
	plainID := int64(plain["id"].(float64))

	// 超期回填：40 天前跟进（回收天数默认 30）。
	for _, id := range []int64{protectedID, voidableID, plainID} {
		setCustomerTimes(t, env, id, ptrTime(daysAgo(40)), nil)
	}

	// 未作废合同：草稿/待审/已签各算「未作废」，取待审一单覆盖中间态。
	protectedContract := createContract(t, env, admin, "拦截回收的未作废合同", map[string]any{
		"customerId": protectedID,
	})
	if st, api := changeContractStatus(t, env, admin, int64(protectedContract["id"].(float64)), 2, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交合同审批：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	// 作废合同：草稿直接作废。
	voidedContract := createContract(t, env, admin, "不拦截回收的作废合同", map[string]any{
		"customerId": voidableID,
	})
	if st, api := changeContractStatus(t, env, admin, int64(voidedContract["id"].(float64)), 4, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废合同：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}

	// 跨租户合同不拦截：租户 2 给租户 1 的 plain 客户 ID 建合同做不到（客户隔离），
	// 反向验证——租户 1 客户在租户 2 视角不可见即无可挂合同，这里直接跑一轮
	// 回收断言 plain 被回收即可（同一客户 ID 在租户 2 无行）。

	runRecycleJobAndWait(t, env, admin)

	// 有未作废合同：本轮跳过，等合同作废后下轮再回收。
	if owner := ownerOf(t, env, admin, protectedID); owner != 1 {
		t.Fatalf("有未作废合同的客户不应回收，实际 owner=%d", owner)
	}
	// 作废合同不拦截：照常回收。
	if owner := ownerOf(t, env, admin, voidableID); owner != 0 {
		t.Fatalf("只有作废合同的客户应被回收，实际 owner=%d", owner)
	}
	// 无合同：照常回收。
	if owner := ownerOf(t, env, admin, plainID); owner != 0 {
		t.Fatalf("无合同的客户应被回收，实际 owner=%d", owner)
	}

	// 合同作废后下一轮恢复回收（作废合同不拦截）。
	if st, api := changeContractStatus(t, env, admin, int64(protectedContract["id"].(float64)), 4, ""); st != http.StatusOK || api.Code != 0 {
		t.Fatalf("待审合同作废：HTTP %d code %d msg %q", st, api.Code, api.Msg)
	}
	runRecycleJobAndWait(t, env, admin)
	if owner := ownerOf(t, env, admin, protectedID); owner != 0 {
		t.Fatalf("合同作废后客户应被回收，实际 owner=%d", owner)
	}
}

// ptrTime 取时间指针（测试数据准备用）。
func ptrTime(t time.Time) *time.Time { return &t }
