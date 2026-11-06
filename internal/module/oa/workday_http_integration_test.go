//go:build integration

// 工作日历的 HTTP 集成用例（issue #50，docs/oa-mvp-spec.md §3.3/§4.8/§4.10）：
// 按月查看（默认规则 + 显式记录合成）、单日/批量维护、恢复默认、唯一约束、
// 租户隔离与权限码。主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t50，
// -p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---- 测试基建 ----

// mustNormalizeDate 解析日期字面量（YYYY-MM-DD），失败直接 Fatal。
func mustNormalizeDate(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("测试日期字面量非法 %q: %v", s, err)
	}
	return parsed
}

// getWorkdayMonth 按月查询，返回整月逐日数组（data.days）。
func getWorkdayMonth(t *testing.T, env *testutil.IntegrationEnv, token, month string) (int, []map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/workday/month?month="+month, token, nil)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var data struct {
		Month string           `json:"month"`
		Days  []map[string]any `json:"days"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析月视图响应: %v（data=%s）", err, string(api.Data))
	}
	return status, data.Days, api.Msg
}

// dayOf 从月视图行里取指定日期的行（不存在返回 nil）。
func dayOf(t *testing.T, days []map[string]any, date string) map[string]any {
	t.Helper()
	for _, day := range days {
		if day["date"] == date {
			return day
		}
	}
	return nil
}

// assertDay 断言某日的类型/显式标记/备注（wantExplicit 为 nil 时不校验）。
func assertDay(t *testing.T, days []map[string]any, date string, wantType int8, wantExplicit bool, wantRemark string) {
	t.Helper()
	day := dayOf(t, days, date)
	if day == nil {
		t.Fatalf("月视图缺少日期 %s", date)
	}
	if got := int8(day["type"].(float64)); got != wantType {
		t.Fatalf("%s 类型 %d，期望 %d", date, got, wantType)
	}
	if got := day["explicit"].(bool); got != wantExplicit {
		t.Fatalf("%s explicit %v，期望 %v", date, got, wantExplicit)
	}
	if wantRemark != "" {
		if got, _ := day["remark"].(string); got != wantRemark {
			t.Fatalf("%s 备注 %q，期望 %q", date, got, wantRemark)
		}
	}
}

// setWorkday 单日设置，返回 (HTTP 状态, msg)。
func setWorkday(t *testing.T, env *testutil.IntegrationEnv, token, date string, typ int8, remark string) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/oa/workday", token, map[string]any{
		"date": date, "type": typ, "remark": remark,
	})
	return status, api.Msg
}

// batchWorkday 批量设置，返回 (HTTP 状态, msg, 受影响天数)。
func batchWorkday(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, string, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/workday/batch", token, body)
	if api.Code != 0 {
		return status, api.Msg, 0
	}
	var data struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析批量设置响应: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, data.Count
}

// clearWorkday 清除显式记录（恢复默认），返回 (HTTP 状态, msg)。
func clearWorkday(t *testing.T, env *testutil.IntegrationEnv, token, date string) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodDelete, "/oa/workday/"+date, token, nil)
	return status, api.Msg
}

// TestIntegrationWorkdayMonthDefaultRules 无显式记录时整月走默认规则
// （spec §3.3：周一至周五工作日、周六周日休息）；响应覆盖整月每一天。
func TestIntegrationWorkdayMonthDefaultRules(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, days, msg := getWorkdayMonth(t, env, admin, "2026-11")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("按月查询：HTTP %d msg %q", status, msg)
	}
	// 2026-11 共 30 天。
	if len(days) != 30 {
		t.Fatalf("2026-11 应返回 30 天，实际 %d", len(days))
	}
	// 周末（11-07/08/14/15/21/22/28/29）默认休息日，其余默认工作日。
	for _, date := range []string{"2026-11-07", "2026-11-08", "2026-11-14", "2026-11-15", "2026-11-21", "2026-11-22", "2026-11-28", "2026-11-29"} {
		assertDay(t, days, date, 1, false, "")
	}
	for _, date := range []string{"2026-11-02", "2026-11-05", "2026-11-13", "2026-11-30"} {
		assertDay(t, days, date, 0, false, "")
	}
	// 请求非整月边界（12 月 31 天、2 月 28 天）。
	if _, days, _ = getWorkdayMonth(t, env, admin, "2026-12"); len(days) != 31 {
		t.Fatalf("2026-12 应返回 31 天，实际 %d", len(days))
	}
	if _, days, _ = getWorkdayMonth(t, env, admin, "2027-02"); len(days) != 28 {
		t.Fatalf("2027-02 应返回 28 天，实际 %d", len(days))
	}
}

// TestIntegrationWorkdayMonthBadRequest 非法月份格式 400。
func TestIntegrationWorkdayMonthBadRequest(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	for _, bad := range []string{"2026-1", "20261", "abc", "2026-13", ""} {
		status, api := env.Request(t, http.MethodGet, "/oa/workday/month?month="+bad, admin, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("月份 %q 应 400，实际 HTTP %d（msg=%q）", bad, status, api.Msg)
		}
	}
}

// TestIntegrationWorkdaySetSingleDay 单日设置生效：周末标记工作日（调休上班）、
// 工作日标记节假日；非法类型/日期 400；重复设置走 upsert 不产生重复记录。
func TestIntegrationWorkdaySetSingleDay(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 周六 11-07 标记工作日（调休上班）。
	if status, msg := setWorkday(t, env, admin, "2026-11-07", 0, "国庆调休上班"); status != http.StatusOK {
		t.Fatalf("周六标记工作日：HTTP %d msg %q", status, msg)
	}
	// 周四 11-05 标记节假日。
	if status, msg := setWorkday(t, env, admin, "2026-11-05", 2, "元旦"); status != http.StatusOK {
		t.Fatalf("工作日标记节假日：HTTP %d msg %q", status, msg)
	}

	_, days, _ := getWorkdayMonth(t, env, admin, "2026-11")
	assertDay(t, days, "2026-11-07", 0, true, "国庆调休上班")
	assertDay(t, days, "2026-11-05", 2, true, "元旦")
	// 未触碰的日期仍走默认规则。
	assertDay(t, days, "2026-11-06", 0, false, "")
	assertDay(t, days, "2026-11-08", 1, false, "")

	// 非法类型 400。
	if status, msg := setWorkday(t, env, admin, "2026-11-03", 3, ""); status != http.StatusBadRequest || msg == "" {
		t.Fatalf("类型 3 应 400，实际 HTTP %d msg %q", status, msg)
	}
	if status, _ := setWorkday(t, env, admin, "2026-11-03", -1, ""); status != http.StatusBadRequest {
		t.Fatalf("类型 -1 应 400，实际 HTTP %d", status)
	}
	// 非法日期 400。
	for _, bad := range []string{"2026-11-7", "20261107", "abc", ""} {
		status, _ := setWorkday(t, env, admin, bad, 2, "")
		if status != http.StatusBadRequest {
			t.Fatalf("日期 %q 应 400，实际 HTTP %d", bad, status)
		}
	}
	// 备注超长 400。
	longRemark := ""
	for i := 0; i < 21; i++ {
		longRemark += "0123456789"
	}
	if status, _ := setWorkday(t, env, admin, "2026-11-03", 2, longRemark); status != http.StatusBadRequest {
		t.Fatalf("备注 %d 字应 400，实际 HTTP %d", len([]rune(longRemark)), status)
	}

	// 重复设置同日：upsert 覆盖而非新增（月视图仍单行、类型更新）。
	if status, _ := setWorkday(t, env, admin, "2026-11-07", 1, "改为休息"); status != http.StatusOK {
		t.Fatalf("重复设置：HTTP %d", status)
	}
	_, days, _ = getWorkdayMonth(t, env, admin, "2026-11")
	assertDay(t, days, "2026-11-07", 1, true, "改为休息")
}

// TestIntegrationWorkdayBatch 批量设置：显式日期列表、区间 + 星期过滤
// （整年周末调整）、区间全量；参数非法 400。
func TestIntegrationWorkdayBatch(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	t.Run("显式日期列表", func(t *testing.T) {
		status, msg, count := batchWorkday(t, env, admin, map[string]any{
			"dates": []string{"2026-11-07", "2026-11-08"}, "type": 2, "remark": "临时假期",
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("批量设置：HTTP %d msg %q", status, msg)
		}
		if count != 2 {
			t.Fatalf("应影响 2 天，实际 %d", count)
		}
		_, days, _ := getWorkdayMonth(t, env, admin, "2026-11")
		assertDay(t, days, "2026-11-07", 2, true, "临时假期")
		assertDay(t, days, "2026-11-08", 2, true, "临时假期")
	})

	t.Run("区间加星期过滤=整年周末调整", func(t *testing.T) {
		// 2026 整年周六周日（weekdays 0/6）标记工作日（单休制调整）。
		status, msg, count := batchWorkday(t, env, admin, map[string]any{
			"startDate": "2026-01-01", "endDate": "2026-12-31",
			"weekdays": []int{0, 6}, "type": 0, "remark": "大小周调整",
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("整年周末调整：HTTP %d msg %q", status, msg)
		}
		// 2026-01-01 是周四：整年周六 53 天（01-03 起）+ 周日 52 天 = 104 天
		//（日历独立核对，非实现推导）。
		if count != 104 {
			t.Fatalf("2026 年周末应 104 天，实际 %d", count)
		}
		// 抽查两端与中段：周末变工作日，工作日不受影响。
		_, jan, _ := getWorkdayMonth(t, env, admin, "2026-01")
		assertDay(t, jan, "2026-01-03", 0, true, "大小周调整") // 周六
		assertDay(t, jan, "2026-01-04", 0, true, "大小周调整") // 周日
		assertDay(t, jan, "2026-01-05", 0, false, "")     // 周一未触碰
		_, dec, _ := getWorkdayMonth(t, env, admin, "2026-12")
		assertDay(t, dec, "2026-12-26", 0, true, "大小周调整") // 周六
		assertDay(t, dec, "2026-12-27", 0, true, "大小周调整") // 周日
	})

	t.Run("区间不加星期过滤", func(t *testing.T) {
		// 春节假期：2027-02-08 … 2027-02-12 全量节假日。
		status, _, count := batchWorkday(t, env, admin, map[string]any{
			"startDate": "2027-02-08", "endDate": "2027-02-12", "type": 2, "remark": "春节",
		})
		if status != http.StatusOK {
			t.Fatalf("春节批量：HTTP %d", status)
		}
		if count != 5 {
			t.Fatalf("应影响 5 天，实际 %d", count)
		}
		_, feb, _ := getWorkdayMonth(t, env, admin, "2027-02")
		assertDay(t, feb, "2027-02-08", 2, true, "春节")
		assertDay(t, feb, "2027-02-12", 2, true, "春节")
	})

	t.Run("非法参数400", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
		}{
			{"无日期来源", map[string]any{"type": 0}},
			{"空日期列表", map[string]any{"dates": []string{}, "type": 0}},
			{"日期列表格式非法", map[string]any{"dates": []string{"2026-11-7"}, "type": 0}},
			{"区间起止颠倒", map[string]any{"startDate": "2026-11-09", "endDate": "2026-11-02", "type": 0}},
			{"区间超长", map[string]any{"startDate": "2026-01-01", "endDate": "2028-12-31", "type": 0}},
			{"星期值越界", map[string]any{"startDate": "2026-11-02", "endDate": "2026-11-08", "weekdays": []int{7}, "type": 0}},
			{"类型非法", map[string]any{"dates": []string{"2026-11-07"}, "type": 9}},
			{"日期数量超限", func() map[string]any {
				dates := make([]string, 0, 401)
				for i := 1; i <= 401; i++ {
					dates = append(dates, fmt.Sprintf("2027-01-%02d", ((i-1)%28)+1))
				}
				return map[string]any{"dates": dates, "type": 0}
			}()},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, msg, _ := batchWorkday(t, env, admin, tc.body)
				if status != http.StatusBadRequest || msg == "" {
					t.Fatalf("%s 应 400，实际 HTTP %d msg %q", tc.name, status, msg)
				}
			})
		}
	})

	t.Run("同批重复日期去重", func(t *testing.T) {
		status, _, count := batchWorkday(t, env, admin, map[string]any{
			"dates": []string{"2026-11-23", "2026-11-23", "2026-11-24"}, "type": 2, "remark": "去重",
		})
		if status != http.StatusOK {
			t.Fatalf("同批重复日期：HTTP %d", status)
		}
		if count != 2 {
			t.Fatalf("去重后应影响 2 天，实际 %d", count)
		}
	})
}

// TestIntegrationWorkdayClear 清除显式记录恢复默认：清除后回默认规则；
// 清除不存在的记录 404。
func TestIntegrationWorkdayClear(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 设置周六 11-07 为工作日，再清除 → 回默认休息日。
	if status, _ := setWorkday(t, env, admin, "2026-11-07", 0, "调休"); status != http.StatusOK {
		t.Fatalf("设置：HTTP %d", status)
	}
	if status, _ := clearWorkday(t, env, admin, "2026-11-07"); status != http.StatusOK {
		t.Fatalf("清除：HTTP %d", status)
	}
	_, days, _ := getWorkdayMonth(t, env, admin, "2026-11")
	assertDay(t, days, "2026-11-07", 1, false, "")

	// 清除无显式记录的日期 → 404。
	if status, msg := clearWorkday(t, env, admin, "2026-11-07"); status != http.StatusNotFound || msg == "" {
		t.Fatalf("清除不存在记录应 404，实际 HTTP %d msg %q", status, msg)
	}
	// 非法日期格式 400。
	if status, _ := clearWorkday(t, env, admin, "2026-11-7"); status != http.StatusBadRequest {
		t.Fatalf("非法日期应 400，实际 HTTP %d", status)
	}

	// 清除后可重新设置（软删行不占用唯一键语义，upsert 复活）。
	if status, _ := setWorkday(t, env, admin, "2026-11-07", 2, "再次设置"); status != http.StatusOK {
		t.Fatalf("清除后重设：HTTP %d", status)
	}
	_, days, _ = getWorkdayMonth(t, env, admin, "2026-11")
	assertDay(t, days, "2026-11-07", 2, true, "再次设置")
}

// TestIntegrationWorkdayUniqueConstraint 唯一约束 (tenant_id, date) 生效：
// 应用层 upsert 不产生重复行；绕过应用直插重复行必须被数据库拒绝。
func TestIntegrationWorkdayUniqueConstraint(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	if status, _ := setWorkday(t, env, admin, "2026-11-07", 0, "第一次"); status != http.StatusOK {
		t.Fatalf("第一次设置：HTTP %d", status)
	}
	// 绕过应用直插同租户同日期 → 数据库唯一键拒绝（1062）。
	err := env.DB.Exec(
		"INSERT INTO `oa_workday_calendar` (`tenant_id`, `date`, `type`) VALUES (1, '2026-11-07', 2)",
	).Error
	if err == nil {
		t.Fatal("直插重复 (tenant_id, date) 应被唯一键拒绝")
	}

	// 跨租户不冲突：租户 2 可以拥有同日期记录。
	tenant2 := createOATenant(t, env, admin, "workday_unique_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	if status, _ := setWorkday(t, env, token2, "2026-11-07", 2, "租户2节假日"); status != http.StatusOK {
		t.Fatalf("租户 2 同日期设置：HTTP %d", status)
	}
}

// TestIntegrationWorkdayTenantIsolation 租户隔离：显式记录互不可见互不影响
// （spec §4.6：跨租户不泄露存在性）。
func TestIntegrationWorkdayTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 租户 1：周四 11-05 节假日、周六 11-07 调休上班。
	if status, _ := setWorkday(t, env, admin, "2026-11-05", 2, "租户1节假日"); status != http.StatusOK {
		t.Fatalf("租户 1 设置：HTTP %d", status)
	}
	if status, _ := setWorkday(t, env, admin, "2026-11-07", 0, "租户1调休"); status != http.StatusOK {
		t.Fatalf("租户 1 设置：HTTP %d", status)
	}

	tenant2 := createOATenant(t, env, admin, "workday_isolate_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)

	// 租户 2 同月视图：全部默认规则，看不到租户 1 的记录。
	_, days2, _ := getWorkdayMonth(t, env, token2, "2026-11")
	assertDay(t, days2, "2026-11-05", 0, false, "")
	assertDay(t, days2, "2026-11-07", 1, false, "")

	// 租户 2 设置同日期不同类型互不冲突、互不覆盖。
	if status, _ := setWorkday(t, env, token2, "2026-11-05", 1, "租户2休息"); status != http.StatusOK {
		t.Fatalf("租户 2 设置：HTTP %d", status)
	}
	_, days1, _ := getWorkdayMonth(t, env, admin, "2026-11")
	assertDay(t, days1, "2026-11-05", 2, true, "租户1节假日")

	// 租户 2 清除租户 1 独有的记录 → 404（不泄露存在性）。
	if status, msg := clearWorkday(t, env, token2, "2026-11-07"); status != http.StatusNotFound || msg == "" {
		t.Fatalf("跨租户清除应 404，实际 HTTP %d msg %q", status, msg)
	}
}

// ---- 租户基建（与 bpm 集成用例同款）----

// createOATenant 平台管理员创建租户并按名称回读 ID（新租户内置 admin/123456）。
func createOATenant(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?name="+name+"&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询租户 %s：HTTP %d code %d", name, status, api.Code)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("租户 %s 应唯一命中: %v（data=%s）", name, err, string(api.Data))
	}
	return page.List[0].ID
}

// loginOATenant 以指定租户登录（登录契约带 tenantId）。
func loginOATenant(t *testing.T, env *testutil.IntegrationEnv, username, password string, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": username, "password": password, "tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 %d 登录 %s：HTTP %d code %d msg %q", tenantID, username, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// TestIntegrationWorkdayPermissionCodes 权限码逐码控权（spec §4.6）：
// oa:workday:query（菜单 520）可看月视图；set/batch/delete（5201–5203）逐码控写；
// 无码 403。
func TestIntegrationWorkdayPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 菜单/按钮 ID：520 工作日历（查询码在菜单上），5201–5203 单日/批量/恢复默认。
	const (
		menuWorkday = int64(520)
		btnSet      = int64(5201)
		btnBatch    = int64(5202)
		btnDelete   = int64(5203)
	)
	readerRole := createRoleWithMenus(t, env, admin, "workday_reader", []int64{menuWorkday})
	managerRole := createRoleWithMenus(t, env, admin, "workday_manager",
		[]int64{menuWorkday, btnSet, btnBatch, btnDelete})
	noneRole := createRoleWithMenus(t, env, admin, "workday_none", []int64{1}) // 仪表盘

	reader := oaLoginNewUser(t, env, admin, "workday_reader_u", readerRole)
	manager := oaLoginNewUser(t, env, admin, "workday_manager_u", managerRole)
	noPerm := oaLoginNewUser(t, env, admin, "workday_none_u", noneRole)

	// 无任何 workday 码：月视图 403。
	status, api := env.Request(t, http.MethodGet, "/oa/workday/month?month=2026-11", noPerm, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无码用户月视图应 403，实际 HTTP %d code %d", status, api.Code)
	}

	// 只读：月视图可读；写路径全 403。
	if status, days, _ := getWorkdayMonth(t, env, reader, "2026-11"); status != http.StatusOK || len(days) != 30 {
		t.Fatalf("只读用户月视图：HTTP %d 天数 %d", status, len(days))
	}
	if status, _ := setWorkday(t, env, reader, "2026-11-07", 0, ""); status != http.StatusForbidden {
		t.Fatalf("只读用户单日设置应 403，实际 HTTP %d", status)
	}
	if status, _, _ := batchWorkday(t, env, reader, map[string]any{
		"dates": []string{"2026-11-07"}, "type": 0,
	}); status != http.StatusForbidden {
		t.Fatalf("只读用户批量设置应 403，实际 HTTP %d", status)
	}
	if status, _ := clearWorkday(t, env, reader, "2026-11-07"); status != http.StatusForbidden {
		t.Fatalf("只读用户恢复默认应 403，实际 HTTP %d", status)
	}

	// 全量角色：单日设置 → 批量设置 → 清除 全链可用。
	if status, _ := setWorkday(t, env, manager, "2026-11-07", 0, "调休"); status != http.StatusOK {
		t.Fatalf("manager 单日设置：HTTP %d", status)
	}
	if status, _, count := batchWorkday(t, env, manager, map[string]any{
		"dates": []string{"2026-11-05", "2026-11-06"}, "type": 2, "remark": "假期",
	}); status != http.StatusOK || count != 2 {
		t.Fatalf("manager 批量设置：HTTP %d count %d", status, count)
	}
	if status, _ := clearWorkday(t, env, manager, "2026-11-07"); status != http.StatusOK {
		t.Fatalf("manager 恢复默认：HTTP %d", status)
	}
}

// oaLoginNewUser 创建挂指定角色的用户并登录，返回 token。
func oaLoginNewUser(t *testing.T, env *testutil.IntegrationEnv, admin, username string, roleID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "nickname": username, "password": "123456",
		"deptId": 101, "status": 0, "roleIds": []int64{roleID},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	return env.Login(t, username, "123456")
}
