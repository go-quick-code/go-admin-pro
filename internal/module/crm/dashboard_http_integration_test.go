//go:build integration

// 仪表盘 HTTP 集成测试（issue #19）：真实 router + 生产中间件（JWT/权限/租户插件）
// + 真实 MySQL，在 HTTP 契约上验证 4 张卡片、商机阶段漏斗、近 12 个月
// 合同/回款趋势的数值口径，以及数据范围矩阵、租户隔离与权限码 403。
// 金额一律十进制字符串（internal/pkg/money），月份按应用时区（ADR 0006）归月。
package crm_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/money"
	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmDashboardMenuID 是种子中的 CRM 仪表盘菜单 ID（权限码 crm:dashboard:view）。
const crmDashboardMenuID = int64(4140)

// moneyStringCents 把响应里的十进制金额字符串解析为分（拒绝非字符串，
// 保证 JSON 契约始终是字符串而非数字字面量）。
func moneyStringCents(t *testing.T, value any) int64 {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("金额应为十进制字符串，实际 %T（%v）", value, value)
	}
	parsed, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("金额字符串 %q 解析失败: %v", text, err)
	}
	return parsed.Cents()
}

// fetchDashboardSummary 调用仪表盘接口并返回解析后的 data。
func fetchDashboardSummary(t *testing.T, env *testutil.IntegrationEnv, token string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/dashboard/summary", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询仪表盘汇总：HTTP %d code %d msg %q，期望 HTTP 200 code 0", status, api.Code, api.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析仪表盘响应: %v（data=%s）", err, string(api.Data))
	}
	return data
}

// dashboardCards 取响应中的 cards 对象。
func dashboardCards(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	cards, ok := data["cards"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 cards 对象：%v", data)
	}
	return cards
}

// dashboardFunnelByStage 把漏斗数组按阶段索引，并断言固定 6 档。
func dashboardFunnelByStage(t *testing.T, data map[string]any) map[int8]map[string]any {
	t.Helper()
	raw, ok := data["funnel"].([]any)
	if !ok {
		t.Fatalf("响应缺少 funnel 数组：%v", data)
	}
	if len(raw) != 6 {
		t.Fatalf("漏斗应固定 6 档，实际 %d", len(raw))
	}
	byStage := make(map[int8]map[string]any, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("漏斗元素类型不符：%v", item)
		}
		stage := int8(row["stage"].(float64))
		byStage[stage] = row
	}
	return byStage
}

// dashboardTrendByMonth 把趋势数组按月份索引，并断言补齐 12 个月。
func dashboardTrendByMonth(t *testing.T, data map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := data["trend"].([]any)
	if !ok {
		t.Fatalf("响应缺少 trend 数组：%v", data)
	}
	if len(raw) != 12 {
		t.Fatalf("趋势应补齐 12 个月，实际 %d", len(raw))
	}
	byMonth := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("趋势元素类型不符：%v", item)
		}
		byMonth[row["month"].(string)] = row
	}
	return byMonth
}

// monthDay 返回相对当前月偏移 offset 个月的某个日历日（yyyy-MM-dd，
// 用测试进程的应用时区计算，与服务端 app.timezone 一致）。
func monthDay(now time.Time, offset, day int) string {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, offset, 0)
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
}

