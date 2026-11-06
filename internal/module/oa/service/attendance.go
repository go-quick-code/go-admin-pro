// attendance.go 承载考勤基础的管理与打卡编排（issue #60，docs/oa-mvp-spec.md
// §3.9/§4.10）：班次 CRUD+启停、考勤组 CRUD+启停、组员物化（按用户/按部门
// 批量添加，后续不自动同步部门变化）、Web 打卡（时间/IP/User-Agent，同日多次
// 保留）。打卡后对当日结果重算（issue #61）：结果派生/校正与每日任务见
// attendance_result.go。
package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 打卡来源列上限（与迁移列宽一致，超长截断避免落库报错）。
const (
	maxIPColumnLen     = 64
	maxUserAgentColLen = MaxUserAgentLength
)

// SetLocation 注入业务时区（ADR 0006：唯一来源是 config.Location()，app 装配
// 时调用）。打卡日期按业务时区取当天（spec §4.10）；未注入时退回本地时区
// （测试装配恒注入）。
func (s *Service) SetLocation(loc *time.Location) { s.location = loc }

// locationOf 返回业务时区（未注入退回 time.Local）。
func (s *Service) locationOf() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// ---------- 班次 ----------

// CreateShift 新建班次（默认启用）。时段校验失败/字段越界 400。
func (s *Service) CreateShift(ctx context.Context, req *dto.ShiftCreateReq) (*dto.AttendanceShiftResp, error) {
	name, startTime, endTime, graceMinutes, sort, err := validateShiftFields(req.Name, req.StartTime, req.EndTime, req.GraceMinutes, req.Sort)
	if err != nil {
		return nil, err
	}
	row := &model.AttendanceShift{
		Name:         name,
		StartTime:    startTime,
		EndTime:      endTime,
		GraceMinutes: graceMinutes,
		Status:       model.AttendanceStatusEnabled,
		Sort:         sort,
	}
	if err := s.repo.CreateShift(ctx, row); err != nil {
		return nil, err
	}
	resp := dto.NewShiftResp(row)
	return &resp, nil
}

// UpdateShift 更新班次；不存在（含跨租户）404。
func (s *Service) UpdateShift(ctx context.Context, id int64, req *dto.ShiftUpdateReq) error {
	row, err := s.mustShift(ctx, id)
	if err != nil {
		return err
	}
	name, startTime, endTime, graceMinutes, sort, err := validateShiftFields(req.Name, req.StartTime, req.EndTime, req.GraceMinutes, req.Sort)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateShift(ctx, row.ID, name, startTime, endTime, graceMinutes, sort); err != nil {
		return err
	}
	return nil
}

