//go:build integration

// mall 积分域 HTTP 集成测试（issue #175，docs/mall-mvp-spec.md §3.1/§3.4/§4/§6）：
//   - 管理端调整全链路：正入/负扣/备注/流水快照（balance_after 与逐笔重放一致）；
//   - 唯一入账入口并发：20 goroutine 同会员不丢不重（终态余额 = 流水重放）；
//   - 非负禁令：余额扣成负数 409（SQL 双保险裁决，无负余额落库）；
//   - 流水分页（id 正序重放序）+ biz_type 过滤；
//   - 越权矩阵：跨租户调整/查流水 404（租户插件隔离，不泄露存在性）；
//   - 积分配置：GET 惰性建默认行（point_per_yuan=1）、PUT 校验 ≥0、租户隔离。
//
// 401 探测网（internal/testutil protected_probe_test.go）经 prefixes 列表
// append /mall 自动覆盖新端点的组归属；按钮权限码 mall:point:* 的授予链路由
// seed（9013/9014）与权限收敛测试覆盖。
package mall_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mallAdjustPoint 发起积分调整，返回 HTTP 状态与响应信封（断言由调用方做）。
func mallAdjustPoint(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64, point int, remark string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/mall/member/adjust-point", token, map[string]any{
		"userId": userID, "point": point, "remark": remark,
	})
}

// mallPointBalance 读会员当前积分余额（GET /mall/member/:id 的 pointBalance）。
func mallPointBalance(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64) int {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, detailPath(userID), token, nil)
	mustOK(t, "读会员详情", status, api)
	data := parseJSON(t, string(api.Data))
	balance, ok := data["pointBalance"].(float64)
	if !ok {
		t.Fatalf("会员详情缺少 pointBalance：%s", api.Data)
	}
	return int(balance)
}

// mallAllPointRecords 翻页取全部积分流水（后端固定 id ASC 重放序）。
func mallAllPointRecords(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64) []map[string]any {
	t.Helper()
	var out []map[string]any
	for page := 1; ; page++ {
		status, api := env.Request(t, http.MethodGet,
			"/mall/member/"+itoa(userID)+"/point-records?pageNo="+itoa(int64(page))+"&pageSize=100", token, nil)
		mustOK(t, "积分流水分页", status, api)
		list := mallPageList(t, api)
		out = append(out, list...)
		if len(list) < 100 {
			return out
		}
	}
}

// assertPointReplay 积分不变量断言（spec §3.4）：按流水 id 正序逐笔累计
// point，每一笔的 balance_after 必须等于累计值（快照链一致），最终代数和
// 必须等于会员 point_balance（余额可由流水重放验证）。
func assertPointReplay(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64, wantBalance int) {
	t.Helper()
	records := mallAllPointRecords(t, env, token, userID)
	running := 0
	for i, rec := range records {
		point, _ := rec["point"].(float64)
		after, _ := rec["balanceAfter"].(float64)
		running += int(point)
		if running != int(after) {
			t.Fatalf("流水第 %d 笔快照不一致：累计 %d ≠ balanceAfter %d（point=%v）", i+1, running, int(after), point)
		}
		if running < 0 {
			t.Fatalf("流水第 %d 笔重放出现负余额 %d（非负禁令被破坏）", i+1, running)
		}
	}
	if running != wantBalance {
		t.Fatalf("流水重放代数和 %d ≠ 会员余额 %d（不丢不重被破坏）", running, wantBalance)
	}
	if got := mallPointBalance(t, env, token, userID); got != wantBalance {
		t.Fatalf("会员余额 %d ≠ 期望 %d", got, wantBalance)
	}
}

