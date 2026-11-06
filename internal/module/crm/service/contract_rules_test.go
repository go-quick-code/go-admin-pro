package service

import (
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 合同纯函数测试（issue #16，docs/crm-mvp-spec.md §4.4 状态机、§4.3 金额不变量）：
// 状态流转全矩阵（合法/非法）、审批权限需求、金额不变量（有/无行项目）、
// 行项目校验（与商机共享规则）、日期解析与起止校验、HT 编号格式。
// 错误一律用户可读中文（400）。

// moneyFrom 构造测试金额（十进制字符串，不经过 float64）。
func moneyFrom(t *testing.T, s string) money.Money {
	t.Helper()
	m, err := money.ParseMoney(s)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", s, err)
	}
	return m
}

// contractStatusMatrix 枚举全部状态对，标注合法性与期望文案片段（流转矩阵的事实来源）。
var contractStatusMatrix = []struct {
	from, to int8
	legal    bool
	wantMsg  string // 非法时的文案关键词
}{
	// 草稿：只能提交审批或作废。
	{from: 1, to: 2, legal: true},
	{from: 1, to: 4, legal: true},
	{from: 1, to: 1, wantMsg: "草稿"},
	{from: 1, to: 3, wantMsg: "草稿"},
	// 待审：只能由用户作废；审批通过/驳回由 bpm 回调回写，不在状态端点（issue #47）。
	{from: 2, to: 3, wantMsg: "待审"},
	{from: 2, to: 1, wantMsg: "待审"},
	{from: 2, to: 4, legal: true},
	{from: 2, to: 2, wantMsg: "待审"},
	// 已签：只能作废。
	{from: 3, to: 4, legal: true},
	{from: 3, to: 1, wantMsg: "已签"},
	{from: 3, to: 2, wantMsg: "已签"},
	{from: 3, to: 3, wantMsg: "已签"},
	// 作废为终态：不再有任何流转。
	{from: 4, to: 1, wantMsg: "作废"},
	{from: 4, to: 2, wantMsg: "作废"},
	{from: 4, to: 3, wantMsg: "作废"},
	{from: 4, to: 4, wantMsg: "作废"},
}

func TestCanTransitionContractStatus(t *testing.T) {
	for _, tc := range contractStatusMatrix {
		err := CanTransitionContractStatus(tc.from, tc.to)
		if tc.legal {
			if err != nil {
				t.Fatalf("%d→%d 应为合法流转，实际错误 %v", tc.from, tc.to, err)
			}
			continue
		}
		if !isBadRequest(err) {
			t.Fatalf("%d→%d 应为 400 非法流转，实际 %v", tc.from, tc.to, err)
		}
		if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
			t.Fatalf("%d→%d 文案应包含 %q，实际 %q", tc.from, tc.to, tc.wantMsg, err.Error())
		}
	}
	// 非法枚举（0、5、负数）一律拒绝，绝不静默放行。
	for _, s := range []int8{0, 5, -1} {
		if err := CanTransitionContractStatus(1, s); !isBadRequest(err) {
			t.Fatalf("目标状态 %d 非法：期望 400，实际 %v", s, err)
		}
		if err := CanTransitionContractStatus(s, 1); !isBadRequest(err) {
			t.Fatalf("来源状态 %d 非法：期望 400，实际 %v", s, err)
		}
	}
}

