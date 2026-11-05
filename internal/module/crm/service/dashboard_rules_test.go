package service_test

import (
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 仪表盘纯函数测试（issue #19，docs/crm-mvp-spec.md §4.1/§6/§8）：
// 近 12 个月序列补齐、逾期判定（到期日严格早于今天且已收 < 计划额）、
// 商机阶段漏斗与「进行中金额」口径、月度合同/回款趋势归月。
// 全部按应用时区（ADR 0006）解释日期，不连数据库。

// dashboardTestZone 是固定的东八区，等价于默认 app.timezone=Asia/Shanghai，
// 但用 FixedZone 避免单测依赖运行环境的 tzdata。
var dashboardTestZone = time.FixedZone("CST", 8*3600)

// TestDashboardMonths 近 12 个月序列：含本月、升序、跨年回退、空/单月边界。
func TestDashboardMonths(t *testing.T) {
	now := time.Date(2026, 11, 5, 15, 30, 0, 0, dashboardTestZone)

	months := service.DashboardMonths(now, 12)
	if len(months) != 12 {
		t.Fatalf("近 12 个月应返回 12 项，实际 %d", len(months))
	}
	if got := months[0].Key(); got != "2025-12" {
		t.Fatalf("首月应为 2025-12，实际 %s", got)
	}
	if got := months[11].Key(); got != "2026-11" {
		t.Fatalf("末月应为当前月 2026-11，实际 %s", got)
	}
	for i := 1; i < len(months); i++ {
		prev := time.Date(months[i-1].Year, months[i-1].Month, 1, 0, 0, 0, 0, dashboardTestZone)
		cur := time.Date(months[i].Year, months[i].Month, 1, 0, 0, 0, 0, dashboardTestZone)
		if !cur.After(prev) {
			t.Fatalf("月份应升序：%s 之后是 %s", months[i-1].Key(), months[i].Key())
		}
	}

	if got := service.DashboardMonths(now, 1); len(got) != 1 || got[0].Key() != "2026-11" {
		t.Fatalf("count=1 应只返回当前月，实际 %+v", got)
	}
	if got := service.DashboardMonths(now, 0); len(got) != 0 {
		t.Fatalf("count=0 应返回空序列，实际 %+v", got)
	}

	// 跨年回退：2026-01 往前 12 个月是 2025-02..2026-01。
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, dashboardTestZone)
	months = service.DashboardMonths(jan, 12)
	if got := months[0].Key(); got != "2025-02" {
		t.Fatalf("2026-01 往前 12 个月首月应为 2025-02，实际 %s", got)
	}
	if got := months[11].Key(); got != "2026-01" {
		t.Fatalf("2026-01 往前 12 个月末月应为 2026-01，实际 %s", got)
	}

	// ADR 0006：同一物理时刻在不同时区可能属于不同月份，调用方必须先转换
	// 到应用时区；DashboardMonths 使用入参自身的 Location。
	utcNow := time.Date(2026, 10, 31, 16, 30, 0, 0, time.UTC) // 东八区已是 11-01 00:30
	if got := service.DashboardMonths(utcNow, 1)[0].Key(); got != "2026-10" {
		t.Fatalf("未转换时区的时刻应归 2026-10，实际 %s", got)
	}
	if got := service.DashboardMonths(utcNow.In(dashboardTestZone), 1)[0].Key(); got != "2026-11" {
		t.Fatalf("转换到应用时区后应归 2026-11，实际 %s", got)
	}
}