// createSignedContract 创建合同并走完 草稿 → 待审 → 已签，返回创建响应。
func createSignedContract(t *testing.T, env *testutil.IntegrationEnv, token, name string, payload map[string]any) map[string]any {
	t.Helper()
	contract := createContract(t, env, token, name, payload)
	id := int64(contract["id"].(float64))
	if status, api := changeContractStatus(t, env, token, id, 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交合同 %s 审批：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	if status, api := changeContractStatus(t, env, token, id, 3, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审批通过合同 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	return contract
}

// createOpportunityAtStage 创建商机并推进到目标阶段（5/6 只能经阶段端点进入）。
func createOpportunityAtStage(t *testing.T, env *testutil.IntegrationEnv, token, name string, customerID int64, stage int8, amount string) map[string]any {
	t.Helper()
	payload := map[string]any{"customerId": customerID, "expectedAmount": amount}
	if stage <= 4 {
		// 1–4 可在创建时直接指定；5/6 是终态，只能经阶段端点推进。
		payload["stage"] = stage
	}
	opportunity := createOpportunity(t, env, token, name, payload)
	if stage > 4 {
		id := int64(opportunity["id"].(float64))
		if status, api := advanceOpportunityStage(t, env, token, id, stage); status != http.StatusOK || api.Code != 0 {
			t.Fatalf("推进商机 %s 到阶段 %d：HTTP %d code %d msg %q", name, stage, status, api.Code, api.Msg)
		}
	}
	return opportunity
}

// TestIntegrationDashboardSummary 卡片 / 漏斗 / 趋势的数值口径：
//   - 客户总数、进行中商机金额（stage 1–4）、已签合同金额（status=3，含窗口外）、
//     逾期回款（到期日严格早于今天且已收 < 计划额，数量 + 未收金额）；
//   - 漏斗固定 1–6 档，空档补 0，金额用 expected_amount；
//   - 近 12 个月趋势含本月、空月补 0，合同按 sign_date、回款按 receipt_date 归月；
//     草稿/待审合同与窗口外的签订日期不计入。
func TestIntegrationDashboardSummary(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	now := time.Now().In(env.Config.Location())
	currentMonth := now.Format("2006-01")
	previousMonth := now.AddDate(0, -1, 0).Format("2006-01")
	twoMonthsAgo := now.AddDate(0, -2, 0).Format("2006-01")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// 3 个可见客户（admin 全部数据范围）。
	customerA := createCustomer(t, env, admin, "仪表盘客户甲", nil)
	createCustomer(t, env, admin, "仪表盘客户乙", nil)
	createCustomer(t, env, admin, "仪表盘客户丙", nil)
	customerID := int64(customerA["id"].(float64))

	// 商机：stage 1–6 各档（stage 1 两个），金额合计用于漏斗与进行中卡片。
	createOpportunityAtStage(t, env, admin, "仪表盘商机-初步一", customerID, 1, "1000.00")
	createOpportunityAtStage(t, env, admin, "仪表盘商机-初步二", customerID, 1, "500.00")
	createOpportunityAtStage(t, env, admin, "仪表盘商机-需求", customerID, 2, "2000.50")
	createOpportunityAtStage(t, env, admin, "仪表盘商机-报价", customerID, 3, "3000.00")
	createOpportunityAtStage(t, env, admin, "仪表盘商机-谈判", customerID, 4, "4000.00")
	stage5 := createOpportunity(t, env, admin, "仪表盘商机-赢单", map[string]any{"customerId": customerID, "expectedAmount": "5000.00"})
	stage6 := createOpportunity(t, env, admin, "仪表盘商机-输单", map[string]any{"customerId": customerID, "expectedAmount": "6000.00"})
	for _, opp := range []struct {
		name string
		row  map[string]any
	}{{"赢单", stage5}, {"输单", stage6}} {
		id := int64(opp.row["id"].(float64))
		target := int8(5)
		if opp.name == "输单" {
			target = 6
		}
		if status, api := advanceOpportunityStage(t, env, admin, id, target); status != http.StatusOK || api.Code != 0 {
			t.Fatalf("推进商机到阶段 %d：HTTP %d code %d msg %q", target, status, api.Code, api.Msg)
		}
	}

	// 已签合同：本月 7000、上月 2500、两月前 1500、13 个月前 1234（窗口外）。
	contractThisMonth := createSignedContract(t, env, admin, "仪表盘本月合同", map[string]any{
		"customerId": customerID, "amount": "7000.00", "signDate": monthDay(now, 0, 1),
	})
	contractLastMonth := createSignedContract(t, env, admin, "仪表盘上月合同", map[string]any{
		"customerId": customerID, "amount": "2500.00", "signDate": monthDay(now, -1, 15),
	})
	contractTwoMonthsAgo := createSignedContract(t, env, admin, "仪表盘两月前合同", map[string]any{
		"customerId": customerID, "amount": "1500.00", "signDate": monthDay(now, -2, 10),
	})
	createSignedContract(t, env, admin, "仪表盘窗口外合同", map[string]any{
		"customerId": customerID, "amount": "1234.00", "signDate": monthDay(now, -13, 10),
	})
	// 草稿与待审都不计入「已签合同金额」与趋势。
	createContract(t, env, admin, "仪表盘草稿合同", map[string]any{
		"customerId": customerID, "amount": "9999.00", "signDate": monthDay(now, 0, 1),
	})
	pending := createContract(t, env, admin, "仪表盘待审合同", map[string]any{
		"customerId": customerID, "amount": "8888.00", "signDate": monthDay(now, 0, 1),
	})
	if status, api := changeContractStatus(t, env, admin, int64(pending["id"].(float64)), 2, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交待审合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 回款计划：P1 部分回款（逾期）、P2 已收齐（不逾期）、P3 未收（逾期）、
	// P4 到期日当天（边界，不逾期）。
	plan1 := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": int64(contractThisMonth["id"].(float64)), "periodNo": 1,
		"amount": "7000.00", "dueDate": yesterday,
	})
	createReceivableRecord(t, env, admin, map[string]any{
		"planId": int64(plan1["id"].(float64)), "amount": "2000.00",
		"receiptDate": now.Format("2006-01-02"),
	})
	plan2 := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": int64(contractLastMonth["id"].(float64)), "periodNo": 1,
		"amount": "2500.00", "dueDate": yesterday,
	})
	createReceivableRecord(t, env, admin, map[string]any{
		"planId": int64(plan2["id"].(float64)), "amount": "2500.00",
		"receiptDate": monthDay(now, -1, 20),
	})
	contractTwoID := int64(contractTwoMonthsAgo["id"].(float64))
	createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractTwoID, "periodNo": 1, "amount": "1000.00", "dueDate": yesterday,
	})
	createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractTwoID, "periodNo": 2, "amount": "500.00",
		"dueDate": now.Format("2006-01-02"),
	})

	data := fetchDashboardSummary(t, env, admin)

	cards := dashboardCards(t, data)
	wantCards := map[string]any{
		"customerCount":               float64(3),
		"inProgressOpportunityAmount": "10500.50", // 1000 + 500 + 2000.50 + 3000 + 4000
		"signedContractAmount":        "12234.00", // 7000 + 2500 + 1500 + 1234（含窗口外）
		"overdueReceivableCount":      float64(2), // P1（部分回款）+ P3（未收）
		"overdueReceivableAmount":     "6000.00",  // P1 未收 5000 + P3 未收 1000
	}
	for key, want := range wantCards {
		if got := cards[key]; got != want {
			t.Fatalf("卡片 %s = %v，期望 %v", key, got, want)
		}
	}

	funnel := dashboardFunnelByStage(t, data)
	wantFunnel := []struct {
		stage  int8
		name   string
		count  float64
		amount string
	}{
		{1, "初步", 2, "1500.00"},
		{2, "需求", 1, "2000.50"},
		{3, "报价", 1, "3000.00"},
		{4, "谈判", 1, "4000.00"},
		{5, "赢单", 1, "5000.00"},
		{6, "输单", 1, "6000.00"},
	}
	for _, want := range wantFunnel {
		row, ok := funnel[want.stage]
		if !ok {
			t.Fatalf("漏斗缺少阶段 %d", want.stage)
		}
		if row["stageName"] != want.name || row["count"] != want.count || row["amount"] != want.amount {
			t.Fatalf("漏斗阶段 %d = %v/%v/%v，期望 %s/%v/%s",
				want.stage, row["stageName"], row["count"], row["amount"], want.name, want.count, want.amount)
		}
	}

	trend := dashboardTrendByMonth(t, data)
	if _, ok := trend[currentMonth]; !ok {
		t.Fatalf("趋势缺少当前月 %s：%v", currentMonth, trend)
	}
	wantTrend := map[string][2]string{
		currentMonth:  {"7000.00", "2000.00"},
		previousMonth: {"2500.00", "2500.00"},
		twoMonthsAgo:  {"1500.00", "0.00"},
	}
	for month, want := range wantTrend {
		row := trend[month]
		if row["contractAmount"] != want[0] || row["receiptAmount"] != want[1] {
			t.Fatalf("%s 趋势 = %v/%v，期望 %s/%s",
				month, row["contractAmount"], row["receiptAmount"], want[0], want[1])
		}
	}
	// 窗口外（13 个月前）的已签合同不出现在趋势里：合同金额合计 11000。
	var contractTotal, receiptTotal int64
	for _, row := range trend {
		contractTotal += moneyStringCents(t, row["contractAmount"])
		receiptTotal += moneyStringCents(t, row["receiptAmount"])
	}
	if contractTotal != 1100000 || receiptTotal != 450000 {
		t.Fatalf("趋势合计应为合同 11000.00 / 回款 4500.00，实际 %d / %d 分", contractTotal, receiptTotal)
	}
	// 空月补 0：三个月前没有任何合同与回款。
	emptyMonth := now.AddDate(0, -3, 0).Format("2006-01")
	if row := trend[emptyMonth]; row["contractAmount"] != "0.00" || row["receiptAmount"] != "0.00" {
		t.Fatalf("空月 %s 应补 0.00/0.00，实际 %v/%v", emptyMonth, row["contractAmount"], row["receiptAmount"])
	}
}

