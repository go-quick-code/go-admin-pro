// leave.go 承载请假类型与额度管理的编排（issue #51，docs/oa-mvp-spec.md
// §3.5/§4.8）：类型 CRUD/启停/删除约束、额度初始化（幂等）、管理端分页、
// 员工本人余额、管理员调整（原因必填并留痕）。判定语义全部在纯函数
// （leave_rule.go），本文件只做参数校验、取数与落库编排。
// 申请表（oa_leave_request）的写入路径由 #52 承接；本工单只读其行数支撑
// 「被引用类型只能停用不能删除」（spec §4.14）。
package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// 类型维护的取值边界（spec §3.5 列宽约束）：名称/编码长度、备注长度、
// 单次额度初始化的用户数上限。
const (
	MaxLeaveTypeNameLen = 50
	MaxLeaveTypeCodeLen = 50
	MaxLeaveRemarkLen   = 200
	MaxLeaveReasonLen   = 200
	MaxInitUsers        = 1000
	MaxInitTypes        = 100
)

// timeLayout 是额度行更新时间的展示格式。
const timeLayout = "2006-01-02 15:04:05"

// leaveTypeCodePattern 类型编码：小写字母/数字/连字符，不以连字符开头结尾。
var leaveTypeCodePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ---------- 请假类型 ----------

// CreateLeaveType 创建类型（默认启用）：名称/编码租户内唯一（409）、编码
// 格式受限、额度一位小数；不占用额度的类型年度额度必须为 0（避免无意义
// 配置）。code 创建后不可变。
func (s *Service) CreateLeaveType(ctx context.Context, req *dto.LeaveTypeCreateReq) (*model.LeaveType, error) {
	if err := validateLeaveTypeFields(req.Name, req.Code, req.BalanceManaged, req.AnnualQuota, req.Remark); err != nil {
		return nil, err
	}
	if dup, err := s.repo.FindLeaveTypeByCodeOrName(ctx, req.Code, req.Name, 0); err != nil {
		return nil, err
	} else if dup != nil {
		return nil, duplicateLeaveTypeErr(dup, req.Code, req.Name)
	}

	row := &model.LeaveType{
		Name:           req.Name,
		Code:           req.Code,
		BalanceManaged: req.BalanceManaged,
		AnnualQuota:    req.AnnualQuota,
		Status:         model.LeaveTypeStatusEnabled,
		Sort:           req.Sort,
		Remark:         req.Remark,
	}
	if err := s.repo.CreateLeaveType(ctx, row); err != nil {
		return nil, mapLeaveTypeDup(err, req.Code, req.Name)
	}
	return row, nil
}

// UpdateLeaveType 更新类型（code 不可变，spec 与 bpm key 同语义）：名称唯一
// 性排除自身；不存在 404。
func (s *Service) UpdateLeaveType(ctx context.Context, req *dto.LeaveTypeUpdateReq) (*model.LeaveType, error) {
	row, err := s.repo.FindLeaveType(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("请假类型不存在")
	}
	if err := validateLeaveTypeFields(req.Name, row.Code, req.BalanceManaged, req.AnnualQuota, req.Remark); err != nil {
		return nil, err
	}
	if dup, err := s.repo.FindLeaveTypeByCodeOrName(ctx, "", req.Name, row.ID); err != nil {
		return nil, err
	} else if dup != nil && dup.Name == req.Name {
		return nil, xerr.Conflict("类型名称已存在：" + req.Name)
	}

	row.Name = req.Name
	row.BalanceManaged = req.BalanceManaged
	row.AnnualQuota = req.AnnualQuota
	row.Sort = req.Sort
	row.Remark = req.Remark
	if err := s.repo.SaveLeaveType(ctx, row); err != nil {
		return nil, mapLeaveTypeDup(err, row.Code, req.Name)
	}
	return row, nil
}

