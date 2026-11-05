package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// 回款计划/记录纯函数测试（issue #18，docs/crm-mvp-spec.md §3/§4.3）：
// HK 编号格式、派生状态判定优先级、金额不变量（计划 ≤ 合同、记录 ≤ 计划）、
// 期次校验。错误一律用户可读中文（400）。

func mustMoney(t *testing.T, text string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", text, err)
	}
	return m
}

func TestFormatReceivableRecordNo(t *testing.T) {
	cases := []struct {
		name    string
		dateKey string
		seq     int64
		want    string
	}{
		{name: "首号", dateKey: "20261105", seq: 1, want: "HK202611050001"},
		{name: "四位补零", dateKey: "20261105", seq: 42, want: "HK202611050042"},
		{name: "9999", dateKey: "20261105", seq: 9999, want: "HK202611059999"},
		{name: "超 9999 自然扩位不截断", dateKey: "20261105", seq: 10000, want: "HK2026110510000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.FormatReceivableRecordNo(tc.dateKey, tc.seq); got != tc.want {
				t.Fatalf("FormatReceivableRecordNo(%q, %d) = %q, want %q", tc.dateKey, tc.seq, got, tc.want)
			}
		})
	}
}

func TestDeriveReceivableStatus(t *testing.T) {
	// 基准「今天」与到期日都按应用时区（ADR 0006）解释；用例固定日期，不依赖运行时钟。
	today := time.Date(2026, 11, 5, 15, 30, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)
	todayMidnight := time.Date(2026, 11, 5, 0, 0, 0, 0, time.Local)

	cases := []struct {
		name       string
		plan       string
		received   string
		dueDate    time.Time
		wantStatus int8
		wantOver   bool
	}{
		{name: "未收款未到期为待回款", plan: "1000.00", received: "0.00", dueDate: tomorrow, wantStatus: model.ReceivableStatusPending},
		{name: "未收款已过期为逾期", plan: "1000.00", received: "0.00", dueDate: yesterday, wantStatus: model.ReceivableStatusOverdue, wantOver: true},
		{name: "部分收款未到期为部分回款", plan: "1000.00", received: "500.00", dueDate: tomorrow, wantStatus: model.ReceivableStatusPartial},
		{name: "部分收款已过期为逾期", plan: "1000.00", received: "500.00", dueDate: yesterday, wantStatus: model.ReceivableStatusOverdue, wantOver: true},
		{name: "收齐未到期为已回款", plan: "1000.00", received: "1000.00", dueDate: tomorrow, wantStatus: model.ReceivableStatusReceived},
		// 已回款优先于逾期：收齐后即便过了到期日也不再标逾期。
		{name: "收齐已过期为已回款", plan: "1000.00", received: "1000.00", dueDate: yesterday, wantStatus: model.ReceivableStatusReceived},
		{name: "超额收款为已回款", plan: "1000.00", received: "1200.00", dueDate: yesterday, wantStatus: model.ReceivableStatusReceived},
		{name: "零金额计划视为已回款", plan: "0.00", received: "0.00", dueDate: yesterday, wantStatus: model.ReceivableStatusReceived},
		// 到期日当天不算逾期（严格早于今天）。
		{name: "到期日当天未逾期", plan: "1000.00", received: "0.00", dueDate: todayMidnight, wantStatus: model.ReceivableStatusPending},
		{name: "到期日当天部分回款", plan: "1000.00", received: "0.01", dueDate: todayMidnight, wantStatus: model.ReceivableStatusPartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, overdue := service.DeriveReceivableStatus(
				mustMoney(t, tc.plan), mustMoney(t, tc.received), tc.dueDate, today)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if overdue != tc.wantOver {
				t.Fatalf("overdue = %v, want %v", overdue, tc.wantOver)
			}
			if name := service.ReceivableStatusNames[status]; name == "" {
				t.Fatalf("状态 %d 缺少中文文案", status)
			}
		})
	}
}

func TestValidateReceivablePlanAmount(t *testing.T) {
	contractAmount := mustMoney(t, "10000.00")

	if err := service.ValidateReceivablePlanAmount(mustMoney(t, "3000.00"), contractAmount, mustMoney(t, "7000.00")); err != nil {
		t.Fatalf("合计恰好等于合同金额应放行，实际 %v", err)
	}
	if err := service.ValidateReceivablePlanAmount(mustMoney(t, "0.00"), contractAmount, mustMoney(t, "0.00")); err != nil {
		t.Fatalf("零金额计划应放行（非负），实际 %v", err)
	}

	err := service.ValidateReceivablePlanAmount(mustMoney(t, "-0.01"), contractAmount, mustMoney(t, "0.00"))
	if err == nil || !strings.Contains(err.Error(), "不能为负") {
		t.Fatalf("负数计划金额应拒绝并说明不能为负，实际 %v", err)
	}

	err = service.ValidateReceivablePlanAmount(mustMoney(t, "0.01"), contractAmount, mustMoney(t, "10000.00"))
	if err == nil || !strings.Contains(err.Error(), "合同金额") {
		t.Fatalf("计划合计超合同金额应拒绝并点名合同金额，实际 %v", err)
	}
}

func TestValidateReceivableRecordAmount(t *testing.T) {
	planAmount := mustMoney(t, "1000.00")

	if err := service.ValidateReceivableRecordAmount(mustMoney(t, "400.00"), planAmount, mustMoney(t, "600.00")); err != nil {
		t.Fatalf("记录合计恰好等于计划金额应放行，实际 %v", err)
	}

	err := service.ValidateReceivableRecordAmount(mustMoney(t, "-1.00"), planAmount, mustMoney(t, "0.00"))
	if err == nil || !strings.Contains(err.Error(), "不能为负") {
		t.Fatalf("负数回款金额应拒绝并说明不能为负，实际 %v", err)
	}

	err = service.ValidateReceivableRecordAmount(mustMoney(t, "0.01"), planAmount, mustMoney(t, "1000.00"))
	if err == nil || !strings.Contains(err.Error(), "计划金额") {
		t.Fatalf("记录合计超计划金额应拒绝并点名计划金额，实际 %v", err)
	}
}

func TestValidateReceivablePeriodNo(t *testing.T) {
	for _, invalid := range []int{0, -1} {
		err := service.ValidateReceivablePeriodNo(invalid)
		if err == nil || !strings.Contains(err.Error(), "期次") {
			t.Fatalf("期次 %d 应拒绝并点名期次，实际 %v", invalid, err)
		}
	}
	if err := service.ValidateReceivablePeriodNo(1); err != nil {
		t.Fatalf("期次 1 应放行，实际 %v", err)
	}
}

func TestValidateReceivableStatusFilter(t *testing.T) {
	if err := service.ValidateReceivableStatusFilter(model.ReceivableStatusOverdue); err != nil {
		t.Fatalf("合法派生状态应放行，实际 %v", err)
	}
	err := service.ValidateReceivableStatusFilter(9)
	if err == nil || !strings.Contains(err.Error(), "回款状态") {
		t.Fatalf("非法派生状态应拒绝并点名回款状态，实际 %v", err)
	}
}