// seedDashboardOwnerData 为一个负责人造一组同口径数据（客户 + 进行中商机 +
// 本月已签合同 + 一期逾期计划与部分回款），供数据范围矩阵按视角比较数值。
func seedDashboardOwnerData(t *testing.T, env *testutil.IntegrationEnv, admin string, ownerID int64, ownerName string) {
	t.Helper()
	now := time.Now().In(env.Config.Location())
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	customer := createCustomer(t, env, admin, "仪表盘范围客户_"+ownerName, map[string]any{"ownerUserId": ownerID})
	customerID := int64(customer["id"].(float64))
	createOpportunity(t, env, admin, "仪表盘范围商机_"+ownerName, map[string]any{
		"customerId": customerID, "ownerUserId": ownerID, "stage": 1, "expectedAmount": "100.00",
	})
	contract := createSignedContract(t, env, admin, "仪表盘范围合同_"+ownerName, map[string]any{
		"customerId": customerID, "ownerUserId": ownerID, "amount": "1000.00",
		"signDate": monthDay(now, 0, 1),
	})
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": int64(contract["id"].(float64)), "periodNo": 1,
		"amount": "1000.00", "dueDate": yesterday,
	})
	createReceivableRecord(t, env, admin, map[string]any{
		"planId": int64(plan["id"].(float64)), "amount": "400.00",
		"receiptDate": now.Format("2006-01-02"),
	})
}

