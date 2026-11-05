package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 商机纯函数测试（issue #15）：阶段状态机（推进合法性、终态锁定）、
// 行项目校验、金额计算与合计。错误一律用户可读中文（400）。

// isBadRequest 断言错误是 400 业务错误（用户可见，中文文案）。
func isBadRequest(err error) bool {
	e, ok := xerr.As(err)
	return ok && e.Status == 400
}

func TestCanAdvanceStage(t *testing.T) {
	mustOK := func(phase string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s：期望放行，实际错误 %v", phase, err)
		}
	}
	must400 := func(phase string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s：期望拒绝，实际放行", phase)
		}
		if !isBadRequest(err) {
			t.Fatalf("%s：期望 400 业务错误，实际 %v", phase, err)
		}
		if strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("%s：错误文案不应为空", phase)
		}
	}

	t.Run("进行中 1-4 可互相推进（含回退）", func(t *testing.T) {
		for _, from := range []int8{1, 2, 3, 4} {
			for _, to := range []int8{1, 2, 3, 4} {
				mustOK(fmt.Sprintf("阶段%d→阶段%d", from, to), CanAdvanceStage(from, to))
			}
		}
	})

	t.Run("进行中可推进到赢单/输单", func(t *testing.T) {
		for _, from := range []int8{1, 2, 3, 4} {
			mustOK("初步→赢单", CanAdvanceStage(from, model.OpportunityStageWon))
			mustOK("初步→输单", CanAdvanceStage(from, model.OpportunityStageLost))
		}
	})

	t.Run("同阶段推进视为无操作放行", func(t *testing.T) {
		for _, s := range []int8{1, 2, 3, 4, 5, 6} {
			mustOK("自身推进", CanAdvanceStage(s, s))
		}
	})

	t.Run("终态不可再变更（赢单/输单锁定）", func(t *testing.T) {
		must400("赢单→初步", CanAdvanceStage(model.OpportunityStageWon, model.OpportunityStageInitial))
		must400("赢单→谈判", CanAdvanceStage(model.OpportunityStageWon, model.OpportunityStageNegotiate))
		must400("赢单→输单", CanAdvanceStage(model.OpportunityStageWon, model.OpportunityStageLost))
		must400("输单→初步", CanAdvanceStage(model.OpportunityStageLost, model.OpportunityStageInitial))
		must400("输单→报价", CanAdvanceStage(model.OpportunityStageLost, model.OpportunityStageQuote))
		must400("输单→赢单", CanAdvanceStage(model.OpportunityStageLost, model.OpportunityStageWon))
	})

	t.Run("非法阶段拒绝", func(t *testing.T) {
		must400("from 越下界", CanAdvanceStage(0, model.OpportunityStageInitial))
		must400("from 越上界", CanAdvanceStage(7, model.OpportunityStageWon))
		must400("to 越下界", CanAdvanceStage(model.OpportunityStageInitial, 0))
		must400("to 越上界", CanAdvanceStage(model.OpportunityStageInitial, 7))
		must400("from 与 to 都非法", CanAdvanceStage(0, 9))
	})

	t.Run("终态拒绝文案点名当前阶段", func(t *testing.T) {
		err := CanAdvanceStage(model.OpportunityStageWon, model.OpportunityStageInitial)
		if !strings.Contains(err.Error(), "赢单") {
			t.Fatalf("文案应点名当前终态，实际 %q", err.Error())
		}
	})
}

func TestValidateOpportunityStage(t *testing.T) {
	if err := ValidateOpportunityStage(model.OpportunityStageQuote); err != nil {
		t.Fatalf("合法阶段不应报错：%v", err)
	}
	if err := ValidateOpportunityStage(9); err == nil {
		t.Fatal("非法阶段应报错")
	}
}

