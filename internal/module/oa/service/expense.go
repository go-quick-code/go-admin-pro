// expense.go 承接报销单草稿的完整闭环（issue #54，docs/oa-mvp-spec.md
// §4.9）：草稿创建/编辑/删除、明细行整体替换与合计重算、可见范围内的
// 列表/详情、附件（infra_file，biz_type='oa_expense'）。
//
// 一致性约定：
//   - 表头金额只由明细求和产出（请求里的 totalAmount 忽略），保存时在**同一
//     事务**内写表头与明细行集，保证「表头 = 明细合计」原子一致；
//   - 删除单据在事务内软删明细行（级联），其他状态（待审/通过/撤回）409；
//   - 明细行集采用整体替换（先软删旧行再批量插入）：新增/修改/删除/排序都由
//     一次草稿保存完成，明细 ID 每次保存会变（前端按行顺序展示即可）。
//
// 额度与审批不在本工单：提交（草稿 → 待审）、bpm 实例与回调由 #55 承接，
// 状态常量和守卫（ValidateExpenseAction 的 ExpenseActionSubmit）已在此预留。
//
// 金额全程 money.Money（int64 分 + decimal(12,2)），禁止浮点；明细合计、
// 金额边界、状态守卫、类别匹配都是纯函数（expense_rule.go）。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ExpenseFileBizType 是报销附件在 infra_file 中的业务类型标识（spec §3.6）。
const ExpenseFileBizType = "oa_expense"

// ExpenseCategoryDictType 是费用类别的系统字典类型（spec §4.9/§7.3）。
const ExpenseCategoryDictType = "oa_expense_category"

// MaxExpenseClaimTitleLen 是报销标题长度上限（oa_expense_claim.title 列宽）。
const MaxExpenseClaimTitleLen = 200

// MaxExpenseClaimReasonLen 是报销事由长度上限（oa_expense_claim.reason 列宽）。
const MaxExpenseClaimReasonLen = 500

// MaxExpenseItemRemarkLen 是明细备注长度上限（oa_expense_item.remark 列宽）。
const MaxExpenseItemRemarkLen = 200

// defaultExpenseAPIPrefix 是未注入 HTTP 前缀时的兜底（与默认配置一致）。
const defaultExpenseAPIPrefix = "/admin-api"

// ExpenseFileContent 是报销附件的下载内容（已通过归属校验）。
// Reader 由调用方负责关闭（API 层流式写出后关闭）。
type ExpenseFileContent struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}

// ExpenseFileStore 是报销附件的存储依赖面（由 internal/app 装配 infra
// 适配器，与请假附件同款模式）。实现负责按 (bizType, bizID) 做归属过滤。
type ExpenseFileStore interface {
	UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.ExpenseFileResp, error)
	ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.ExpenseFileResp, error)
	DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error
	OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*ExpenseFileContent, error)
}

// SetExpenseFileStore 注入报销附件存储实现（app 装配处调用）。
func (s *Service) SetExpenseFileStore(store ExpenseFileStore) { s.expenseFiles = store }

// ---------- 草稿 ----------

// CreateExpenseClaim 创建报销草稿（spec §4.9）：表头标题/事由 + 全部明细行；
// 表头金额由明细求和（请求里的 totalAmount 忽略）。类别取字典启用值、金额
// 必须 > 0、至少一条明细，任一不满足 400。
func (s *Service) CreateExpenseClaim(ctx context.Context, req *dto.ExpenseClaimSaveReq) (*dto.ExpenseClaimDetailResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	title, err := validateExpenseTitle(req.Title)
	if err != nil {
		return nil, err
	}
	reason, err := validateExpenseReason(req.Reason)
	if err != nil {
		return nil, err
	}
	items, total, err := s.buildExpenseItems(ctx, req.Items)
	if err != nil {
		return nil, err
	}

	row := &model.ExpenseClaim{
		UserID:      userID,
		Title:       title,
		TotalAmount: total,
		Reason:      reason,
		Status:      model.ExpenseStatusDraft,
	}
	var claimID int64
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateExpenseClaim(ctx, row); err != nil {
			return err
		}
		claimID = row.ID
		return tx.ReplaceExpenseItems(ctx, claimID, items)
	})
	if err != nil {
		return nil, err
	}
	return s.GetExpenseClaimDetail(ctx, claimID)
}