// SetLeaveTypeStatus 启停类型（被引用时只能停用，停用后不可发起新请假）。
func (s *Service) SetLeaveTypeStatus(ctx context.Context, id int64, status int8) error {
	if status != model.LeaveTypeStatusEnabled && status != model.LeaveTypeStatusDisabled {
		return xerr.BadRequest("状态取值应为 0 启用 / 1 停用")
	}
	row, err := s.repo.FindLeaveType(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("请假类型不存在")
	}
	row.Status = status
	return s.repo.SaveLeaveType(ctx, row)
}

// DeleteLeaveType 删除类型：被申请引用（oa_leave_request 有行，任意状态）
// 时只能停用不能删除（spec §4.14），返回 409；不存在 404。软删行仍占用
// 编码/名称唯一键（编码不复用，与 bpm key 语义一致）。
func (s *Service) DeleteLeaveType(ctx context.Context, id int64) error {
	row, err := s.repo.FindLeaveType(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("请假类型不存在")
	}
	count, err := s.repo.CountLeaveRequestsByType(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return xerr.Conflict(fmt.Sprintf("类型 %s 已被 %d 条请假申请引用，只能停用不能删除", row.Name, count))
	}
	_, err = s.repo.DeleteLeaveType(ctx, id)
	return err
}

// ListLeaveTypes 全量类型列表（管理页筛选；员工发起请假登录即可用）。
func (s *Service) ListLeaveTypes(ctx context.Context, status *int8, keyword string) ([]model.LeaveType, error) {
	return s.repo.ListLeaveTypes(ctx, status, keyword)
}

// validateLeaveTypeFields 校验类型字段的取值边界（唯一性在调用方处理）。
func validateLeaveTypeFields(name, code string, managed bool, quota money.Money, remark string) error {
	if n := len([]rune(name)); n == 0 || n > MaxLeaveTypeNameLen {
		return xerr.BadRequest(fmt.Sprintf("类型名称不能为空且不超过 %d 字", MaxLeaveTypeNameLen))
	}
	if c := len([]rune(code)); c == 0 || c > MaxLeaveTypeCodeLen {
		return xerr.BadRequest(fmt.Sprintf("类型编码不能为空且不超过 %d 字符", MaxLeaveTypeCodeLen))
	}
	if !leaveTypeCodePattern.MatchString(code) {
		return xerr.BadRequest("类型编码应为小写字母/数字/连字符（如 annual、sick-leave），不以连字符开头结尾")
	}
	if len([]rune(remark)) > MaxLeaveRemarkLen {
		return xerr.BadRequest(fmt.Sprintf("备注不能超过 %d 字", MaxLeaveRemarkLen))
	}
	return validateDayQuota(quota, managed)
}

// validateDayQuota 校验天数额度：一位小数（半天 0.5）、在 decimal(5,1) 范围内、
// 非负；不占用额度的类型必须为 0。
func validateDayQuota(quota money.Money, managed bool) error {
	if !IsDayPrecision(quota) {
		return xerr.BadRequest("额度最多一位小数（半天为 0.5）")
	}
	if !IsDayRange(quota) {
		return xerr.BadRequest("额度超出范围（最大 9999.9 天）")
	}
	if quota.Cents() < 0 {
		return xerr.BadRequest("额度不能为负数")
	}
	if !managed && quota.Cents() != 0 {
		return xerr.BadRequest("不占用额度的类型年度额度应为 0")
	}
	return nil
}

// duplicateLeaveTypeErr 把唯一性预检命中翻译为可读 409。
func duplicateLeaveTypeErr(dup *model.LeaveType, code, name string) error {
	if dup.Code == code && code != "" {
		return xerr.Conflict("类型编码已存在：" + code)
	}
	return xerr.Conflict("类型名称已存在：" + name)
}

// mapLeaveTypeDup 把并发/软删行占码的数据库唯一键错误翻译为 409。
func mapLeaveTypeDup(err error, code, name string) error {
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return err
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && strings.Contains(mysqlErr.Message, "uk_oa_leave_type_tenant_code") {
		return xerr.Conflict("类型编码已存在：" + code)
	}
	return xerr.Conflict("类型名称已存在：" + name)
}