func TestValidateLineItems(t *testing.T) {
	mk := func(productID, quantity int64, price string) lineItem {
		p, err := money.ParseMoney(price)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", price, err)
		}
		return lineItem{ProductID: productID, Quantity: quantity, Price: p}
	}

	t.Run("空行项目放行（商机允许无行项目，预计金额手填）", func(t *testing.T) {
		if err := ValidateLineItems(nil); err != nil {
			t.Fatalf("空行项目应放行：%v", err)
		}
	})

	t.Run("合法行项目放行", func(t *testing.T) {
		items := []lineItem{mk(1, 2, "100.00"), mk(2, 1, "0.00")}
		if err := ValidateLineItems(items); err != nil {
			t.Fatalf("合法行项目应放行：%v", err)
		}
	})

	t.Run("数量必须正整数", func(t *testing.T) {
		for _, qty := range []int64{0, -1} {
			err := ValidateLineItems([]lineItem{mk(1, qty, "1.00")})
			if err == nil {
				t.Fatalf("数量 %d 应拒绝", qty)
			}
			if !strings.Contains(err.Error(), "数量") {
				t.Fatalf("文案应点名数量，实际 %q", err.Error())
			}
		}
	})

	t.Run("单价不能为负", func(t *testing.T) {
		err := ValidateLineItems([]lineItem{mk(1, 1, "-0.01")})
		if err == nil {
			t.Fatal("负单价应拒绝")
		}
		if !strings.Contains(err.Error(), "单价") {
			t.Fatalf("文案应点名单价，实际 %q", err.Error())
		}
		// 零单价放行（赠品行）。
		if err := ValidateLineItems([]lineItem{mk(1, 1, "0.00")}); err != nil {
			t.Fatalf("零单价应放行：%v", err)
		}
	})

	t.Run("产品必填", func(t *testing.T) {
		err := ValidateLineItems([]lineItem{mk(0, 1, "1.00")})
		if err == nil {
			t.Fatal("产品 ID 为 0 应拒绝")
		}
		if !strings.Contains(err.Error(), "产品") {
			t.Fatalf("文案应点名产品，实际 %q", err.Error())
		}
	})

	t.Run("同一商机内产品不可重复", func(t *testing.T) {
		items := []lineItem{mk(1, 2, "1.00"), mk(2, 1, "1.00"), mk(1, 3, "1.00")}
		err := ValidateLineItems(items)
		if err == nil {
			t.Fatal("重复产品应拒绝")
		}
		if !strings.Contains(err.Error(), "重复") {
			t.Fatalf("文案应点名重复，实际 %q", err.Error())
		}
	})

	t.Run("错误带行序号便于定位", func(t *testing.T) {
		items := []lineItem{mk(1, 1, "1.00"), mk(2, 0, "1.00")}
		err := ValidateLineItems(items)
		if err == nil || !strings.Contains(err.Error(), "2") {
			t.Fatalf("文案应带行序号 2，实际 %v", err)
		}
	})
}

func TestComputeLineAmount(t *testing.T) {
	parse := func(t *testing.T, s string) money.Money {
		t.Helper()
		m, err := money.ParseMoney(s)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", s, err)
		}
		return m
	}

	t.Run("金额 = 数量 × 单价", func(t *testing.T) {
		got, err := ComputeLineAmount(3, parse(t, "1234.56"))
		if err != nil {
			t.Fatalf("ComputeLineAmount: %v", err)
		}
		if got.String() != "3703.68" {
			t.Fatalf("金额 = %q, 期望 3703.68", got.String())
		}
	})

	t.Run("越界返回错误", func(t *testing.T) {
		_, err := ComputeLineAmount(100000, parse(t, "9999999999.99"))
		if !errors.Is(err, money.ErrOutOfRange) {
			t.Fatalf("越界应透传 money.ErrOutOfRange，实际 %v", err)
		}
	})
}

func TestSumLineAmounts(t *testing.T) {
	parse := func(t *testing.T, s string) money.Money {
		t.Helper()
		m, err := money.ParseMoney(s)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", s, err)
		}
		return m
	}

	t.Run("空列表合计 0.00", func(t *testing.T) {
		got, err := SumLineAmounts(nil)
		if err != nil {
			t.Fatalf("空合计不应报错：%v", err)
		}
		if got.String() != "0.00" {
			t.Fatalf("空合计 = %q, 期望 0.00", got.String())
		}
	})

	t.Run("多行合计", func(t *testing.T) {
		total, err := SumLineAmounts([]money.Money{
			parse(t, "3703.68"), parse(t, "0.01"), parse(t, "299.31"),
		})
		if err != nil {
			t.Fatalf("合计不应报错：%v", err)
		}
		if total.String() != "4003.00" {
			t.Fatalf("合计 = %q, 期望 4003.00", total.String())
		}
	})
}