// UpdateExpenseClaim 更新草稿（或已驳回单据重新编辑）：仅本人，仅草稿/已驳回
// 可改；表头字段与明细行集整体替换，合计重算。状态保持不变（重提由 #55 的
// 提交流程负责）。
//
// 校验顺序：先确认单据存在且属于本人（跨租户/他人一律 404，不泄露存在性），
// 再校验状态守卫与请求内容，最后在事务内行锁单据重检一次后落库——避免与
// 提交/删除并发交叉。
func (s *Service) UpdateExpenseClaim(ctx context.Context, req *dto.ExpenseClaimSaveReq) (*dto.ExpenseClaimDetailResp, error) {
	if req.ID <= 0 {
		return nil, xerr.BadRequest("报销单 ID 不能为空")
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	existing, err := s.findOwnedExpenseClaim(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := ValidateExpenseAction(ExpenseActionUpdate, existing.Status); err != nil {
		return nil, err
	}
	title, err := validateExpenseTitle(req.Title)
	if err != nil {
		return nil, err
	}
	reason, err := validateExpenseReason(req.Reason)
	if err != nil {
		return nil, err
	}
	items, total, err := s.buildExpenseItems(ctx, req.Items)
	if err != nil {
		return nil, err
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindExpenseClaimForUpdate(ctx, req.ID)
		if err != nil {
			return err
		}
		if row == nil || row.UserID != userID {
			return xerr.NotFound("报销单不存在")
		}
		if err := ValidateExpenseAction(ExpenseActionUpdate, row.Status); err != nil {
			return err
		}
		row.Title = title
		row.Reason = reason
		row.TotalAmount = total
		if err := tx.SaveExpenseClaim(ctx, row); err != nil {
			return err
		}
		return tx.ReplaceExpenseItems(ctx, row.ID, items)
	})
	if err != nil {
		return nil, err
	}
	return s.GetExpenseClaimDetail(ctx, req.ID)
}

// DeleteExpenseClaim 删除报销单：仅本人、仅草稿/已驳回（spec §4.14）；事务内
// 软删表头与全部明细行（级联）。附件不物理删除（与请假一致：infra_file 记录
// 保留，附件管理页可见，便于审计）。
func (s *Service) DeleteExpenseClaim(ctx context.Context, id int64) error {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return xerr.Unauthorized("")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindExpenseClaimForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || row.UserID != userID {
			return xerr.NotFound("报销单不存在")
		}
		if err := ValidateExpenseAction(ExpenseActionDelete, row.Status); err != nil {
			return err
		}
		if err := tx.SoftDeleteExpenseItemsByClaim(ctx, id); err != nil {
			return err
		}
		deleted, err := tx.DeleteExpenseClaim(ctx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return xerr.NotFound("报销单不存在")
		}
		return nil
	})
}

// ---------- 列表 / 详情 ----------