// ---------- 请假额度 ----------

// InitBalances 按 (用户 × 类型, 自然年) 幂等初始化额度：已有行不重复建、
// 不覆盖（保护已调整数据），缺失行以类型年度额度快照落行。
// typeIds 省略时默认全部启用中的占用额度类型；非占用额度类型 400。
func (s *Service) InitBalances(ctx context.Context, req *dto.BalanceInitReq) (*dto.BalanceInitResp, error) {
	if !IsValidYear(req.Year) {
		return nil, xerr.BadRequest("年度应为 2000–2100 的自然年")
	}
	if len(req.UserIDs) == 0 {
		return nil, xerr.BadRequest("请提供 userIds 员工列表")
	}
	if len(req.UserIDs) > MaxInitUsers {
		return nil, xerr.BadRequest(fmt.Sprintf("单次初始化不能超过 %d 名员工", MaxInitUsers))
	}
	userIDs := dedupePositive(req.UserIDs)
	if len(userIDs) == 0 {
		return nil, xerr.BadRequest("userIds 应为有效的用户 ID")
	}
	if len(req.TypeIDs) > MaxInitTypes {
		return nil, xerr.BadRequest(fmt.Sprintf("单次初始化不能超过 %d 个类型", MaxInitTypes))
	}

	// 目标类型：显式列表（须启用且占用额度）或默认全部启用中的占用额度类型。
	var types []model.LeaveType
	if len(req.TypeIDs) > 0 {
		rows, err := s.repo.ListLeaveTypesByIDs(ctx, dedupePositive(req.TypeIDs))
		if err != nil {
			return nil, err
		}
		if len(rows) != len(dedupePositive(req.TypeIDs)) {
			return nil, xerr.NotFound("请假类型不存在或未启用")
		}
		types = rows
	} else {
		enabled := model.LeaveTypeStatusEnabled
		rows, err := s.repo.ListLeaveTypes(ctx, &enabled, "")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.BalanceManaged {
				types = append(types, row)
			}
		}
	}
	for _, row := range types {
		if !row.BalanceManaged {
			return nil, xerr.BadRequest(fmt.Sprintf("类型 %s 不占用额度，无需初始化额度", row.Name))
		}
	}
	if len(types) == 0 {
		return nil, xerr.BadRequest("没有可初始化的占用额度类型")
	}

	typeIDs := make([]int64, 0, len(types))
	quotaByType := make(map[int64]money.Money, len(types))
	for _, row := range types {
		typeIDs = append(typeIDs, row.ID)
		quotaByType[row.ID] = row.AnnualQuota
	}

	// 幂等计划：wanted（去重后的全组合）− existing = 缺失行。
	wanted := make([]BalanceKey, 0, len(userIDs)*len(typeIDs))
	for _, uid := range userIDs {
		for _, tid := range typeIDs {
			wanted = append(wanted, BalanceKey{UserID: uid, LeaveTypeID: tid, Year: req.Year})
		}
	}
	wanted = MissingInitKeys(wanted, nil) // 同批去重
	existing, err := s.repo.ListBalancesCross(ctx, req.Year, userIDs, typeIDs)
	if err != nil {
		return nil, err
	}
	existingKeys := make([]BalanceKey, 0, len(existing))
	for _, row := range existing {
		existingKeys = append(existingKeys, BalanceKey{UserID: row.UserID, LeaveTypeID: row.LeaveTypeID, Year: row.Year})
	}
	missing := MissingInitKeys(wanted, existingKeys)

	rows := make([]model.LeaveBalance, 0, len(missing))
	for _, key := range missing {
		rows = append(rows, model.LeaveBalance{
			UserID:      key.UserID,
			LeaveTypeID: key.LeaveTypeID,
			Year:        key.Year,
			Quota:       quotaByType[key.LeaveTypeID],
		})
	}
	for i := range rows {
		if err := s.repo.CreateLeaveBalance(ctx, &rows[i]); err != nil {
			return nil, err
		}
	}
	return &dto.BalanceInitResp{Created: len(rows), Total: len(wanted)}, nil
}

