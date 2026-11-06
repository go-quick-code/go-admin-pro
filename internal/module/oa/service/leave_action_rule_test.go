// leave_action_rule_test.go 表驱动锁定请假发起人操作的窗口语义
// （issue #53，docs/oa-mvp-spec.md §4.8）：撤回只看业务状态（首个审批动作
// 由 bpm 权威判定）；取消要求已通过且业务时区下严格早于开始日期。
// 期望值手算核对：开始前一天可取消、开始当天与之后拒绝。
package service

import (
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// TestValidateLeaveActionWithdraw 撤回状态机：仅待审可撤回；其余状态
// （草稿/通过/驳回/撤回/已取消）一律 409。
func TestValidateLeaveActionWithdraw(t *testing.T) {
	cases := []struct {
		name    string
		status  int8
		wantErr bool
	}{
		{"草稿不可撤回", model.LeaveRequestStatusDraft, true},
		{"待审可撤回", model.LeaveRequestStatusPending, false},
		{"已通过不可撤回", model.LeaveRequestStatusApproved, true},
		{"已驳回不可撤回", model.LeaveRequestStatusRejected, true},
		{"已撤回不可重复撤回", model.LeaveRequestStatusWithdrawn, true},
		{"已取消不可撤回", model.LeaveRequestStatusCancelled, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLeaveAction(LeaveActionWithdraw, tc.status,
				testDate(t, "2026-11-20"), testDate(t, "2026-11-06"), time.UTC)
			assertActionErr(t, err, tc.wantErr, 409)
		})
	}
}

// TestValidateLeaveActionCancel status × 开始日期 × 「现在」的窗口矩阵。
func TestValidateLeaveActionCancel(t *testing.T) {
	loc := mustLoadLocation(t, "Asia/Shanghai")
	cases := []struct {
		name      string
		status    int8
		start     string
		now       time.Time
		wantErr   bool
		wantCode  int
		wantInMsg string
	}{
		{
			name: "已通过_开始前一天_可取消", status: model.LeaveRequestStatusApproved,
			start: "2026-11-07", now: time.Date(2026, 11, 6, 23, 59, 0, 0, loc),
		},
		{
			name: "已通过_开始当天凌晨_拒绝", status: model.LeaveRequestStatusApproved,
			start: "2026-11-06", now: time.Date(2026, 11, 6, 0, 0, 0, 0, loc),
			wantErr: true, wantCode: 400, wantInMsg: "联系管理员",
		},
		{
			name: "已通过_开始当天深夜_拒绝", status: model.LeaveRequestStatusApproved,
			start: "2026-11-06", now: time.Date(2026, 11, 6, 23, 59, 59, 0, loc),
			wantErr: true, wantCode: 400, wantInMsg: "联系管理员",
		},
		{
			name: "已通过_开始后_拒绝", status: model.LeaveRequestStatusApproved,
			start: "2026-11-05", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 400, wantInMsg: "联系管理员",
		},
		{
			// 时区语义：UTC 的 11-05 17:00 是上海 11-06 01:00，开始日 11-06
			// 已到；若误用 UTC「今天」会错误放行。
			name: "业务时区跨日_UTC尚未到_上海已到_拒绝", status: model.LeaveRequestStatusApproved,
			start: "2026-11-06", now: time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC),
			wantErr: true, wantCode: 400, wantInMsg: "联系管理员",
		},
		{
			name: "草稿不可取消", status: model.LeaveRequestStatusDraft,
			start: "2026-11-20", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 409,
		},
		{
			name: "待审不可取消", status: model.LeaveRequestStatusPending,
			start: "2026-11-20", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 409,
		},
		{
			name: "已驳回不可取消", status: model.LeaveRequestStatusRejected,
			start: "2026-11-20", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 409,
		},
		{
			name: "已撤回不可取消", status: model.LeaveRequestStatusWithdrawn,
			start: "2026-11-20", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 409,
		},
		{
			name: "已取消不可重复取消", status: model.LeaveRequestStatusCancelled,
			start: "2026-11-20", now: time.Date(2026, 11, 6, 9, 0, 0, 0, loc),
			wantErr: true, wantCode: 409,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLeaveAction(LeaveActionCancel, tc.status,
				testDate(t, tc.start), tc.now, loc)
			assertActionErrWithMsg(t, err, tc.wantErr, tc.wantCode, tc.wantInMsg)
		})
	}
}

// TestValidateLeaveActionUnknown 未知动作按参数错误拒绝。
func TestValidateLeaveActionUnknown(t *testing.T) {
	err := ValidateLeaveAction(LeaveRequestAction(99), model.LeaveRequestStatusPending,
		testDate(t, "2026-11-20"), testDate(t, "2026-11-06"), time.UTC)
	assertActionErr(t, err, true, 400)
}

// TestValidateLeaveActionCancelNilLocation nil 时区退回 time.Local（兜底不 panic）。
func TestValidateLeaveActionCancelNilLocation(t *testing.T) {
	start := testDate(t, "2026-11-20")
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local)
	if err := ValidateLeaveAction(LeaveActionCancel, model.LeaveRequestStatusApproved, start, now, nil); err != nil {
		t.Fatalf("开始前应可取消：%v", err)
	}
}

// assertActionErr 断言错误是否存在及 HTTP 状态码。
func assertActionErr(t *testing.T, err error, wantErr bool, wantCode int) {
	t.Helper()
	if !wantErr {
		if err != nil {
			t.Fatalf("期望通过，实际错误：%v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("期望错误（HTTP %d），实际通过", wantCode)
	}
	assertActionCode(t, err, wantCode)
}

// assertActionErrWithMsg 在状态码之外断言可读错误信息包含关键提示。
func assertActionErrWithMsg(t *testing.T, err error, wantErr bool, wantCode int, wantInMsg string) {
	t.Helper()
	if !wantErr {
		if err != nil {
			t.Fatalf("期望通过，实际错误：%v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("期望错误（HTTP %d），实际通过", wantCode)
	}
	assertActionCode(t, err, wantCode)
	if wantInMsg != "" && !strings.Contains(err.Error(), wantInMsg) {
		t.Fatalf("错误信息 %q 应包含 %q", err.Error(), wantInMsg)
	}
}

func assertActionCode(t *testing.T, err error, wantCode int) {
	t.Helper()
	bizErr, ok := xerr.As(err)
	if !ok {
		t.Fatalf("期望业务错误，实际 %T：%v", err, err)
	}
	if bizErr.Status != wantCode {
		t.Fatalf("HTTP 状态 = %d，期望 %d（msg=%q）", bizErr.Status, wantCode, bizErr.Msg)
	}
}

// mustLoadLocation 加载 IANA 时区（测试数据恒合法，失败直接 Fatal）。
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("加载时区 %s: %v", name, err)
	}
	return loc
}