// PageExpenseClaims 报销单分页（spec §4.6 可见范围）：
// 持 oa:expense:manage → 全租户；部门负责人 → 所负责部门子树内用户的数据
// （多个部门并集）∪ 本人；其余 → 仅本人。范围外用户的行不会出现。
// 列表同时返回明细条数（一次批量查询，避免 N+1）与合计。
func (s *Service) PageExpenseClaims(ctx context.Context, req *dto.ExpenseClaimPageReq) ([]dto.ExpenseClaimItem, int64, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	scope, err := s.VisibilityScopeOf(ctx, "expense")
	if err != nil {
		return nil, 0, err
	}
	// nil 表示全租户；空切片表示什么都看不到（fail-closed）。
	var userFilter []int64
	if !scope.All {
		userFilter = scope.UserIDs
		if userFilter == nil {
			userFilter = []int64{}
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageExpenseClaimsByScope(ctx, userFilter, req.Status, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	counts, err := s.expenseItemCounts(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.ExpenseClaimItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, expenseClaimItemOf(row, counts[row.ID]))
	}
	return items, total, nil
}

// GetExpenseClaimDetail 报销单详情：表头 + 明细行 + 附件 + 审批时间线
// （草稿无实例时 approval 为 nil）。按 §4.6 可见范围读取：manage → 全租户、
// 部门负责人 → 子树、本人；范围外/跨租户一律 404，不泄露存在性。
func (s *Service) GetExpenseClaimDetail(ctx context.Context, id int64) (*dto.ExpenseClaimDetailResp, error) {
	row, err := s.findVisibleExpenseClaim(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListExpenseItemsByClaim(ctx, id)
	if err != nil {
		return nil, err
	}
	detail := &dto.ExpenseClaimDetailResp{
		ExpenseClaimItem: expenseClaimItemOf(*row, len(items)),
		Items:            expenseItemsOf(items),
		Files:            []dto.ExpenseFileResp{},
	}
	files, err := s.ListExpenseClaimFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	detail.Files = files
	if row.ApprovalInstanceID != nil {
		if s.bpm == nil {
			return nil, xerr.Internal(errors.New("oa: 审批引擎未装配"))
		}
		// 可见性已由本方法按 §4.6 校验，时间线用业务读接口（跳过 bpm 的
		// 发起人/办理人身份判定），保证部门负责人与财务能看到完整时间线。
		approval, err := s.bpm.GetInstanceDetailForBusiness(ctx, *row.ApprovalInstanceID)
		if err != nil {
			return nil, err
		}
		detail.Approval = approval
	}
	return detail, nil
}

// ---------- 附件 ----------

// ListExpenseClaimFiles 列出可见范围内报销单的附件（新上传的在前），URL
// 改写为鉴权下载端点。
func (s *Service) ListExpenseClaimFiles(ctx context.Context, id int64) ([]dto.ExpenseFileResp, error) {
	if _, err := s.findVisibleExpenseClaim(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.expenseFileStore()
	if err != nil {
		return nil, err
	}
	files, err := store.ListFiles(ctx, ExpenseFileBizType, id)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].URL = s.expenseFileDownloadURL(id, files[i].ID)
	}
	return files, nil
}

// UploadExpenseClaimFile 上传附件：仅本人、仅草稿/已驳回（提交后材料随审批
// 冻结）；大小/扩展名白名单由 infra 存储实现按统一口径校验。
func (s *Service) UploadExpenseClaimFile(ctx context.Context, id int64, fh *multipart.FileHeader) (*dto.ExpenseFileResp, error) {
	row, err := s.findOwnedExpenseClaim(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := ValidateExpenseAction(ExpenseActionUpdate, row.Status); err != nil {
		return nil, err
	}
	if fh == nil {
		return nil, xerr.BadRequest("请选择要上传的文件")
	}
	store, err := s.expenseFileStore()
	if err != nil {
		return nil, err
	}
	file, err := store.UploadFile(ctx, ExpenseFileBizType, id, fh)
	if err != nil {
		return nil, err
	}
	file.URL = s.expenseFileDownloadURL(id, file.ID)
	return &file, nil
}

// DownloadExpenseClaimFile 打开可见范围内报销单的附件供鉴权下载；单据不可见
// （含跨租户）或文件不属于该单据一律 404。
func (s *Service) DownloadExpenseClaimFile(ctx context.Context, id, fileID int64) (*ExpenseFileContent, error) {
	if _, err := s.findVisibleExpenseClaim(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.expenseFileStore()
	if err != nil {
		return nil, err
	}
	content, err := store.OpenFile(ctx, ExpenseFileBizType, id, fileID)
	if err != nil {
		return nil, err
	}
	if content == nil || content.Reader == nil {
		return nil, xerr.NotFound("文件不存在")
	}
	if content.ContentType == "" {
		content.ContentType = "application/octet-stream"
	}
	return content, nil
}

// DeleteExpenseClaimFile 删除本人草稿/已驳回单据的附件。
func (s *Service) DeleteExpenseClaimFile(ctx context.Context, id, fileID int64) error {
	row, err := s.findOwnedExpenseClaim(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateExpenseAction(ExpenseActionUpdate, row.Status); err != nil {
		return err
	}
	store, err := s.expenseFileStore()
	if err != nil {
		return err
	}
	return store.DeleteFile(ctx, ExpenseFileBizType, id, fileID)
}

// expenseFileDownloadURL 返回附件鉴权下载地址（带 API 前缀，不暴露静态直链）。
func (s *Service) expenseFileDownloadURL(claimID, fileID int64) string {
	prefix := s.apiPrefix
	if prefix == "" {
		prefix = defaultExpenseAPIPrefix
	}
	return fmt.Sprintf("%s/oa/expense/claim/%d/files/%d/download",
		strings.TrimRight(prefix, "/"), claimID, fileID)
}

// expenseFileStore 返回已装配的存储实现；未装配显式报错，不静默丢附件。
func (s *Service) expenseFileStore() (ExpenseFileStore, error) {
	if s.expenseFiles == nil {
		return nil, xerr.Internal(errors.New("oa: 报销附件存储未装配"))
	}
	return s.expenseFiles, nil
}

// ---------- 内部：取数与校验 ----------

// buildExpenseItems 校验并构造明细行集：类别取字典启用值、日期严格
// YYYY-MM-DD、备注 ≤200 字、金额 > 0；合计由纯函数 ValidateExpenseItems
// 计算（请求里的表头金额不参与）。排序按请求顺序归一化为 0..n-1。
func (s *Service) buildExpenseItems(ctx context.Context, reqs []dto.ExpenseItemSaveReq) ([]model.ExpenseItem, money.Money, error) {
	categories, err := s.sys.ListDictValues(ctx, ExpenseCategoryDictType)
	if err != nil {
		return nil, money.Money{}, err
	}
	items := make([]model.ExpenseItem, 0, len(reqs))
	amounts := make([]money.Money, 0, len(reqs))
	for i, item := range reqs {
		category, err := NormalizeExpenseCategory(item.Category, categories)
		if err != nil {
			return nil, money.Money{}, xerr.BadRequest(fmt.Sprintf("第 %d 条明细：%s", i+1, err.Error()))
		}
		date, err := s.parseExpenseDate(item.ExpenseDate)
		if err != nil {
			return nil, money.Money{}, xerr.BadRequest(fmt.Sprintf("第 %d 条明细：%s", i+1, err.Error()))
		}
		remark := strings.TrimSpace(item.Remark)
		if len([]rune(remark)) > MaxExpenseItemRemarkLen {
			return nil, money.Money{}, xerr.BadRequest(fmt.Sprintf(
				"第 %d 条明细的备注不能超过 %d 字", i+1, MaxExpenseItemRemarkLen,
			))
		}
		items = append(items, model.ExpenseItem{
			Category:    category,
			Amount:      item.Amount,
			ExpenseDate: date,
			Remark:      remark,
			Sort:        i,
		})
		amounts = append(amounts, item.Amount)
	}
	total, err := ValidateExpenseItems(amounts)
	if err != nil {
		return nil, money.Money{}, err
	}
	return items, total, nil
}

// parseExpenseDate 解析发生日期（严格 YYYY-MM-DD，400 可读错误）；解析时区
// 取应用业务时区（ADR 0006），与考勤/请假日期口径一致。
func (s *Service) parseExpenseDate(value string) (time.Time, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return time.Time{}, xerr.BadRequest("发生日期不能为空")
	}
	date, err := time.ParseInLocation("2006-01-02", text, s.businessLocation())
	if err != nil {
		return time.Time{}, xerr.BadRequest("发生日期格式应为 YYYY-MM-DD")
	}
	return date, nil
}

// expenseItemCounts 批量统计列表页各单的明细条数（一次查询，避免 N+1）。
func (s *Service) expenseItemCounts(ctx context.Context, rows []model.ExpenseClaim) (map[int64]int, error) {
	counts := make(map[int64]int, len(rows))
	if len(rows) == 0 {
		return counts, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	items, err := s.repo.ListExpenseItemsByClaimIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		counts[item.ClaimID]++
	}
	return counts, nil
}

// findOwnedExpenseClaim 读本人报销单（写动作的归属校验）：非本人/跨租户
// 一律 404，不泄露存在性。
func (s *Service) findOwnedExpenseClaim(ctx context.Context, id int64) (*model.ExpenseClaim, error) {
	row, err := s.repo.FindExpenseClaim(ctx, id)
	if err != nil {
		return nil, err
	}
	userID := CurrentUserID(ctx)
	if row == nil || userID <= 0 || row.UserID != userID {
		return nil, xerr.NotFound("报销单不存在")
	}
	return row, nil
}

// findVisibleExpenseClaim 读可见范围内的报销单（读动作的归属校验，spec §4.6）：
// 持 oa:expense:manage → 全租户；部门负责人 → 所负责部门子树；否则仅本人。
// 不存在/跨租户/范围外一律 404，不泄露存在性。
func (s *Service) findVisibleExpenseClaim(ctx context.Context, id int64) (*model.ExpenseClaim, error) {
	row, err := s.repo.FindExpenseClaim(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("报销单不存在")
	}
	scope, err := s.VisibilityScopeOf(ctx, "expense")
	if err != nil {
		return nil, err
	}
	if !scope.Permits(row.UserID) {
		return nil, xerr.NotFound("报销单不存在")
	}
	return row, nil
}

// validateExpenseTitle 校验标题：去首尾空白后 1–200 字。
func validateExpenseTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", xerr.BadRequest("报销标题不能为空")
	}
	if len([]rune(title)) > MaxExpenseClaimTitleLen {
		return "", xerr.BadRequest(fmt.Sprintf("报销标题不能超过 %d 字", MaxExpenseClaimTitleLen))
	}
	return title, nil
}

// validateExpenseReason 校验事由：去首尾空白后 1–500 字。
func validateExpenseReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", xerr.BadRequest("报销事由不能为空")
	}
	if len([]rune(reason)) > MaxExpenseClaimReasonLen {
		return "", xerr.BadRequest(fmt.Sprintf("报销事由不能超过 %d 字", MaxExpenseClaimReasonLen))
	}
	return reason, nil
}