// PageBalances 管理端额度分页（oa:leave:manage）：附类型与员工昵称，余额
// 可用 = quota + adjustment - used（AvailableDays 纯函数）。
func (s *Service) PageBalances(ctx context.Context, req *dto.BalancePageReq) ([]dto.BalanceItemResp, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageBalances(ctx, req.UserID, req.LeaveTypeID, req.Year, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}

	typeByID, err := s.typeMap(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.BalanceItemResp, 0, len(rows))
	if len(rows) > 0 {
		userIDs := make([]int64, 0, len(rows))
		for _, row := range rows {
			userIDs = append(userIDs, row.UserID)
		}
		names, err := s.sys.UserNicknames(ctx, userIDs)
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			items = append(items, balanceItemOf(row, typeByID[row.LeaveTypeID], names[row.UserID]))
		}
	}
	return items, total, nil
}

// MyBalances 员工本人的额度视图（登录即可）：对启用中的每个类型各一行，
// 占用额度类型缺行时余额全零（尚未初始化）；不占额度类型仅展示标记。
// year 省略时取当前自然年。
func (s *Service) MyBalances(ctx context.Context, year int) (*dto.MineBalancesResp, error) {
	if year == 0 {
		year = time.Now().Year()
	}
	if !IsValidYear(year) {
		return nil, xerr.BadRequest("年度应为 2000–2100 的自然年")
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}

	enabled := model.LeaveTypeStatusEnabled
	types, err := s.repo.ListLeaveTypes(ctx, &enabled, "")
	if err != nil {
		return nil, err
	}
	typeIDs := make([]int64, 0, len(types))
	for _, row := range types {
		if row.BalanceManaged {
			typeIDs = append(typeIDs, row.ID)
		}
	}
	balances, err := s.repo.ListBalancesCross(ctx, year, []int64{userID}, typeIDs)
	if err != nil {
		return nil, err
	}
	balanceByType := make(map[int64]*model.LeaveBalance, len(balances))
	for i := range balances {
		balanceByType[balances[i].LeaveTypeID] = &balances[i]
	}

	resp := &dto.MineBalancesResp{Year: year, Items: make([]dto.MineBalanceItemResp, 0, len(types))}
	for _, typ := range types {
		item := dto.MineBalanceItemResp{
			LeaveTypeID:    typ.ID,
			TypeCode:       typ.Code,
			TypeName:       typ.Name,
			BalanceManaged: typ.BalanceManaged,
			AnnualQuota:    typ.AnnualQuota,
		}
		if typ.BalanceManaged {
			item.Balance = &dto.BalanceItem{Year: year}
			if row := balanceByType[typ.ID]; row != nil {
				item.Balance = balanceItemValues(row)
			}
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

// AdjustBalance 管理员手工调整（oa:leave:manage）：adjustment 累加 delta
// （可负、非零、一位小数），原因必填并留痕（adjust_reason + updater/updated_at
// 由审计插件回填，spec §4.8）。额度行不存在时先按类型年度额度补行（调整即
// 初始化）；事务 + 行锁串行化并发调整。返回调整后的余额视图。
func (s *Service) AdjustBalance(ctx context.Context, req *dto.BalanceAdjustReq) (*dto.BalanceItemResp, error) {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, xerr.BadRequest("调整必须填写原因")
	}
	if len([]rune(reason)) > MaxLeaveReasonLen {
		return nil, xerr.BadRequest(fmt.Sprintf("调整原因不能超过 %d 字", MaxLeaveReasonLen))
	}
	if !IsValidYear(req.Year) {
		return nil, xerr.BadRequest("年度应为 2000–2100 的自然年")
	}
	if req.Delta.Cents() == 0 {
		return nil, xerr.BadRequest("调整量不能为 0（负数表示调减）")
	}
	if !IsDayPrecision(req.Delta) {
		return nil, xerr.BadRequest("调整量最多一位小数（半天为 0.5）")
	}
	if !IsDayRange(req.Delta) {
		return nil, xerr.BadRequest("调整量超出范围（±9999.9 天）")
	}

	typ, err := s.repo.FindLeaveType(ctx, req.LeaveTypeID)
	if err != nil {
		return nil, err
	}
	if typ == nil {
		return nil, xerr.NotFound("请假类型不存在")
	}
	if !typ.BalanceManaged {
		return nil, xerr.BadRequest(fmt.Sprintf("类型 %s 不占用额度，无需调整", typ.Name))
	}

	var row *model.LeaveBalance
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		existing, err := tx.FindBalanceForUpdate(ctx, req.UserID, req.LeaveTypeID, req.Year)
		if err != nil {
			return err
		}
		if existing == nil {
			existing = &model.LeaveBalance{
				UserID:      req.UserID,
				LeaveTypeID: req.LeaveTypeID,
				Year:        req.Year,
				Quota:       typ.AnnualQuota,
			}
			if err := tx.CreateLeaveBalance(ctx, existing); err != nil {
				return err
			}
		}
		adjustment := money.FromCents(existing.Adjustment.Cents() + req.Delta.Cents())
		if !IsDayRange(adjustment) {
			return xerr.BadRequest("调整后的累计调整超出范围（±9999.9 天）")
		}
		existing.Adjustment = adjustment
		existing.AdjustReason = reason
		if err := tx.SaveLeaveBalance(ctx, existing); err != nil {
			return err
		}
		row = existing
		return nil
	})
	if err != nil {
		return nil, err
	}

	typeByID, err := s.typeMap(ctx)
	if err != nil {
		return nil, err
	}
	names, err := s.sys.UserNicknames(ctx, []int64{req.UserID})
	if err != nil {
		return nil, err
	}
	item := balanceItemOf(*row, typeByID[row.LeaveTypeID], names[row.UserID])
	return &item, nil
}