// SetShiftStatus 启停班次；状态值非法 400，班次不存在 404。
func (s *Service) SetShiftStatus(ctx context.Context, id int64, status int8) error {
	if !ValidAttendanceStatus(status) {
		return xerr.BadRequest("状态应为 0 启用 / 1 停用")
	}
	row, err := s.mustShift(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.SetShiftStatus(ctx, row.ID, status)
}

// DeleteShift 删除班次；被考勤组绑定时 409（先调整考勤组），不存在 404。
func (s *Service) DeleteShift(ctx context.Context, id int64) error {
	row, err := s.mustShift(ctx, id)
	if err != nil {
		return err
	}
	bound, err := s.repo.CountGroupsByShift(ctx, row.ID)
	if err != nil {
		return err
	}
	if bound > 0 {
		return xerr.Conflict("班次已被考勤组绑定，不能删除；请先调整相关考勤组")
	}
	_, err = s.repo.DeleteShift(ctx, row.ID)
	return err
}

// GetShift 班次详情；不存在 404。
func (s *Service) GetShift(ctx context.Context, id int64) (*dto.AttendanceShiftResp, error) {
	row, err := s.mustShift(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := dto.NewShiftResp(row)
	return &resp, nil
}

// PageShifts 分页查询班次（name 模糊、status 精确；sort 升序）。
func (s *Service) PageShifts(ctx context.Context, req *dto.ShiftPageReq) ([]dto.AttendanceShiftResp, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageShifts(ctx, repository.ShiftFilter{
		Name:   trimSpace(req.Name),
		Status: req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	list := make([]dto.AttendanceShiftResp, 0, len(rows))
	for i := range rows {
		list = append(list, dto.NewShiftResp(&rows[i]))
	}
	return list, total, nil
}

// mustShift 按 ID 读班次，不存在（含跨租户）404——不泄露存在性。
func (s *Service) mustShift(ctx context.Context, id int64) (*model.AttendanceShift, error) {
	row, err := s.repo.FindShiftByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("班次不存在")
	}
	return row, nil
}

// validateShiftFields 班次字段统一校验：名称、time-of-day 时段（不支持跨天）、
// 宽限分钟、排序。返回修剪后的名称与原样 time-of-day。
func validateShiftFields(name, startTime, endTime string, graceMinutes, sort int) (string, string, string, int, int, error) {
	name, err := ValidateAttendanceName(name)
	if err != nil {
		return "", "", "", 0, 0, err
	}
	startTime, err = NormalizeTimeOfDay(trimSpace(startTime))
	if err != nil {
		return "", "", "", 0, 0, xerr.BadRequest("上班时间格式应为 HH:MM（24 小时制，如 09:00）")
	}
	endTime, err = NormalizeTimeOfDay(trimSpace(endTime))
	if err != nil {
		return "", "", "", 0, 0, xerr.BadRequest("下班时间格式应为 HH:MM（24 小时制，如 18:00）")
	}
	if err := ValidateShiftWindow(startTime, endTime); err != nil {
		return "", "", "", 0, 0, err
	}
	if err := ValidateGraceMinutes(graceMinutes); err != nil {
		return "", "", "", 0, 0, err
	}
	if err := ValidateAttendanceSort(sort); err != nil {
		return "", "", "", 0, 0, err
	}
	return name, startTime, endTime, graceMinutes, sort, nil
}

// ---------- 考勤组 ----------

// CreateGroup 新建考勤组（默认启用）。绑定班次必须存在（跨租户/不存在 404）。
func (s *Service) CreateGroup(ctx context.Context, req *dto.GroupCreateReq) (*dto.AttendanceGroupResp, error) {
	name, err := ValidateAttendanceName(req.Name)
	if err != nil {
		return nil, err
	}
	if _, err := s.mustShift(ctx, req.ShiftID); err != nil {
		return nil, err
	}
	row := &model.AttendanceGroup{Name: name, ShiftID: req.ShiftID, Status: model.AttendanceStatusEnabled}
	if err := s.repo.CreateGroup(ctx, row); err != nil {
		return nil, err
	}
	return s.groupResp(ctx, row)
}

// UpdateGroup 更新考勤组（名称/绑定班次）；组或班次不存在 404。
func (s *Service) UpdateGroup(ctx context.Context, id int64, req *dto.GroupUpdateReq) error {
	row, err := s.mustGroup(ctx, id)
	if err != nil {
		return err
	}
	name, err := ValidateAttendanceName(req.Name)
	if err != nil {
		return err
	}
	if _, err := s.mustShift(ctx, req.ShiftID); err != nil {
		return err
	}
	return s.repo.UpdateGroup(ctx, row.ID, name, req.ShiftID)
}

// SetGroupStatus 启停考勤组（停用后组员打卡 400）；状态值非法 400，组不存在 404。
func (s *Service) SetGroupStatus(ctx context.Context, id int64, status int8) error {
	if !ValidAttendanceStatus(status) {
		return xerr.BadRequest("状态应为 0 启用 / 1 停用")
	}
	row, err := s.mustGroup(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.SetGroupStatus(ctx, row.ID, status)
}

// DeleteGroup 删除考勤组；仍有组员时 409（spec §4.14：被引用只能停用，
// 请先移出组员或停用），不存在 404。删除在同一事务内兜底解除组员关系。
func (s *Service) DeleteGroup(ctx context.Context, id int64) error {
	row, err := s.mustGroup(ctx, id)
	if err != nil {
		return err
	}
	members, err := s.repo.ListGroupUsersByGroup(ctx, row.ID)
	if err != nil {
		return err
	}
	if len(members) > 0 {
		return xerr.Conflict("考勤组下仍有组员，不能删除；请先移出组员或停用考勤组")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if _, err := tx.DeleteGroup(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteGroupUsersByGroup(ctx, row.ID)
	})
}

// GetGroup 考勤组详情（含组员列表，昵称经 system 网关回填）。
func (s *Service) GetGroup(ctx context.Context, id int64) (*dto.AttendanceGroupResp, error) {
	row, err := s.mustGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.groupResp(ctx, row)
}

// PageGroups 分页查询考勤组。
func (s *Service) PageGroups(ctx context.Context, req *dto.GroupPageReq) ([]dto.AttendanceGroupResp, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageGroups(ctx, repository.GroupFilter{
		Name:   trimSpace(req.Name),
		Status: req.Status,
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	list := make([]dto.AttendanceGroupResp, 0, len(rows))
	for i := range rows {
		resp, err := s.groupPageResp(ctx, &rows[i])
		if err != nil {
			return nil, 0, err
		}
		list = append(list, *resp)
	}
	return list, total, nil
}

// groupPageResp 组装列表行（绑定班次摘要 + 组员人数；不拉组员明细，
// 与 DTO 约定一致：详情才带 Members）。
func (s *Service) groupPageResp(ctx context.Context, row *model.AttendanceGroup) (*dto.AttendanceGroupResp, error) {
	resp := &dto.AttendanceGroupResp{
		ID:      row.ID,
		Name:    row.Name,
		ShiftID: row.ShiftID,
		Status:  row.Status,
		Members: []dto.GroupMemberItem{},
	}
	if shift, err := s.repo.FindShiftByID(ctx, row.ShiftID); err != nil {
		return nil, err
	} else if shift != nil {
		shiftResp := dto.NewShiftResp(shift)
		resp.Shift = &shiftResp
	}
	count, err := s.repo.CountGroupUsers(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	resp.MemberCount = count
	return resp, nil
}

// AddGroupMembers 按用户批量添加组员（物化入组）：仅接受本租户启用用户，
// 同批去重；用户已在其他组时改绑到本组（一个用户只属于一个组）。
// 返回实际入组人数。
func (s *Service) AddGroupMembers(ctx context.Context, groupID int64, req *dto.AddMembersReq) (int, error) {
	row, err := s.mustGroup(ctx, groupID)
	if err != nil {
		return 0, err
	}
	if len(req.UserIDs) == 0 {
		return 0, xerr.BadRequest("请选择要添加的成员")
	}
	enabled, err := s.sys.ListSimpleUsers(ctx)
	if err != nil {
		return 0, err
	}
	valid := make(map[int64]struct{}, len(enabled))
	for _, u := range enabled {
		valid[u.ID] = struct{}{}
	}
	ids := dedupeInt64(req.UserIDs, valid)
	if len(ids) == 0 {
		return 0, xerr.BadRequest("所选成员不存在或未启用，无法添加")
	}
	if err := s.repo.UpsertGroupUsers(ctx, row.ID, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// AddGroupMembersByDept 按部门批量添加组员：取部门当前成员（system 网关解析）
// 物化入组；后续部门人员变化不自动同步（spec §4.10）。返回实际入组人数。
func (s *Service) AddGroupMembersByDept(ctx context.Context, groupID int64, req *dto.AddMembersByDeptReq) (int, error) {
	row, err := s.mustGroup(ctx, groupID)
	if err != nil {
		return 0, err
	}
	if len(req.DeptIDs) == 0 {
		return 0, xerr.BadRequest("请选择要导入的部门")
	}
	userIDs, err := s.sys.ListUserIDsByDeptIDs(ctx, req.DeptIDs)
	if err != nil {
		return 0, err
	}
	if len(userIDs) == 0 {
		return 0, xerr.BadRequest("所选部门下没有成员，无法添加")
	}
	// 部门解析结果含停用用户；组员仅物化启用用户（与按用户添加同口径）。
	enabled, err := s.sys.ListSimpleUsers(ctx)
	if err != nil {
		return 0, err
	}
	valid := make(map[int64]struct{}, len(enabled))
	for _, u := range enabled {
		valid[u.ID] = struct{}{}
	}
	ids := dedupeInt64(userIDs, valid)
	if len(ids) == 0 {
		return 0, xerr.BadRequest("所选部门下没有启用成员，无法添加")
	}
	if err := s.repo.UpsertGroupUsers(ctx, row.ID, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// RemoveGroupMember 移出组员；组不存在 404，成员不在该组 404。
func (s *Service) RemoveGroupMember(ctx context.Context, groupID, userID int64) error {
	row, err := s.mustGroup(ctx, groupID)
	if err != nil {
		return err
	}
	deleted, err := s.repo.DeleteGroupUser(ctx, row.ID, userID)
	if err != nil {
		return err
	}
	if !deleted {
		return xerr.NotFound("该用户不在考勤组中")
	}
	return nil
}

// mustGroup 按 ID 读考勤组，不存在（含跨租户）404——不泄露存在性。
func (s *Service) mustGroup(ctx context.Context, id int64) (*model.AttendanceGroup, error) {
	row, err := s.repo.FindGroupByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("考勤组不存在")
	}
	return row, nil
}

// groupResp 组装考勤组响应（含绑定班次摘要与组员列表/人数）。
func (s *Service) groupResp(ctx context.Context, row *model.AttendanceGroup) (*dto.AttendanceGroupResp, error) {
	resp := &dto.AttendanceGroupResp{
		ID:      row.ID,
		Name:    row.Name,
		ShiftID: row.ShiftID,
		Status:  row.Status,
	}
	if shift, err := s.repo.FindShiftByID(ctx, row.ShiftID); err != nil {
		return nil, err
	} else if shift != nil {
		shiftResp := dto.NewShiftResp(shift)
		resp.Shift = &shiftResp
	}
	members, err := s.repo.ListGroupUsersByGroup(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	resp.MemberCount = int64(len(members))
	resp.Members = make([]dto.GroupMemberItem, 0, len(members))
	if len(members) > 0 {
		ids := make([]int64, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.UserID)
		}
		nicknames, err := s.sys.UserNicknames(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			resp.Members = append(resp.Members, dto.GroupMemberItem{
				UserID:   m.UserID,
				Nickname: nicknames[m.UserID],
			})
		}
	}
	return resp, nil
}

// dedupeInt64 去重并按 keep 过滤（keep 为 nil 时仅去重），保持输入升序稳定输出。
func dedupeInt64(in []int64, keep map[int64]struct{}) []int64 {
	seen := make(map[int64]struct{}, len(in))
	out := make([]int64, 0, len(in))
	for _, id := range in {
		if _, ok := seen[id]; ok {
			continue
		}
		if keep != nil {
			if _, ok := keep[id]; !ok {
				continue
			}
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ---------- Web 打卡 ----------

// Punch 上/下班打卡（type：0 上班 / 1 下班）。身份收口当前登录人：
//   - 未加入任何考勤组（或组已被删除）→ 400「你尚未加入任何考勤组…」；
//   - 所在考勤组已停用 → 400「你所在的考勤组已停用…」；
//   - 打卡日期按业务时区取当天；同一天允许多次，全部保留（不覆盖）；
//   - 记录 IP 与 User-Agent（超长截断到列宽）。
func (s *Service) Punch(ctx context.Context, punchType int8, ip, userAgent string) (*dto.PunchResp, error) {
	if !ValidPunchType(punchType) {
		return nil, xerr.BadRequest("打卡类型应为 上班 / 下班")
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	group, err := s.enabledGroupOfUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().In(s.locationOf())
	row := &model.AttendancePunch{
		UserID:    userID,
		PunchDate: mustUTCDate(now), // DATE 列存业务时区当天的零点表示
		PunchTime: now,
		Type:      punchType,
		Source:    model.PunchSourceWeb,
		IP:        truncateRunes(ip, maxIPColumnLen),
		UserAgent: truncateRunes(userAgent, maxUserAgentColLen),
	}
	if err := s.repo.CreatePunch(ctx, row); err != nil {
		return nil, err
	}
	// 打卡后立即重算当日结果（issue #61，spec §4.10 重算触发点）。重算幂等；
	// 失败不影响已落库的打卡（每日任务/手工重算会收敛），但错误如实返回。
	if _, err := s.RecomputeUserDate(ctx, userID, DateKey(row.PunchDate)); err != nil {
		return nil, err
	}
	resp := newPunchResp(row, group)
	return &resp, nil
}

// enabledGroupOfUser 解析当前用户启用的考勤组（spec §4.10：无考勤组或组停用
// 时打卡 400）。未入组/组已删除返回明确 400；组停用返回另一文案的 400。
func (s *Service) enabledGroupOfUser(ctx context.Context, userID int64) (*model.AttendanceGroup, error) {
	membership, err := s.repo.FindGroupUserByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if membership == nil {
		return nil, xerr.BadRequest("你尚未加入任何考勤组，无法打卡，请联系管理员加入考勤组")
	}
	group, err := s.repo.FindGroupByID(ctx, membership.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		// 组已被删除但成员关系残留：按未分组处理（删组会级联清成员，此处兜底）。
		return nil, xerr.BadRequest("你尚未加入任何考勤组，无法打卡，请联系管理员加入考勤组")
	}
	if group.Status != model.AttendanceStatusEnabled {
		return nil, xerr.BadRequest("你所在的考勤组已停用，无法打卡，请联系管理员")
	}
	return group, nil
}

// GetMyPunchToday 我的打卡状态（员工打卡页）：所在启用考勤组与班次信息
// （未加入任何启用组时 group 为 null，页面据此提示与禁用按钮）+ 当天全部打卡。
// 状态探测不因未入组报错（打卡动作才 400）。
func (s *Service) GetMyPunchToday(ctx context.Context) (*dto.MyPunchTodayResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	now := time.Now().In(s.locationOf())
	resp := &dto.MyPunchTodayResp{
		Date:    DateKey(now),
		Group:   nil,
		Punches: []dto.PunchResp{},
	}

	// 组信息：入组即展示（含停用组，页面标注停用状态），未入组为 null。
	if membership, err := s.repo.FindGroupUserByUser(ctx, userID); err != nil {
		return nil, err
	} else if membership != nil {
		if group, err := s.repo.FindGroupByID(ctx, membership.GroupID); err != nil {
			return nil, err
		} else if group != nil {
			info, err := s.punchGroupInfo(ctx, group)
			if err != nil {
				return nil, err
			}
			resp.Group = info
		}
	}

	rows, err := s.repo.ListPunchesByUserDate(ctx, userID, DateKey(now))
	if err != nil {
		return nil, err
	}
	for i := range rows {
		resp.Punches = append(resp.Punches, newPunchResp(&rows[i], nil))
	}
	return resp, nil
}

// PageMyPunches 我的打卡记录（分页，punch_time 降序；日期区间可选）。
func (s *Service) PageMyPunches(ctx context.Context, req *dto.PunchPageReq) ([]dto.PunchResp, int64, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PagePunchesByUser(ctx, userID, repository.PunchFilter{
		StartDate: trimSpace(req.StartDate),
		EndDate:   trimSpace(req.EndDate),
	}, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	list := make([]dto.PunchResp, 0, len(rows))
	for i := range rows {
		list = append(list, newPunchResp(&rows[i], nil))
	}
	return list, total, nil
}

// punchGroupInfo 组装打卡页的组/班次信息。
func (s *Service) punchGroupInfo(ctx context.Context, group *model.AttendanceGroup) (*dto.PunchGroupInfo, error) {
	info := &dto.PunchGroupInfo{ID: group.ID, Name: group.Name, Status: group.Status}
	if shift, err := s.repo.FindShiftByID(ctx, group.ShiftID); err != nil {
		return nil, err
	} else if shift != nil {
		info.Shift = &dto.AttendanceShiftResp{
			ID:           shift.ID,
			Name:         shift.Name,
			StartTime:    shift.StartTime,
			EndTime:      shift.EndTime,
			GraceMinutes: shift.GraceMinutes,
			Status:       shift.Status,
			Sort:         shift.Sort,
		}
	}
	return info, nil
}

// newPunchResp 由打卡行构造响应（group 非空时附带组摘要，供打卡动作回显）。
func newPunchResp(row *model.AttendancePunch, group *model.AttendanceGroup) dto.PunchResp {
	resp := dto.PunchResp{
		ID:         row.ID,
		Type:       row.Type,
		TypeName:   PunchTypeName(row.Type),
		Date:       DateKey(row.PunchDate),
		Time:       row.PunchTime.Format(time.RFC3339),
		Source:     row.Source,
		SourceName: PunchSourceName(row.Source),
		IP:         row.IP,
		UserAgent:  row.UserAgent,
	}
	if group != nil {
		resp.GroupID = group.ID
		resp.GroupName = group.Name
	}
	return resp
}