// expenseClaimItemOf 组装报销单行视图（totalAmount 取库内合计，恒等于明细
// 求和；日期/时间统一格式化）。
func expenseClaimItemOf(row model.ExpenseClaim, itemCount int) dto.ExpenseClaimItem {
	item := dto.ExpenseClaimItem{
		ID:                 row.ID,
		UserID:             row.UserID,
		Title:              row.Title,
		TotalAmount:        row.TotalAmount,
		Reason:             row.Reason,
		Status:             row.Status,
		RejectReason:       row.RejectReason,
		ApprovalInstanceID: row.ApprovalInstanceID,
		ItemCount:          itemCount,
	}
	if !row.CreatedAt.IsZero() {
		item.CreateTime = row.CreatedAt.Format(timeLayout)
	}
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.Format(timeLayout)
	}
	return item
}

// expenseItemsOf 把明细行集映射为视图（日期归一化为 YYYY-MM-DD）。
func expenseItemsOf(rows []model.ExpenseItem) []dto.ExpenseItemResp {
	items := make([]dto.ExpenseItemResp, 0, len(rows))
	for _, row := range rows {
		items = append(items, dto.ExpenseItemResp{
			ID:          row.ID,
			Category:    row.Category,
			Amount:      row.Amount,
			ExpenseDate: DateKey(row.ExpenseDate),
			Remark:      row.Remark,
			Sort:        row.Sort,
		})
	}
	return items
}