// setupDashboardScopeEnv 搭建数据范围观察场景（与合同/回款矩阵同一部门布局）：
// 4 个销售在 101/102/市场二组名下各持一组数据；观察者在 102 持本人一组数据。
func setupDashboardScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64) (observer string) {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	createUserByAPI(t, env, admin, "dashboard_owner_rnd", rndDept, nil)
	createUserByAPI(t, env, admin, "dashboard_owner_mkt", mktDept, nil)
	createUserByAPI(t, env, admin, "dashboard_owner_mkt2", mktDept, nil)
	subDept := createDeptByAPI(t, env, admin, "仪表盘二组", mktDept)
	createUserByAPI(t, env, admin, "dashboard_owner_sub", subDept, nil)

	for _, username := range []string{
		"dashboard_owner_rnd", "dashboard_owner_mkt", "dashboard_owner_mkt2", "dashboard_owner_sub",
	} {
		seedDashboardOwnerData(t, env, admin, findUserIDByUsername(t, env, admin, username), username)
	}

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仪表盘观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": []int64{crmDashboardMenuID, 4141},
	})
	observer = "dashboard_observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	seedDashboardOwnerData(t, env, admin, findUserIDByUsername(t, env, admin, observer), observer)
	return observer
}

// TestIntegrationDashboardDataScopeMatrix 每个指标都随 5 种数据范围变化：
// 客户按 owner_*、商机/合同按自身 owner_*、回款 join 合同套合同 owner 谓词；
// 范围外数据不计入任何卡片、漏斗或趋势。
func TestIntegrationDashboardDataScopeMatrix(t *testing.T) {
	cases := []struct {
		name              string
		roleCode          string
		dataScope         int
		customIDs         []int64
		wantCustomers     float64
		wantInProgress    string
		wantSigned        string
		wantOverdueCount  float64
		wantOverdueAmount string
		wantFunnelCount   float64
		wantFunnelAmount  string
		wantContractTrend string
		wantReceiptTrend  string
	}{
		{
			name: "全部数据", roleCode: "db_all", dataScope: 1,
			wantCustomers: 5, wantInProgress: "500.00", wantSigned: "5000.00",
			wantOverdueCount: 5, wantOverdueAmount: "3000.00",
			wantFunnelCount: 5, wantFunnelAmount: "500.00",
			wantContractTrend: "5000.00", wantReceiptTrend: "2000.00",
		},
		{
			// 101 + 102 的并集：命中研发、市场两人与市场部观察者共 4 组数据；
			// 二组（102 的子部门）精确集合不含，若实现误扩子部门会多出第 5 组。
			name: "自定义部门_精确集合不含子部门", roleCode: "db_custom", dataScope: 2, customIDs: []int64{101, 102},
			wantCustomers: 4, wantInProgress: "400.00", wantSigned: "4000.00",
			wantOverdueCount: 4, wantOverdueAmount: "2400.00",
			wantFunnelCount: 4, wantFunnelAmount: "400.00",
			wantContractTrend: "4000.00", wantReceiptTrend: "1600.00",
		},
		{
			name: "本部门", roleCode: "db_dept", dataScope: 3,
			wantCustomers: 3, wantInProgress: "300.00", wantSigned: "3000.00",
			wantOverdueCount: 3, wantOverdueAmount: "1800.00",
			wantFunnelCount: 3, wantFunnelAmount: "300.00",
			wantContractTrend: "3000.00", wantReceiptTrend: "1200.00",
		},
		{
			name: "本部门及以下", roleCode: "db_dept_child", dataScope: 4,
			wantCustomers: 4, wantInProgress: "400.00", wantSigned: "4000.00",
			wantOverdueCount: 4, wantOverdueAmount: "2400.00",
			wantFunnelCount: 4, wantFunnelAmount: "400.00",
			wantContractTrend: "4000.00", wantReceiptTrend: "1600.00",
		},
		{
			name: "仅本人", roleCode: "db_self", dataScope: 5,
			wantCustomers: 1, wantInProgress: "100.00", wantSigned: "1000.00",
			wantOverdueCount: 1, wantOverdueAmount: "600.00",
			wantFunnelCount: 1, wantFunnelAmount: "100.00",
			wantContractTrend: "1000.00", wantReceiptTrend: "400.00",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewIntegrationEnv(t)
			admin := env.Login(t, "admin", "123456")
			observer := setupDashboardScopeEnv(t, env, admin, tc.roleCode, tc.dataScope, tc.customIDs)
			token := env.Login(t, observer, "123456")
			now := time.Now().In(env.Config.Location())
			currentMonth := now.Format("2006-01")

			data := fetchDashboardSummary(t, env, token)
			cards := dashboardCards(t, data)
			wantCards := map[string]any{
				"customerCount":               tc.wantCustomers,
				"inProgressOpportunityAmount": tc.wantInProgress,
				"signedContractAmount":        tc.wantSigned,
				"overdueReceivableCount":      tc.wantOverdueCount,
				"overdueReceivableAmount":     tc.wantOverdueAmount,
			}
			for key, want := range wantCards {
				if got := cards[key]; got != want {
					t.Fatalf("%s 卡片 %s = %v，期望 %v", tc.name, key, got, want)
				}
			}

			funnel := dashboardFunnelByStage(t, data)
			if funnel[1]["count"] != tc.wantFunnelCount || funnel[1]["amount"] != tc.wantFunnelAmount {
				t.Fatalf("%s 漏斗初步阶段 = %v/%v，期望 %v/%s",
					tc.name, funnel[1]["count"], funnel[1]["amount"], tc.wantFunnelCount, tc.wantFunnelAmount)
			}

			trend := dashboardTrendByMonth(t, data)
			row := trend[currentMonth]
			if row["contractAmount"] != tc.wantContractTrend || row["receiptAmount"] != tc.wantReceiptTrend {
				t.Fatalf("%s 本月趋势 = %v/%v，期望 %s/%s",
					tc.name, row["contractAmount"], row["receiptAmount"], tc.wantContractTrend, tc.wantReceiptTrend)
			}
		})
	}
}