// TestSummarizeOverduePlans 逾期判定与未收金额口径：
// 到期日严格早于今天、且已收 < 计划额才计逾期；未收 = 计划额 - 已收。
func TestSummarizeOverduePlans(t *testing.T) {
	today := time.Date(2026, 11, 5, 15, 30, 0, 0, dashboardTestZone)
	yesterday := time.Date(2026, 11, 4, 0, 0, 0, 0, dashboardTestZone)
	todayMidnight := time.Date(2026, 11, 5, 0, 0, 0, 0, dashboardTestZone)
	tomorrow := time.Date(2026, 11, 6, 0, 0, 0, 0, dashboardTestZone)

	plans := []service.DashboardDuePlan{
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "0.00"), DueDate: yesterday},
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "400.00"), DueDate: yesterday},
		// 已收齐/超额：不再算逾期（钱已收到）。
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "1000.00"), DueDate: yesterday},
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "1200.00"), DueDate: yesterday},
		// 到期日当天不算逾期（严格早于今天）。
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "0.00"), DueDate: todayMidnight},
		{Amount: mustMoney(t, "1000.00"), ReceivedAmount: mustMoney(t, "0.00"), DueDate: tomorrow},
		// 零金额计划视为已收齐。
		{Amount: mustMoney(t, "0.00"), ReceivedAmount: mustMoney(t, "0.00"), DueDate: yesterday},
	}
	count, amount := service.SummarizeOverduePlans(plans, today, dashboardTestZone)
	if count != 2 {
		t.Fatalf("逾期数量应为 2，实际 %d", count)
	}
	if got := amount.String(); got != "1600.00" {
		t.Fatalf("逾期未收金额应为 1600.00，实际 %s", got)
	}

	count, amount = service.SummarizeOverduePlans(nil, today, dashboardTestZone)
	if count != 0 || amount.String() != "0.00" {
		t.Fatalf("无计划应为 0 期 / 0.00，实际 %d / %s", count, amount.String())
	}

	// 到期日以 UTC 表示但落在东八区的昨天：按应用时区日历日判定仍逾期。
	utcYesterday := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC) // 东八区 11-04 00:00
	count, amount = service.SummarizeOverduePlans([]service.DashboardDuePlan{
		{Amount: mustMoney(t, "500.00"), ReceivedAmount: mustMoney(t, "100.00"), DueDate: utcYesterday},
	}, today, dashboardTestZone)
	if count != 1 || amount.String() != "400.00" {
		t.Fatalf("UTC 到期日应按应用时区归日：实际 %d / %s", count, amount.String())
	}
}

// TestBuildOpportunityFunnel 漏斗固定 6 档（1–6）补零，阶段名与聚合金额正确；
// 进行中金额只计 stage 1–4（赢单/输单是终态，不算进行中）。
func TestBuildOpportunityFunnel(t *testing.T) {
	rows := []service.DashboardStageTotal{
		{Stage: 5, Count: 1, Amount: mustMoney(t, "9000.00")},
		{Stage: 1, Count: 2, Amount: mustMoney(t, "1500.00")},
		{Stage: 3, Count: 1, Amount: mustMoney(t, "500.50")},
		{Stage: 6, Count: 1, Amount: mustMoney(t, "100.00")},
		// 非法阶段不应出现在漏斗里（仓储查询只取 1–6，纯函数防御性忽略）。
		{Stage: 9, Count: 7, Amount: mustMoney(t, "1.00")},
	}

	funnel := service.BuildOpportunityFunnel(rows)
	if len(funnel) != 6 {
		t.Fatalf("漏斗应固定 6 档，实际 %d", len(funnel))
	}
	wantNames := []string{"初步", "需求", "报价", "谈判", "赢单", "输单"}
	for i, item := range funnel {
		if item.Stage != int8(i+1) {
			t.Fatalf("第 %d 项阶段应为 %d，实际 %d", i, i+1, item.Stage)
		}
		if item.StageName != wantNames[i] {
			t.Fatalf("阶段 %d 名称应为 %s，实际 %s", item.Stage, wantNames[i], item.StageName)
		}
	}
	if funnel[0].Count != 2 || funnel[0].Amount.String() != "1500.00" {
		t.Fatalf("初步阶段应为 2 个 / 1500.00，实际 %d / %s", funnel[0].Count, funnel[0].Amount.String())
	}
	if funnel[1].Count != 0 || funnel[1].Amount.String() != "0.00" {
		t.Fatalf("需求阶段应补零，实际 %d / %s", funnel[1].Count, funnel[1].Amount.String())
	}
	if funnel[2].Amount.String() != "500.50" {
		t.Fatalf("报价阶段金额应为 500.50，实际 %s", funnel[2].Amount.String())
	}
	if funnel[3].Count != 0 {
		t.Fatalf("谈判阶段应补零，实际 %d", funnel[3].Count)
	}
	if funnel[4].Amount.String() != "9000.00" || funnel[5].Amount.String() != "100.00" {
		t.Fatalf("赢单/输单金额不符：%s / %s", funnel[4].Amount.String(), funnel[5].Amount.String())
	}

	if got := service.SumInProgressOpportunityAmount(funnel).String(); got != "2000.50" {
		t.Fatalf("进行中商机金额应为 1500.00 + 500.50 = 2000.50，实际 %s", got)
	}

	if got := service.BuildOpportunityFunnel(nil); len(got) != 6 {
		t.Fatalf("无数据也应返回 6 档零值，实际 %d", len(got))
	}
}

