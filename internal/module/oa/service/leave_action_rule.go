// leave_action_rule.go 承载请假发起人操作的窗口判定（issue #53，
// docs/oa-mvp-spec.md §4.8/§5）：撤回与取消。全部为**纯函数**（无存储访问、
// 无 HTTP 上下文），服务层只负责取数与事务编排：
//
//   - 撤回：仅「待审」状态可发起；「首个审批动作前」由 bpm 的
//     WithdrawInstance 权威判定（本函数只管业务状态机）；
//   - 取消：仅「已通过」状态，且开始日期尚未到达（严格早于开始日）；
//     「今天」按应用业务时区（app.timezone）计算，开始当天即视为已开始。
//
// 非法流转一律返回可读的 409（状态不允许）或 400（窗口已过），
// 表驱动测试锁定语义（leave_action_rule_test.go）。
package service

import (
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// LeaveRequestAction 是请假发起人可执行的两种操作。
type LeaveRequestAction int8

const (
	// LeaveActionWithdraw 撤回：待审且首个审批动作前。
	LeaveActionWithdraw LeaveRequestAction = 1
	// LeaveActionCancel 取消：已通过且开始日期未到。
	LeaveActionCancel LeaveRequestAction = 2
)

// ValidateLeaveAction 判定发起人操作在当前状态与时间窗口下是否可执行
// （纯函数，spec §4.8）：
//
//   - 撤回：status 必须是待审（pending）；是否已有审批动作由 bpm 撤回入口
//     在实例事务内判定，避免业务侧重复实现审批事实的判定；
//   - 取消：status 必须是已通过（approved），且业务时区下的「今天」严格早于
//     开始日期——开始当天即不可取消（此时请假已发生）；
//   - 其余状态返回 409；取消窗口已过返回 400，并提示联系管理员调整记录
//     （spec §4.8：开始日期后由管理员直接调整）。
//
// loc 为应用业务时区（config.Location()）；nil 时退回 time.Local，与服务层
// 既有时间口径一致，生产装配必须传入配置时区。
func ValidateLeaveAction(action LeaveRequestAction, status int8, startDate, now time.Time, loc *time.Location) error {
	switch action {
	case LeaveActionWithdraw:
		if status != model.LeaveRequestStatusPending {
			return xerr.Conflict("当前状态不可撤回（仅待审且尚无审批动作的申请可撤回）")
		}
		return nil
	case LeaveActionCancel:
		if status != model.LeaveRequestStatusApproved {
			return xerr.Conflict("仅已通过的请假申请可取消")
		}
		if loc == nil {
			loc = time.Local
		}
		// 日期键按业务时区归一化后比较：开始当天视为已开始（不可取消）。
		if DateKey(startDate) <= DateKey(now.In(loc)) {
			return xerr.BadRequest(fmt.Sprintf(
				"请假已开始（%s），不能取消；如需调整请假记录请联系管理员",
				DateKey(startDate),
			))
		}
		return nil
	default:
		return xerr.BadRequest("未知的请假操作")
	}
}
