// leave_attendance.go 承接请假与考勤的联动（issue #62，docs/oa-mvp-spec.md
// §4.8/§4.10）：
//
//   - LeaveDaysProvider 的生产实现：按用户 + 日期返回当日已通过请假天数
//     （0 / 0.5 / 1），供 #61 的结果派生写入 leave_days（整天 → 请假状态，
//     半天只记录不改状态）；
//   - 请假通过（bpm 回调事务提交后）与取消（业务事务提交后）按请假区间重算
//     考勤结果——重算读的是**已提交**的请假状态，因此必须放在提交之后。
//
// 天数口径全部复用纯函数 LeaveDaysOnDate（leave_attendance_rule.go）：
// 与 #52 请假时长同源（#50 工作日槽位），非工作日不贡献。
package service

import (
	"context"
	"log/slog"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// NewLeaveDaysProvider 构造请假天数取数实现（app 装配时注入 oa Service，
// issue #61 的 SetLeaveDaysProvider 接缝）。repo 提供已通过请假与工作日历
// 读取，租户条件由仓储层的租户插件按 ctx 自动追加。
func NewLeaveDaysProvider(repo *repository.Repository) LeaveDaysProvider {
	return &leaveDaysProvider{repo: repo}
}

// leaveDaysProvider 是 LeaveDaysProvider 的生产实现。
type leaveDaysProvider struct {
	repo *repository.Repository
}

// LeaveDaysOn 返回 userID 在 date（YYYY-MM-DD）当天的已通过请假天数：
//   - 非工作日（休息日/节假日）不贡献，返回 0；
//   - 当天覆盖的已通过申请逐条按槽位裁剪后累加（同一天最多 1 天）；
//   - 日期格式非法返回 400（调用方均为内部归一化后的日期键，属防御分支）。
func (p *leaveDaysProvider) LeaveDaysOn(ctx context.Context, userID int64, date string) (float64, error) {
	if userID <= 0 {
		return 0, nil
	}
	parsed, err := NormalizeDate(date)
	if err != nil {
		return 0, xerr.BadRequest("请假天数取数的日期格式应为 YYYY-MM-DD")
	}
	key := DateKey(parsed)
	rows, err := p.repo.ListWorkdaysByRange(ctx, key, key)
	if err != nil {
		return 0, err
	}
	explicit := explicitDaysOf(rows)
	if ResolveDayType(parsed, explicit) != WorkdayTypeWorking {
		return 0, nil
	}

	requests, err := p.repo.ListApprovedLeaveRequestsCoveringDate(ctx, userID, key)
	if err != nil {
		return 0, err
	}
	total := 0.0
	for i := range requests {
		row := &requests[i]
		total += LeaveDaysOnDate(parsed, row.StartDate, row.EndDate, row.StartHalf, row.EndHalf, explicit)
	}
	if total > 1 {
		total = 1 // 一人一天最多 1 天（结果列语义 0/0.5/1）
	}
	return total, nil
}

// AfterApproved 实现 bpm 的审批提交后钩子（bpm.ApprovalCommitHook）：请假审批
// 通过的事务提交成功后，按请假区间重算考勤结果（spec §4.10「请假通过触发
// 重算」）。放在提交后是硬要求——重算经 LeaveDaysProvider 读已通过的请假
// 记录，事务内读不到未提交的状态。重算失败只记录日志（审批已生效，打卡/
// 每日任务/手工重算可补正），不回滚审批。
func (s *Service) AfterApproved(ctx context.Context, inst *bpmmodel.ProcessInstance) {
	if inst == nil {
		return
	}
	row, err := s.repo.FindLeaveRequest(ctx, inst.BusinessID)
	if err != nil {
		slog.Error("recompute attendance after leave approval: read leave request failed",
			"err", err, "instanceId", inst.ID, "leaveRequestId", inst.BusinessID)
		return
	}
	if row == nil || row.Status != model.LeaveRequestStatusApproved {
		return
	}
	s.recomputeLeaveAttendance(ctx, row.UserID, DateKey(row.StartDate), DateKey(row.EndDate), "approve")
}

// recomputeLeaveAttendance 按请假区间重算考勤结果（best-effort）：审批/取消已成
// 事实，重算失败不反压主流程；结果可被打卡/每日任务/手工重算修正。
func (s *Service) recomputeLeaveAttendance(ctx context.Context, userID int64, startKey, endKey, trigger string) {
	if userID <= 0 || startKey > endKey {
		return
	}
	days, err := s.RecomputeUserRange(ctx, &dto.AttendanceRecomputeReq{
		UserID: userID, StartDate: startKey, EndDate: endKey,
	})
	if err != nil {
		slog.Error("recompute attendance after leave change failed",
			"err", err, "trigger", trigger, "userId", userID, "start", startKey, "end", endKey)
		return
	}
	slog.Info("recompute attendance after leave change done",
		"trigger", trigger, "userId", userID, "start", startKey, "end", endKey, "days", days)
}