// TestContractApprovalSnapshot 锁定 bpm 实例快照规则（issue #47，spec §4.5）：
// 标题含合同号，摘要含客户名称与金额；超长按快照列宽截断。
func TestContractApprovalSnapshot(t *testing.T) {
	title, summary := contractApprovalSnapshot(
		"HT202601020001", "年度服务合同", "华西制造有限公司", moneyFrom(t, "128000.00"),
	)
	if title != "合同审批: HT202601020001" {
		t.Fatalf("标题快照错误：%q", title)
	}
	want := "客户：华西制造有限公司；合同：年度服务合同；金额：128000.00 元"
	if summary != want {
		t.Fatalf("摘要快照错误：%q，期望 %q", summary, want)
	}

	// 客户名缺失降级为 "-"（不阻塞提交，合同本身仍可读）。
	_, summary = contractApprovalSnapshot("HT1", "合同", "", moneyFrom(t, "0"))
	if !strings.Contains(summary, "客户：-") {
		t.Fatalf("空客户名应降级为 -：%q", summary)
	}

	// 摘要按 500 字符截断（不切坏多字节字符）。
	longCustomer := strings.Repeat("客", 600)
	_, summary = contractApprovalSnapshot("HT1", "合同", longCustomer, moneyFrom(t, "1"))
	if runes := []rune(summary); len(runes) != 500 {
		t.Fatalf("摘要应按 500 字符截断，实际 %d", len(runes))
	}
}

// TestTruncateContractRejectReason 驳回原因按 crm_contract.reject_reason
// varchar(500) 列宽截断（issue #47）。
func TestTruncateContractRejectReason(t *testing.T) {
	if got := truncateContractRejectReason("金额需要重新核算"); got != "金额需要重新核算" {
		t.Fatalf("短原因不应改写：%q", got)
	}
	long := strings.Repeat("驳", MaxContractRejectReasonLen+10)
	got := truncateContractRejectReason(long)
	if runes := []rune(got); len(runes) != MaxContractRejectReasonLen {
		t.Fatalf("应按 %d 字符截断，实际 %d", MaxContractRejectReasonLen, len(runes))
	}
}

func TestResolveContractAmount(t *testing.T) {
	t.Run("有行项目时金额等于行项目合计，手填值忽略", func(t *testing.T) {
		got, err := ResolveContractAmount(
			moneyFrom(t, "999999.99"), // 客户端乱传的金额
			[]money.Money{moneyFrom(t, "2469.12"), moneyFrom(t, "299.97")},
		)
		if err != nil {
			t.Fatalf("有行项目求和: %v", err)
		}
		if got.String() != "2769.09" {
			t.Fatalf("金额应为行项目合计 2769.09，实际 %s", got.String())
		}
	})
	t.Run("无行项目时用手填金额", func(t *testing.T) {
		got, err := ResolveContractAmount(moneyFrom(t, "5000.00"), nil)
		if err != nil {
			t.Fatalf("无行项目手填: %v", err)
		}
		if got.String() != "5000.00" {
			t.Fatalf("手填金额应生效 5000.00，实际 %s", got.String())
		}
	})
	t.Run("无行项目时手填金额可为 0", func(t *testing.T) {
		if _, err := ResolveContractAmount(moneyFrom(t, "0"), nil); err != nil {
			t.Fatalf("0 元合同应放行: %v", err)
		}
	})
	t.Run("无行项目时手填负数拒绝", func(t *testing.T) {
		_, err := ResolveContractAmount(moneyFrom(t, "-0.01"), nil)
		if !isBadRequest(err) || !strings.Contains(err.Error(), "不能为负") {
			t.Fatalf("负数金额应 400 且文案含「不能为负」，实际 %v", err)
		}
	})
	t.Run("有行项目时手填负数也被忽略（金额来自合计）", func(t *testing.T) {
		got, err := ResolveContractAmount(moneyFrom(t, "-1"), []money.Money{moneyFrom(t, "10.00")})
		if err != nil {
			t.Fatalf("有行项目时手填值应被忽略: %v", err)
		}
		if got.String() != "10.00" {
			t.Fatalf("金额应等于行项目合计 10.00，实际 %s", got.String())
		}
	})
	t.Run("行项目合计越界拒绝", func(t *testing.T) {
		_, err := ResolveContractAmount(moneyFrom(t, "0"), []money.Money{
			moneyFrom(t, "9999999999.99"), moneyFrom(t, "9999999999.99"),
		})
		if !isBadRequest(err) {
			t.Fatalf("合计越界应 400，实际 %v", err)
		}
	})
}