// TestIntegrationDashboardTenantIsolation 租户隔离：租户 2 看不到租户 1 的
// 客户/商机/合同/回款，所有指标为 0（漏斗与趋势仍返回固定结构）。
func TestIntegrationDashboardTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	seedDashboardOwnerData(t, env, admin, findUserIDByUsername(t, env, admin, "admin"), "admin")

	tenantData := fetchDashboardSummary(t, env, admin)
	if dashboardCards(t, tenantData)["customerCount"] != float64(1) {
		t.Fatalf("租户 1 客户数应为 1，实际 %v", dashboardCards(t, tenantData)["customerCount"])
	}

	tenant2 := createTenantByAPI(t, env, admin, "仪表盘隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	data2 := fetchDashboardSummary(t, env, token2)

	cards := dashboardCards(t, data2)
	wantCards := map[string]any{
		"customerCount":               float64(0),
		"inProgressOpportunityAmount": "0.00",
		"signedContractAmount":        "0.00",
		"overdueReceivableCount":      float64(0),
		"overdueReceivableAmount":     "0.00",
	}
	for key, want := range wantCards {
		if got := cards[key]; got != want {
			t.Fatalf("租户 2 卡片 %s = %v，期望 %v（租户隔离）", key, got, want)
		}
	}
	for stage, row := range dashboardFunnelByStage(t, data2) {
		if row["count"] != float64(0) || row["amount"] != "0.00" {
			t.Fatalf("租户 2 漏斗阶段 %d = %v/%v，期望 0/0.00", stage, row["count"], row["amount"])
		}
	}
	for month, row := range dashboardTrendByMonth(t, data2) {
		if row["contractAmount"] != "0.00" || row["receiptAmount"] != "0.00" {
			t.Fatalf("租户 2 趋势 %s = %v/%v，期望 0.00/0.00", month, row["contractAmount"], row["receiptAmount"])
		}
	}
}