// TestBuildDashboardTrend 月度趋势按应用时区归月、空月补 0、窗口外数据丢弃。
func TestBuildDashboardTrend(t *testing.T) {
	now := time.Date(2026, 11, 5, 15, 30, 0, 0, dashboardTestZone)
	months := service.DashboardMonths(now, 12) // 2025-12 .. 2026-11

	contracts := []service.DashboardDatedAmount{
		{Date: time.Date(2026, 1, 15, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "1000.00")},
		{Date: time.Date(2026, 1, 31, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "0.50")},
		{Date: time.Date(2026, 11, 1, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "300.00")},
		// 窗口外（13 个月前）不计入任何月份。
		{Date: time.Date(2025, 10, 1, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "999.00")},
	}
	receipts := []service.DashboardDatedAmount{
		{Date: time.Date(2026, 1, 10, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "100.00")},
		{Date: time.Date(2026, 11, 5, 0, 0, 0, 0, dashboardTestZone), Amount: mustMoney(t, "50.25")},
		// 物理时刻 2026-01-31 17:00 UTC = 东八区 2026-02-01 01:00，应归 2026-02。
		{Date: time.Date(2026, 1, 31, 17, 0, 0, 0, time.UTC), Amount: mustMoney(t, "7.00")},
	}

	trend := service.BuildDashboardTrend(months, contracts, receipts, dashboardTestZone)
	if len(trend) != 12 {
		t.Fatalf("趋势应补齐 12 个月，实际 %d", len(trend))
	}
	for i, point := range trend {
		if want := months[i].Key(); point.Month != want {
			t.Fatalf("第 %d 个月份应为 %s，实际 %s", i, want, point.Month)
		}
	}

	cases := []struct {
		month          string
		contractAmount string
		receiptAmount  string
	}{
		{month: "2025-12", contractAmount: "0.00", receiptAmount: "0.00"},
		{month: "2026-01", contractAmount: "1000.50", receiptAmount: "100.00"},
		{month: "2026-02", contractAmount: "0.00", receiptAmount: "7.00"},
		{month: "2026-03", contractAmount: "0.00", receiptAmount: "0.00"},
		{month: "2026-11", contractAmount: "300.00", receiptAmount: "50.25"},
	}
	byMonth := make(map[string]service.DashboardTrendPoint, len(trend))
	for _, point := range trend {
		byMonth[point.Month] = point
	}
	for _, tc := range cases {
		point, ok := byMonth[tc.month]
		if !ok {
			t.Fatalf("缺少月份 %s", tc.month)
		}
		if point.ContractAmount.String() != tc.contractAmount {
			t.Fatalf("%s 合同金额应为 %s，实际 %s", tc.month, tc.contractAmount, point.ContractAmount.String())
		}
		if point.ReceiptAmount.String() != tc.receiptAmount {
			t.Fatalf("%s 回款金额应为 %s，实际 %s", tc.month, tc.receiptAmount, point.ReceiptAmount.String())
		}
	}
}