// balanceItemOf 组装管理端额度行视图（附类型与员工昵称）。
func balanceItemOf(row model.LeaveBalance, typ *model.LeaveType, userName string) dto.BalanceItemResp {
	item := dto.BalanceItemResp{
		ID:           row.ID,
		UserID:       row.UserID,
		UserName:     userName,
		LeaveTypeID:  row.LeaveTypeID,
		Year:         row.Year,
		AdjustReason: row.AdjustReason,
		Updater:      updaterID(row.Updater),
	}
	if typ != nil {
		item.TypeCode = typ.Code
		item.TypeName = typ.Name
		item.BalanceManaged = typ.BalanceManaged
	}
	item.Quota = row.Quota
	item.Adjustment = row.Adjustment
	item.Used = row.Used
	item.Available = AvailableDays(row.Quota, row.Adjustment, row.Used)
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.Format(timeLayout)
	}
	return item
}

// balanceItemValues 提取额度数值（「我的额度」行内嵌）。
func balanceItemValues(row *model.LeaveBalance) *dto.BalanceItem {
	item := &dto.BalanceItem{
		Year:         row.Year,
		Quota:        row.Quota,
		Adjustment:   row.Adjustment,
		Used:         row.Used,
		Available:    AvailableDays(row.Quota, row.Adjustment, row.Used),
		AdjustReason: row.AdjustReason,
		Updater:      updaterID(row.Updater),
	}
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.Format(timeLayout)
	}
	return item
}

// updaterID 把审计字段 updater（用户 ID 字符串，系统账号 "0"）转为数值。
func updaterID(updater string) int64 {
	id, _ := strconv.ParseInt(updater, 10, 64)
	return id
}

// typeMap 全量类型索引（页内组装用，类型量级为个位数）。
func (s *Service) typeMap(ctx context.Context) (map[int64]*model.LeaveType, error) {
	rows, err := s.repo.ListLeaveTypes(ctx, nil, "")
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*model.LeaveType, len(rows))
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// dedupePositive 去重并剔除非正 ID（保持首次出现顺序）。
func dedupePositive(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