func TestValidateContractLineItems(t *testing.T) {
	// 与商机共享同一行项目规则（空列表放行、产品必填、数量正整数、单价非负、不重复）。
	if err := ValidateContractLineItems(nil); err != nil {
		t.Fatalf("空行项目列表应放行: %v", err)
	}
	if err := ValidateContractLineItems([]lineItem{{ProductID: 1, Quantity: 2, Price: moneyFrom(t, "3.00")}}); err != nil {
		t.Fatalf("合法行项目应放行: %v", err)
	}
	if err := ValidateContractLineItems([]lineItem{{ProductID: 1, Quantity: 0, Price: moneyFrom(t, "3.00")}}); !isBadRequest(err) {
		t.Fatalf("数量 0 应 400，实际 %v", err)
	}
	if err := ValidateContractLineItems([]lineItem{{ProductID: 1, Quantity: 1, Price: moneyFrom(t, "-1")}}); !isBadRequest(err) {
		t.Fatalf("负单价应 400，实际 %v", err)
	}
	dup := []lineItem{
		{ProductID: 7, Quantity: 1, Price: moneyFrom(t, "1.00")},
		{ProductID: 7, Quantity: 2, Price: moneyFrom(t, "1.00")},
	}
	if err := ValidateContractLineItems(dup); !isBadRequest(err) {
		t.Fatalf("重复产品应 400，实际 %v", err)
	}
}

func TestParseContractDate(t *testing.T) {
	t.Run("空串放行返回 nil", func(t *testing.T) {
		got, err := ParseContractDate("", time.Local)
		if err != nil || got != nil {
			t.Fatalf("空串应放行且返回 nil，实际 %v %v", got, err)
		}
	})
	t.Run("纯日期与时间格式均可解析", func(t *testing.T) {
		for _, in := range []string{"2026-01-02", "2026-01-02 03:04:05"} {
			got, err := ParseContractDate(in, time.Local)
			if err != nil || got == nil {
				t.Fatalf("%q 应可解析，实际 %v %v", in, got, err)
			}
			if got.Format("2006-01-02") != "2026-01-02" {
				t.Fatalf("%q 解析日期应为 2026-01-02，实际 %s", in, got.Format("2006-01-02"))
			}
		}
	})
	t.Run("非法格式 400", func(t *testing.T) {
		if _, err := ParseContractDate("明年", time.Local); !isBadRequest(err) {
			t.Fatalf("非法日期应 400，实际 %v", err)
		}
	})
}

func TestValidateContractDateRange(t *testing.T) {
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local)
	if err := ValidateContractDateRange(nil, &end); err != nil {
		t.Fatalf("缺起始日期应放行: %v", err)
	}
	if err := ValidateContractDateRange(&start, nil); err != nil {
		t.Fatalf("缺结束日期应放行: %v", err)
	}
	if err := ValidateContractDateRange(&start, &end); err != nil {
		t.Fatalf("起 ≤ 止应放行: %v", err)
	}
	if err := ValidateContractDateRange(&end, &start); !isBadRequest(err) {
		t.Fatalf("起 > 止应 400，实际 %v", err)
	}
	same := start
	if err := ValidateContractDateRange(&start, &same); err != nil {
		t.Fatalf("起 = 止应放行（当日生效当日失效）: %v", err)
	}
}

func TestFormatContractNo(t *testing.T) {
	got := FormatContractNo("20260102", 1)
	if got != "HT202601020001" {
		t.Fatalf("编号应为 HT202601020001，实际 %s", got)
	}
	// 序号超 9999 自然扩位（唯一性优先于固定位数）。
	if got := FormatContractNo("20260102", 10000); got != "HT2026010210000" {
		t.Fatalf("序号 10000 应扩位 HT2026010210000，实际 %s", got)
	}
}

func TestValidateContractStatus(t *testing.T) {
	for s := int8(1); s <= 4; s++ {
		if err := ValidateContractStatus(s); err != nil {
			t.Fatalf("状态 %d 应合法: %v", s, err)
		}
	}
	for _, s := range []int8{0, 5, -1} {
		if err := ValidateContractStatus(s); !isBadRequest(err) {
			t.Fatalf("状态 %d 应 400，实际 %v", s, err)
		}
	}
}