// TestIntegrationDashboardPermissionDenied 无 crm:dashboard:view 一律 403：
// 观察者只绑客户页面菜单（401），未绑仪表盘菜单/按钮（4140/4141）。
func TestIntegrationDashboardPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色仪表盘", "code": "db_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "db_no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "db_no_perm_user", "123456")

	status, api := env.Request(t, http.MethodGet, "/crm/dashboard/summary", noPerm, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无 crm:dashboard:view 查询仪表盘：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodGet, "/crm/dashboard/summary", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询仪表盘：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationDashboardCustomerCountPoolVisibility 客户总数遵循公海可见性
// （spec §4.1）：持 crm:customer:pool:view 者（含 super_admin 通配）计入公海客户，
// 且不受数据范围过滤；无该权限者只统计有负责人的客户。
func TestIntegrationDashboardCustomerCountPoolVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 2 个有负责人客户 + 1 个释放到公海的客户。
	createCustomer(t, env, admin, "公海计数客户甲", nil)
	createCustomer(t, env, admin, "公海计数客户乙", nil)
	pooled := createCustomer(t, env, admin, "公海计数客户丙", nil)
	if status, api := releaseCustomerRaw(t, env, admin, int64(pooled["id"].(float64)), "仪表盘公海计数"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("释放客户到公海：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// admin（super_admin ⇒ "*"）可见全部客户，公海计入：3。
	if got := dashboardCards(t, fetchDashboardSummary(t, env, admin))["customerCount"]; got != float64(3) {
		t.Fatalf("admin 客户总数 = %v，期望 3（含公海）", got)
	}

	// 仪表盘权限但无公海权限：公海不计入，只剩 2 个有负责人客户。
	noPoolRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仪表盘无公海角色", "code": "db_no_pool", "dataScope": 1,
		"menuIds": []int64{crmDashboardMenuID, 4141},
	})
	createUserByAPI(t, env, admin, "db_no_pool_user", 102, []int64{noPoolRole})
	noPool := env.Login(t, "db_no_pool_user", "123456")
	if got := dashboardCards(t, fetchDashboardSummary(t, env, noPool))["customerCount"]; got != float64(2) {
		t.Fatalf("无 crm:customer:pool:view 用户客户总数 = %v，期望 2（不含公海）", got)
	}

	// 有公海权限但数据范围「仅本人」：公海不受数据范围过滤，仍计入；
	// 他人负责的 2 个客户不可见：1。
	poolRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仪表盘含公海角色", "code": "db_with_pool", "dataScope": 5,
		"menuIds": []int64{crmDashboardMenuID, 4141, poolCustomerMenuID},
	})
	createUserByAPI(t, env, admin, "db_with_pool_user", 102, []int64{poolRole})
	withPool := env.Login(t, "db_with_pool_user", "123456")
	if got := dashboardCards(t, fetchDashboardSummary(t, env, withPool))["customerCount"]; got != float64(1) {
		t.Fatalf("持公海权限的仅本人用户客户总数 = %v，期望 1（仅公海，不受数据范围过滤）", got)
	}
}