// TestIntegrationPointAdjustAndReplay 调整全链路：+100 → -30 → 余额 70；
// 流水 biz_type=admin_adjust、biz_id=0、快照链可重放；point=0 与缺 userId
// 400；未知会员 404；备注超长 400。
func TestIntegrationPointAdjustAndReplay(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	member := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000501", "nickname": "积分甲"})
	id := int64(member["id"].(float64))

	// +100：入账流水带快照 100。
	status, api := mallAdjustPoint(t, env, token, id, 100, "活动奖励")
	mustOK(t, "入账 +100", status, api)
	first := parseJSON(t, string(api.Data))
	if first["bizType"] != "admin_adjust" || int(first["balanceAfter"].(float64)) != 100 || int(first["point"].(float64)) != 100 {
		t.Fatalf("入账流水字段不符：%s", api.Data)
	}
	if v, _ := first["bizId"].(float64); int64(v) != 0 {
		t.Fatalf("admin_adjust 的 biz_id 应恒 0：%s", api.Data)
	}
	if first["remark"] != "活动奖励" {
		t.Fatalf("流水备注未落库：%s", api.Data)
	}

	// -30：扣减流水带快照 70。
	status, api = mallAdjustPoint(t, env, token, id, -30, "兑换扣减")
	mustOK(t, "扣减 -30", status, api)
	second := parseJSON(t, string(api.Data))
	if int(second["balanceAfter"].(float64)) != 70 || int(second["point"].(float64)) != -30 {
		t.Fatalf("扣减流水字段不符：%s", api.Data)
	}

	// 不变量：快照链 + 重放 = 余额。
	assertPointReplay(t, env, token, id, 70)

	// 会员分页可见余额（列表列数据源）。
	status, api = env.Request(t, http.MethodGet, "/mall/member/page?mobile=13800000501&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "会员分页", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || int(list[0]["pointBalance"].(float64)) != 70 {
		t.Fatalf("会员分页积分列应 70：%v", list)
	}

	// 0 拒绝（400）。
	status, api = mallAdjustPoint(t, env, token, id, 0, "")
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("point=0：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 缺 userId（400）。
	status, api = env.Request(t, http.MethodPost, "/mall/member/adjust-point", token, map[string]any{"point": 5})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("缺 userId：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 未知会员 404。
	status, api = mallAdjustPoint(t, env, token, 999999, 5, "")
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("未知会员调整：HTTP %d code %d，期望 404", status, api.Code)
	}
	// 备注超长（>500 字）400。
	long := strings.Repeat("好", 501)
	status, api = mallAdjustPoint(t, env, token, id, 1, long)
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("备注超长：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 非法 biz_type 过滤 400。
	status, api = env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?bizType=hack&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("非法 biz_type 过滤：HTTP %d code %d，期望 400", status, api.Code)
	}
}

// TestIntegrationPointConcurrent20 并发 20 goroutine 同会员（issue #175 验收项）：
//   - 第一轮 20×(+10)：全部成功，终态 200，流水恰 20 条，重放一致（不丢不重）；
//   - 第二轮余额 10 时 20×(-1)：恰 10 成 10 拒（409），终态 0 无负余额，
//     流水快照链仍一致。
func TestIntegrationPointConcurrent20(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	member := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000601", "nickname": "积分乙"})
	id := int64(member["id"].(float64))

	// 第一轮：20 × +10 并发入账。
	const goroutines = 20
	statuses := make([]int, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, _ := mallAdjustPoint(t, env, token, id, 10, "并发入账")
			statuses[i] = st
		}(i)
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("并发入账第 %d 笔 HTTP %d，期望 200（唯一入账入口应串行化成功）", i, st)
		}
	}
	assertPointReplay(t, env, token, id, goroutines*10)
	records := mallAllPointRecords(t, env, token, id)
	if len(records) != goroutines {
		t.Fatalf("并发 20 笔应恰 20 条流水（不丢不重），实际 %d 条", len(records))
	}
	// DB 侧直查自证（绕过 API 分页）：行数恰 20。
	if n := memberPointRecordCountFromDB(t, env, id); n != goroutines {
		t.Fatalf("DB 流水行数应 20（不丢不重 DB 自证），实际 %d", n)
	}

	// 第二轮：余额 10，20 × -1 并发扣减——非负 SQL 双保险裁决，恰 10 成 10 拒。
	status, api := mallAdjustPoint(t, env, token, id, -190, "压到 10")
	mustOK(t, "压余额到 10", status, api)
	drain := make([]int, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, _ := mallAdjustPoint(t, env, token, id, -1, "并发扣减")
			drain[i] = st
		}(i)
	}
	wg.Wait()
	okCount, conflictCount := 0, 0
	for i, st := range drain {
		switch st {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("并发扣减第 %d 笔 HTTP %d（期望 200/409）", i, st)
		}
	}
	if okCount != 10 || conflictCount != 10 {
		t.Fatalf("并发扣减应 10 成 10 拒（409），实际 ok=%d conflict=%d", okCount, conflictCount)
	}
	// 终态 0：无负余额落库，快照链仍一致（重放恒 ≥0）。
	assertPointReplay(t, env, token, id, 0)
}

// TestIntegrationPointRecordsPagination 流水分页（issue #175 验收项）：
// 25 笔调整，pageSize=10 三页 10/10/5；跨页重放一致；biz_type 过滤命中与
// 排除双向成立。
func TestIntegrationPointRecordsPagination(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	member := mallCreateMember(t, env, token, map[string]any{"mobile": "13800000701", "nickname": "积分丙"})
	id := int64(member["id"].(float64))

	const total = 25
	for i := 0; i < total; i++ {
		status, api := mallAdjustPoint(t, env, token, id, 1, "第N笔")
		mustOK(t, "第笔入账", status, api)
	}

	// 三页 10/10/5，total=25。
	status, api := env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?pageNo=1&pageSize=10", token, nil)
	mustOK(t, "流水第 1 页", status, api)
	page := parseJSON(t, string(api.Data))
	if int(page["total"].(float64)) != total {
		t.Fatalf("流水 total 应 25：%s", api.Data)
	}
	if list := page["list"].([]any); len(list) != 10 {
		t.Fatalf("第 1 页应 10 条，实际 %d", len(list))
	}
	status, api = env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?pageNo=3&pageSize=10", token, nil)
	mustOK(t, "流水第 3 页", status, api)
	if list := parseJSON(t, string(api.Data))["list"].([]any); len(list) != 5 {
		t.Fatalf("第 3 页应 5 条，实际 %d", len(list))
	}

	// 跨页全量重放一致（id 正序）。
	assertPointReplay(t, env, token, id, total)

	// biz_type 过滤：admin_adjust 全命中；order_pay 无流水（T4 接线前）。
	status, api = env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?bizType=admin_adjust&pageNo=1&pageSize=100", token, nil)
	mustOK(t, "biz_type=admin_adjust 过滤", status, api)
	if int(parseJSON(t, string(api.Data))["total"].(float64)) != total {
		t.Fatalf("admin_adjust 过滤应 25 条：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?bizType=order_pay&pageNo=1&pageSize=100", token, nil)
	mustOK(t, "biz_type=order_pay 过滤", status, api)
	if int(parseJSON(t, string(api.Data))["total"].(float64)) != 0 {
		t.Fatalf("order_pay 过滤应 0 条（T4 未接线）：%s", api.Data)
	}
	// 未知会员流水 404。
	status, api = env.Request(t, http.MethodGet, "/mall/member/999999/point-records?pageNo=1&pageSize=10", token, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("未知会员流水：HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationPointCrossTenant 越权矩阵（issue #175 验收项）：租户 2
// 管理员调整/查租户 1 会员积分一律 404（跨租户业务 ID 不可见，不泄露存在性）；
// 各租户余额/流水互不可见、互不串账。
func TestIntegrationPointCrossTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	member := mallCreateMember(t, env, adminToken, map[string]any{"mobile": "13800000801", "nickname": "租户甲"})
	id := int64(member["id"].(float64))
	status, api := mallAdjustPoint(t, env, adminToken, id, 50, "租户甲入账")
	mustOK(t, "租户 1 入账", status, api)

	tenant2 := createTenantByAPI(t, env, adminToken, "mall-point-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 调整租户 1 会员 → 404。
	status, api = mallAdjustPoint(t, env, token2, id, -50, "越权扣减")
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户调整：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	// 租户 2 查租户 1 会员流水 → 404。
	status, api = env.Request(t, http.MethodGet, "/mall/member/"+itoa(id)+"/point-records?pageNo=1&pageSize=10", token2, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户流水：HTTP %d code %d，期望 404", status, api.Code)
	}
	// 越权未得手：租户 1 余额原封不动、流水仍 1 条。
	assertPointReplay(t, env, adminToken, id, 50)

	// 租户 2 自己的会员独立记账，不串租户 1 的数据。
	member2 := mallCreateMember(t, env, token2, map[string]any{"mobile": "13800000801", "nickname": "租户乙"})
	id2 := int64(member2["id"].(float64))
	status, api = mallAdjustPoint(t, env, token2, id2, 8, "租户乙入账")
	mustOK(t, "租户 2 入账", status, api)
	assertPointReplay(t, env, token2, id2, 8)
	assertPointReplay(t, env, adminToken, id, 50)
}

// TestIntegrationPointConfig 积分配置（issue #175，spec §3.1 租户级单行）：
// GET 行缺失惰性建默认行（point_per_yuan=1）；PUT 更新 + ≥0 校验（负数 400、
// 0 合法 = 支付不入账）；租户间配置互不影响（UNIQUE(tenant_id) 单行）。
func TestIntegrationPointConfig(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	// 首读惰性建行：默认 1。
	status, api := env.Request(t, http.MethodGet, "/mall/point-config", adminToken, nil)
	mustOK(t, "读积分配置", status, api)
	cfg := parseJSON(t, string(api.Data))
	if int(cfg["pointPerYuan"].(float64)) != 1 {
		t.Fatalf("缺省配置应 pointPerYuan=1：%s", api.Data)
	}

	// 更新为 5，回读生效。
	status, api = env.Request(t, http.MethodPut, "/mall/point-config", adminToken, map[string]any{
		"pointPerYuan": 5, "remark": "促销翻倍",
	})
	mustOK(t, "更新积分配置", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/point-config", adminToken, nil)
	mustOK(t, "回读积分配置", status, api)
	if got := int(parseJSON(t, string(api.Data))["pointPerYuan"].(float64)); got != 5 {
		t.Fatalf("更新后应 pointPerYuan=5，实际 %d", got)
	}

	// 负数 400（≥0 校验）；0 合法（0 = 支付不入账）。
	status, api = env.Request(t, http.MethodPut, "/mall/point-config", adminToken, map[string]any{"pointPerYuan": -1})
	if status != http.StatusBadRequest || api.Code != 400 {
		t.Fatalf("pointPerYuan=-1：HTTP %d code %d，期望 400", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/point-config", adminToken, map[string]any{"pointPerYuan": 0})
	mustOK(t, "pointPerYuan=0 合法", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/point-config", adminToken, nil)
	mustOK(t, "回读积分配置（0）", status, api)
	if got := int(parseJSON(t, string(api.Data))["pointPerYuan"].(float64)); got != 0 {
		t.Fatalf("更新后应 pointPerYuan=0，实际 %d", got)
	}

	// 租户隔离：租户 2 首读仍是默认 1（UNIQUE(tenant_id) 各租户单行）。
	tenant2 := createTenantByAPI(t, env, adminToken, "mall-point-config-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	status, api = env.Request(t, http.MethodGet, "/mall/point-config", token2, nil)
	mustOK(t, "租户 2 读积分配置", status, api)
	if got := int(parseJSON(t, string(api.Data))["pointPerYuan"].(float64)); got != 1 {
		t.Fatalf("租户 2 缺省配置应独立 pointPerYuan=1，实际 %d", got)
	}
}

// memberPointRecordCountFromDB 直查流水行数（DB 侧自证，绕过 API 分页）。
func memberPointRecordCountFromDB(t *testing.T, env *testutil.IntegrationEnv, userID int64) int64 {
	t.Helper()
	var count int64
	if err := tenant.Skip(env.DB).Raw(
		"SELECT COUNT(*) FROM member_point_record WHERE user_id = ?", userID,
	).Scan(&count).Error; err != nil {
		t.Fatalf("直查积分流水数: %v", err)
	}
	return count
}
