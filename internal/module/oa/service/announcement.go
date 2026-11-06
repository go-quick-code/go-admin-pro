// announcement.go 承载公告域的编排（issue #48/#49，docs/oa-mvp-spec.md
// §3.4/§4.7/§4.14/§5）：公告 CRUD、受众维护、发布/撤回状态流转、员工侧
// 我的公告与详情、附件（infra_file 按 biz_type='oa_announcement' 归属）、
// 打开详情幂等写已读与已读统计（issue #49）。
// 判定语义全部在纯函数（announcement_rule.go / announcement_read_rule.go）：
// 受众可见性、发布收件人展开、状态机决策、未读标记与统计口径；本文件只做
// 参数校验、取数与落库编排。
//
// 发布在**一个事务**内完成「公告置发布 + 为受众批量写 announcement 通知」
// （spec §4.7/§4.13：通知与源操作同生共死，notify 写失败则发布回滚）。
// 富文本入库前统一经 bluemonday 白名单清洗（spec §4.7），返回时仍是清洗后
// 的 HTML。已读记录是追加型数据：撤回/重新发布不删除也不重置。
package service

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"strings"
	"time"

	notifymodel "github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/text"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/microcosm-cc/bluemonday"
)

// 公告维护的取值边界。
const (
	MaxAnnouncementTitleLen   = 200
	MaxAnnouncementContentLen = 200_000
	MaxAnnouncementTargets    = 200
	// announcementNotifyTitleLen 与 notify_message.title 列宽一致。
	announcementNotifyTitleLen = 128
	// AnnouncementFileBizType 是公告附件在 infra_file 中的业务类型标识。
	AnnouncementFileBizType = "oa_announcement"
	// AnnouncementPermissionQuery 是公告管理列表/管理详情预览的权限码。
	AnnouncementPermissionQuery = "oa:announcement:query"
	// AnnouncementPermissionUpdate 是公告编辑与附件维护的权限码。
	AnnouncementPermissionUpdate = "oa:announcement:update"
	// AnnouncementPermissionReadStats 是已读统计（人数/名单）的权限码
	// （按钮 5215，issue #49）。
	AnnouncementPermissionReadStats = "oa:announcement:read-stats"
)

// defaultAPIPrefix 是未注入 HTTP 前缀时的兜底（与 configs/config.yaml 默认值一致）。
const defaultAPIPrefix = "/admin-api"

// announcementHTMLPolicy 是公告正文的白名单清洗策略：UGC 策略允许常见的
// 段落/标题/列表/链接/图片/表格标签，移除 script/style/事件处理器等。
var announcementHTMLPolicy = bluemonday.UGCPolicy()

// AnnouncementFileStore 是公告附件的存储依赖面（由 internal/app 装配 infra
// 适配器；与合同附件同一模式）。实现负责按 (bizType, bizID) 做归属过滤：
// 不属于该业务的文件一律按不存在处理。
type AnnouncementFileStore interface {
	// UploadFile 上传文件并写入业务归属，大小/扩展名限制复用 infra 口径。
	UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.AnnouncementFileResp, error)
	// ListFiles 列出业务归属下的文件（新上传的在前）。
	ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.AnnouncementFileResp, error)
	// DeleteFile 删除业务归属下的文件；文件不属于该业务时返回 404。
	DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error
	// OpenFile 打开业务归属下的文件供鉴权下载；不属于该业务时返回 404。
	OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*dto.AnnouncementFileContent, error)
	// SoftDeleteFilesByBiz 软删除业务归属下的文件记录（草稿删除时保留文件
	// 记录，仅置 deleted_at；spec §4.7）。
	SoftDeleteFilesByBiz(ctx context.Context, bizType string, bizID int64) error
}

// SetAnnouncementFileStore 注入公告附件存储实现（app 装配处调用）。
// SetAPIPrefix 是共享注入点（与请假附件同款，见 leave_request.go）。
func (s *Service) SetAnnouncementFileStore(store AnnouncementFileStore) {
	s.announcementFiles = store
}

// ---------- 管理端列表/详情 ----------

// PageAnnouncements 管理端分页（oa:announcement:query）：全部状态，置顶在前。
// 列表行携带当前管理员对每条公告的已读状态（与员工列表/详情同口径）。
func (s *Service) PageAnnouncements(ctx context.Context, req *dto.AnnouncementPageReq) ([]dto.AnnouncementItemResp, int64, error) {
	req.Normalize()
	if req.Status != nil && !oamodel.ValidAnnouncementStatus(*req.Status) {
		return nil, 0, xerr.BadRequest("公告状态取值应为 0 草稿 / 1 发布 / 2 撤回")
	}
	if req.AudienceType != nil && !oamodel.ValidAudienceType(*req.AudienceType) {
		return nil, 0, xerr.BadRequest("受众类型取值应为 0 全员 / 1 自定义")
	}
	rows, total, err := s.repo.PageAnnouncements(ctx, repository.AnnouncementFilter{
		Status:       req.Status,
		AudienceType: req.AudienceType,
		Pinned:       req.Pinned,
		Keyword:      strings.TrimSpace(req.Keyword),
	}, CurrentUserID(ctx), req.Offset(), req.PageSize)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.AnnouncementItemResp, 0, len(rows))
	for i := range rows {
		items = append(items, announcementItemOf(&rows[i].Announcement, rows[i].Read))
	}
	return items, total, nil
}

// PageMyAnnouncements 员工「我的公告」：只返回已发布且命中受众的公告；
// 受众判定走仓储单一 EXISTS 谓词（与纯函数 Audience.Permits 同语义）。
// 列表行携带当前用户的已读状态（issue #49 未读标记）。
func (s *Service) PageMyAnnouncements(ctx context.Context, req *dto.AnnouncementMyPageReq) ([]dto.AnnouncementItemResp, int64, error) {
	req.Normalize()
	viewer, err := s.audienceViewerOf(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := s.repo.PageVisibleAnnouncements(ctx, viewer, strings.TrimSpace(req.Keyword), req.Offset(), req.PageSize)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.AnnouncementItemResp, 0, len(rows))
	for i := range rows {
		items = append(items, announcementItemOf(&rows[i].Announcement, rows[i].Read))
	}
	return items, total, nil
}

// GetAnnouncement 公告详情（打开即已读，issue #49）：
// 已发布公告对命中受众的登录用户可见；已撤回公告仍对命中受众可见（spec §4.7：
// 撤回不删除已写通知，用户打开详情看到「已撤回」），但不再出现在列表；
// 草稿仅 oa:announcement:query 持有者可预览。都不可见时按 404 处理，
// 不泄露存在性。通过可见性判定后幂等写入已读记录（唯一键保证重复打开
// 不产生重复行、保留首次 read_at）。
func (s *Service) GetAnnouncement(ctx context.Context, id int64) (*dto.AnnouncementDetailResp, error) {
	row, _, err := s.findAccessibleAnnouncement(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkAnnouncementRead(ctx, id, CurrentUserID(ctx), s.businessNow()); err != nil {
		return nil, err
	}
	targets, err := s.repo.ListAnnouncementTargets(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.announcementDetailOf(ctx, row, targets, true)
}

// PageAnnouncementReadStats 已读统计（oa:announcement:read-stats，issue #49）：
// 返回去重后的已读人数与分页名单（昵称经 system 网关补齐）。统计口径只认
// oa_announcement_read 的记录本身：撤回保留历史已读，重新发布不重置；
// 跨租户/不存在的公告按 404 处理（FindAnnouncement 已带租户条件）。
func (s *Service) PageAnnouncementReadStats(ctx context.Context, id int64, req *dto.AnnouncementReadStatsReq) (*dto.AnnouncementReadStatsResp, error) {
	req.Normalize()
	row, err := s.repo.FindAnnouncement(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("公告不存在")
	}
	rows, total, err := s.repo.PageAnnouncementReads(ctx, id, req.Offset(), req.PageSize)
	if err != nil {
		return nil, err
	}
	readUsers := make([]AnnouncementReadUser, 0, len(rows))
	for i := range rows {
		readUsers = append(readUsers, AnnouncementReadUser{UserID: rows[i].UserID, ReadAt: rows[i].ReadAt})
	}
	readUsers = AggregateAnnouncementReadUsers(readUsers)
	userIDs := make([]int64, 0, len(readUsers))
	for _, user := range readUsers {
		userIDs = append(userIDs, user.UserID)
	}
	nicknames, err := s.sys.UserNicknames(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	list := make([]dto.AnnouncementReadUserResp, 0, len(readUsers))
	for _, user := range readUsers {
		list = append(list, dto.AnnouncementReadUserResp{
			UserID:   user.UserID,
			Nickname: nicknames[user.UserID],
			ReadAt:   user.ReadAt,
		})
	}
	return &dto.AnnouncementReadStatsResp{
		ReadCount: total,
		Total:     total,
		List:      list,
	}, nil
}

// ---------- 创建/编辑/删除 ----------

// CreateAnnouncement 创建公告（草稿）：content 经 bluemonday 清洗后入库，
// 自定义受众目标去重校验并与主表同事务写入。
func (s *Service) CreateAnnouncement(ctx context.Context, req *dto.AnnouncementCreateReq) (*dto.AnnouncementDetailResp, error) {
	title, content, audienceType, err := validateAnnouncementFields(req.Title, req.Content, req.AudienceType)
	if err != nil {
		return nil, err
	}
	targets, err := buildAnnouncementTargets(0, audienceType, req.Targets)
	if err != nil {
		return nil, err
	}
	row := &oamodel.Announcement{
		Title:        title,
		Content:      content,
		Status:       oamodel.AnnouncementStatusDraft,
		AudienceType: audienceType,
		Pinned:       req.Pinned,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateAnnouncement(ctx, row); err != nil {
			return err
		}
		for i := range targets {
			targets[i].AnnouncementID = row.ID
		}
		return tx.ReplaceAnnouncementTargets(ctx, row.ID, targets)
	})
	if err != nil {
		return nil, err
	}
	return s.announcementDetailOf(ctx, row, targets, false)
}

// UpdateAnnouncement 编辑公告（仅草稿）：受众目标整体替换；发布后不可编辑
// （spec §5：PUT 仅草稿）。
func (s *Service) UpdateAnnouncement(ctx context.Context, req *dto.AnnouncementUpdateReq) (*dto.AnnouncementDetailResp, error) {
	title, content, audienceType, err := validateAnnouncementFields(req.Title, req.Content, req.AudienceType)
	if err != nil {
		return nil, err
	}
	targets, err := buildAnnouncementTargets(0, audienceType, req.Targets)
	if err != nil {
		return nil, err
	}

	var row *oamodel.Announcement
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		current, err := tx.FindAnnouncementForUpdate(ctx, req.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return xerr.NotFound("公告不存在")
		}
		if !CanEditAnnouncement(current.Status) {
			return xerr.Conflict("公告已发布或已撤回，不能编辑")
		}
		current.Title = title
		current.Content = content
		current.AudienceType = audienceType
		current.Pinned = req.Pinned
		if err := tx.SaveAnnouncement(ctx, current); err != nil {
			return err
		}
		for i := range targets {
			targets[i].AnnouncementID = current.ID
		}
		if err := tx.ReplaceAnnouncementTargets(ctx, current.ID, targets); err != nil {
			return err
		}
		row = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	read, err := s.announcementReadStateOf(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return s.announcementDetailOf(ctx, row, targets, read)
}

// DeleteAnnouncement 删除公告（仅草稿）：发布后不能删除，只能撤回
// （spec §4.7/§4.14）。草稿删除时软删除关联附件记录，不做物理删除。
func (s *Service) DeleteAnnouncement(ctx context.Context, id int64) error {
	var deleted bool
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		current, err := tx.FindAnnouncementForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if current == nil {
			return xerr.NotFound("公告不存在")
		}
		if !CanDeleteAnnouncement(current.Status) {
			return xerr.Conflict("公告已发布，不能删除，只能撤回")
		}
		deleted, err = tx.DeleteAnnouncement(ctx, id)
		return err
	})
	if err != nil {
		return err
	}
	if !deleted {
		return xerr.NotFound("公告不存在")
	}
	store, err := s.announcementFileStore()
	if err != nil {
		return err
	}
	return store.SoftDeleteFilesByBiz(ctx, AnnouncementFileBizType, id)
}

// ---------- 状态流转 ----------

// PublishAnnouncement 发布/重新发布公告（草稿或撤回状态）：事务内锁读
// 公告、展开受众收件人、置发布状态并批量写 announcement 通知；任一步失败
// 整笔回滚（spec §4.7/§4.13）。
func (s *Service) PublishAnnouncement(ctx context.Context, id int64) error {
	if s.notify == nil {
		return xerr.Internal(errors.New("公告发布未装配通知网关"))
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindAnnouncementForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("公告不存在")
		}
		if !CanPublishAnnouncement(row.Status) {
			return xerr.Conflict("公告已发布，请先撤回后再重新发布")
		}
		recipients, err := s.resolveAudienceRecipients(ctx, tx, row)
		if err != nil {
			return err
		}
		now := time.Now()
		row.Status = oamodel.AnnouncementStatusPublished
		row.PublishedAt = &now
		row.WithdrawnAt = nil
		if err := tx.SaveAnnouncement(ctx, row); err != nil {
			return err
		}
		return s.notify.CreateInTx(ctx, tx.DB(), buildAnnouncementNotifies(row, recipients))
	})
}

// WithdrawAnnouncement 撤回公告（仅已发布）：置撤回状态并记录撤回时间；
// 已写通知不删除，用户打开详情看到「已撤回」（spec §4.7）。
func (s *Service) WithdrawAnnouncement(ctx context.Context, id int64) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindAnnouncementForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("公告不存在")
		}
		if !CanWithdrawAnnouncement(row.Status) {
			return xerr.Conflict("仅已发布的公告可以撤回")
		}
		now := time.Now()
		row.Status = oamodel.AnnouncementStatusWithdrawn
		row.WithdrawnAt = &now
		return tx.SaveAnnouncement(ctx, row)
	})
}

// resolveAudienceRecipients 展开发布收件人：全员取租户内启用用户；自定义
// 取所选部门子树用户 ∪ 所选角色启用用户（纯函数 ResolveAudienceUserIDs）。
func (s *Service) resolveAudienceRecipients(ctx context.Context, tx *repository.Repository, row *oamodel.Announcement) ([]int64, error) {
	if row.AudienceType == oamodel.AudienceTypeAll {
		all, err := s.sys.ListActiveUserIDs(ctx)
		if err != nil {
			return nil, err
		}
		return ResolveAudienceUserIDs(true, nil, nil, all), nil
	}
	targets, err := tx.ListAnnouncementTargets(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	var deptIDs, roleIDs []int64
	for _, target := range targets {
		switch target.TargetType {
		case oamodel.AnnouncementTargetDept:
			deptIDs = append(deptIDs, target.TargetID)
		case oamodel.AnnouncementTargetRole:
			roleIDs = append(roleIDs, target.TargetID)
		}
	}
	var deptUsers []int64
	for _, deptID := range deptIDs {
		children, err := s.sys.DeptWithChildren(ctx, deptID)
		if err != nil {
			return nil, err
		}
		users, err := s.sys.ListUserIDsByDeptIDs(ctx, children)
		if err != nil {
			return nil, err
		}
		deptUsers = append(deptUsers, users...)
	}
	var roleUsers []int64
	for _, roleID := range roleIDs {
		users, err := s.sys.ListActiveUserIDsByRole(ctx, roleID)
		if err != nil {
			return nil, err
		}
		roleUsers = append(roleUsers, users...)
	}
	return ResolveAudienceUserIDs(false, deptUsers, roleUsers, nil), nil
}

// buildAnnouncementNotifies 为每个收件人物化一条 announcement 通知
// （spec §4.7：发布时在事务内为受众批量写通知；通知标题列 128 字截断）。
func buildAnnouncementNotifies(row *oamodel.Announcement, userIDs []int64) []notifyservice.Input {
	inputs := make([]notifyservice.Input, 0, len(userIDs))
	title := text.TruncateRunes("新公告："+row.Title, announcementNotifyTitleLen)
	content := text.TruncateRunes(row.Title, 500)
	routeParams := fmt.Sprintf(`{"id":%d}`, row.ID)
	for _, userID := range userIDs {
		inputs = append(inputs, notifyservice.Input{
			UserID:      userID,
			Type:        notifymodel.TypeAnnouncement,
			Title:       title,
			Content:     content,
			BizType:     AnnouncementFileBizType,
			BizID:       row.ID,
			Route:       "/oa/announcement-mine", // 菜单 532 的员工侧路由（OA 根 /oa + announcement-mine）
			RouteParams: routeParams,
		})
	}
	return inputs
}

// ---------- 附件 ----------

// ListAnnouncementFiles 公告附件列表（可见用户或 oa:announcement:query）。
func (s *Service) ListAnnouncementFiles(ctx context.Context, id int64) ([]dto.AnnouncementFileResp, error) {
	if _, _, err := s.findAccessibleAnnouncement(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.announcementFileStore()
	if err != nil {
		return nil, err
	}
	files, err := store.ListFiles(ctx, AnnouncementFileBizType, id)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].URL = s.announcementFileURL(id, files[i].ID)
	}
	return files, nil
}

// UploadAnnouncementFile 上传公告附件（仅草稿；权限码在路由层）。
func (s *Service) UploadAnnouncementFile(ctx context.Context, id int64, fh *multipart.FileHeader) (*dto.AnnouncementFileResp, error) {
	row, err := s.repo.FindAnnouncement(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("公告不存在")
	}
	if !CanEditAnnouncement(row.Status) {
		return nil, xerr.Conflict("公告已发布或已撤回，不能修改附件")
	}
	if fh == nil {
		return nil, xerr.BadRequest("请选择要上传的文件")
	}
	store, err := s.announcementFileStore()
	if err != nil {
		return nil, err
	}
	file, err := store.UploadFile(ctx, AnnouncementFileBizType, id, fh)
	if err != nil {
		return nil, err
	}
	file.URL = s.announcementFileURL(id, file.ID)
	return &file, nil
}

// DownloadAnnouncementFile 打开公告附件供鉴权下载：公告可见（可见用户或
// 管理查询码）且文件属于该公告（跨公告 fileId 由存储层归属过滤返回 404）。
func (s *Service) DownloadAnnouncementFile(ctx context.Context, id, fileID int64) (*dto.AnnouncementFileContent, error) {
	if _, _, err := s.findAccessibleAnnouncement(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.announcementFileStore()
	if err != nil {
		return nil, err
	}
	content, err := store.OpenFile(ctx, AnnouncementFileBizType, id, fileID)
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

// DeleteAnnouncementFile 删除公告附件（仅草稿；权限码在路由层）。
func (s *Service) DeleteAnnouncementFile(ctx context.Context, id, fileID int64) error {
	row, err := s.repo.FindAnnouncement(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("公告不存在")
	}
	if !CanEditAnnouncement(row.Status) {
		return xerr.Conflict("公告已发布或已撤回，不能修改附件")
	}
	store, err := s.announcementFileStore()
	if err != nil {
		return err
	}
	return store.DeleteFile(ctx, AnnouncementFileBizType, id, fileID)
}

// ---------- 内部辅助 ----------

// findAccessibleAnnouncement 读取并鉴权公告：已发布/已撤回公告对命中受众的
// 登录用户可见（撤回后详情仍显示「已撤回」，spec §4.7）；草稿仅
// oa:announcement:query 持有者可预览。不可见按 404 处理（不泄露存在性）。
// 返回值第二项表示是否管理端（持查询码）。
func (s *Service) findAccessibleAnnouncement(ctx context.Context, id int64) (*oamodel.Announcement, bool, error) {
	row, err := s.repo.FindAnnouncement(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		return nil, false, xerr.NotFound("公告不存在")
	}
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, false, xerr.Unauthorized("")
	}
	canManage, err := s.sys.UserHasPermission(ctx, userID, AnnouncementPermissionQuery)
	if err != nil {
		return nil, false, err
	}
	if row.Status == oamodel.AnnouncementStatusDraft {
		if !canManage {
			return nil, false, xerr.NotFound("公告不存在")
		}
		return row, true, nil
	}
	if canManage {
		return row, true, nil
	}
	targets, err := s.repo.ListAnnouncementTargets(ctx, id)
	if err != nil {
		return nil, false, err
	}
	audience := audienceOfTargets(targets, row.AudienceType)
	viewer, err := s.audienceViewerOf(ctx)
	if err != nil {
		return nil, false, err
	}
	if !audience.Permits(AudienceViewer{UserID: userID, DeptIDs: viewer.DeptIDs, RoleIDs: viewer.RoleIDs}) {
		return nil, false, xerr.NotFound("公告不存在")
	}
	return row, false, nil
}

// audienceViewerOf 构造查看者的受众判定输入：部门祖先链（含自身，向上
// 最多 64 层防环）与角色集合。
func (s *Service) audienceViewerOf(ctx context.Context) (repository.AudienceViewer, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return repository.AudienceViewer{}, xerr.Unauthorized("")
	}
	deptID, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return repository.AudienceViewer{}, err
	}
	var deptIDs []int64
	for depth := 0; ok && deptID > 0 && depth < 64; depth++ {
		deptIDs = append(deptIDs, deptID)
		parent, has, err := s.sys.DeptParentID(ctx, deptID)
		if err != nil {
			return repository.AudienceViewer{}, err
		}
		deptID, ok = parent, has
	}
	roleIDs, err := s.sys.UserRoleIDs(ctx, userID)
	if err != nil {
		return repository.AudienceViewer{}, err
	}
	return repository.AudienceViewer{UserID: userID, DeptIDs: deptIDs, RoleIDs: roleIDs}, nil
}

// announcementDetailOf 组装详情（受众目标附部门/角色名称）；read 是当前
// 查看者对公告的已读状态（详情打开即写已读，issue #49）。
func (s *Service) announcementDetailOf(ctx context.Context, row *oamodel.Announcement, targets []oamodel.AnnouncementTarget, read bool) (*dto.AnnouncementDetailResp, error) {
	resp := &dto.AnnouncementDetailResp{
		AnnouncementItemResp: announcementItemOf(row, read),
		Content:              row.Content,
		Targets:              make([]dto.AnnouncementTargetResp, 0, len(targets)),
	}
	deptIDs := make([]int64, 0, len(targets))
	roleIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		if target.TargetType == oamodel.AnnouncementTargetDept {
			deptIDs = append(deptIDs, target.TargetID)
		} else {
			roleIDs = append(roleIDs, target.TargetID)
		}
	}
	var deptNames, roleNames map[int64]string
	var err error
	if len(deptIDs) > 0 {
		if deptNames, err = s.sys.DeptNames(ctx, deptIDs); err != nil {
			return nil, err
		}
	}
	if len(roleIDs) > 0 {
		if roleNames, err = s.sys.RoleNames(ctx, roleIDs); err != nil {
			return nil, err
		}
	}
	for _, target := range targets {
		name := ""
		if target.TargetType == oamodel.AnnouncementTargetDept {
			name = deptNames[target.TargetID]
		} else {
			name = roleNames[target.TargetID]
		}
		resp.Targets = append(resp.Targets, dto.AnnouncementTargetResp{
			TargetType: target.TargetType,
			TargetID:   target.TargetID,
			TargetName: name,
		})
	}
	return resp, nil
}

// announcementItemOf 把模型映射成列表行视图；read 是当前查看者的已读状态，
// 未读标记由纯函数 AnnouncementUnread 统一判定（issue #49）。
func announcementItemOf(row *oamodel.Announcement, read bool) dto.AnnouncementItemResp {
	return dto.AnnouncementItemResp{
		ID:           row.ID,
		Title:        row.Title,
		Status:       row.Status,
		AudienceType: row.AudienceType,
		Pinned:       row.Pinned,
		Read:         read,
		Unread:       AnnouncementUnread(row.Status, read),
		PublishedAt:  row.PublishedAt,
		WithdrawnAt:  row.WithdrawnAt,
		CreateTime:   row.CreatedAt,
		UpdateTime:   row.UpdatedAt,
	}
}

// announcementReadStateOf 读取当前用户对公告的已读状态（创建/编辑响应回显
// 与详情状态保持一致；无登录主体时返回 false）。
func (s *Service) announcementReadStateOf(ctx context.Context, announcementID int64) (bool, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return false, nil
	}
	return s.repo.HasAnnouncementRead(ctx, announcementID, userID)
}

// audienceOfTargets 把受众行还原为纯函数入参（全员时忽略目标）。
func audienceOfTargets(targets []oamodel.AnnouncementTarget, audienceType int8) Audience {
	if audienceType == oamodel.AudienceTypeAll {
		return Audience{All: true}
	}
	audience := Audience{}
	for _, target := range targets {
		switch target.TargetType {
		case oamodel.AnnouncementTargetDept:
			audience.DeptIDs = append(audience.DeptIDs, target.TargetID)
		case oamodel.AnnouncementTargetRole:
			audience.RoleIDs = append(audience.RoleIDs, target.TargetID)
		}
	}
	return audience
}

// validateAnnouncementFields 校验标题/正文/受众类型，返回清洗后的正文。
func validateAnnouncementFields(title, content string, audienceType int8) (string, string, int8, error) {
	title = strings.TrimSpace(title)
	if n := len([]rune(title)); n == 0 || n > MaxAnnouncementTitleLen {
		return "", "", 0, xerr.BadRequest(fmt.Sprintf("公告标题不能为空且不超过 %d 字", MaxAnnouncementTitleLen))
	}
	raw := strings.TrimSpace(content)
	if n := len([]rune(raw)); n == 0 || n > MaxAnnouncementContentLen {
		return "", "", 0, xerr.BadRequest(fmt.Sprintf("公告正文不能为空且不超过 %d 字符", MaxAnnouncementContentLen))
	}
	cleaned := strings.TrimSpace(announcementHTMLPolicy.Sanitize(raw))
	if cleaned == "" {
		return "", "", 0, xerr.BadRequest("公告正文清洗后为空，请填写有效内容")
	}
	if !oamodel.ValidAudienceType(audienceType) {
		return "", "", 0, xerr.BadRequest("受众类型取值应为 0 全员 / 1 自定义")
	}
	return title, cleaned, audienceType, nil
}

// buildAnnouncementTargets 校验受众目标：自定义受众必须至少选择一项；
// 目标类型/ID 合法、按 (类型, ID) 去重、数量受限；全员受众忽略目标。
func buildAnnouncementTargets(announcementID int64, audienceType int8, reqs []dto.AnnouncementTargetReq) ([]oamodel.AnnouncementTarget, error) {
	if audienceType != oamodel.AudienceTypeCustom {
		if len(reqs) > 0 {
			return nil, xerr.BadRequest("全员受众不能指定部门或角色")
		}
		return nil, nil
	}
	if len(reqs) == 0 {
		return nil, xerr.BadRequest("自定义受众至少选择一个部门或角色")
	}
	if len(reqs) > MaxAnnouncementTargets {
		return nil, xerr.BadRequest(fmt.Sprintf("受众目标不能超过 %d 个", MaxAnnouncementTargets))
	}
	seen := make(map[string]struct{}, len(reqs))
	out := make([]oamodel.AnnouncementTarget, 0, len(reqs))
	for _, req := range reqs {
		if !oamodel.ValidAnnouncementTargetType(req.TargetType) {
			return nil, xerr.BadRequest("受众目标类型取值应为 1 部门 / 2 角色")
		}
		if req.TargetID <= 0 {
			return nil, xerr.BadRequest("受众目标 ID 必须大于 0")
		}
		key := fmt.Sprintf("%d:%d", req.TargetType, req.TargetID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, oamodel.AnnouncementTarget{
			AnnouncementID: announcementID,
			TargetType:     req.TargetType,
			TargetID:       req.TargetID,
		})
	}
	return out, nil
}

// announcementFileStore 返回已装配的附件存储；未装配时显式报错。
func (s *Service) announcementFileStore() (AnnouncementFileStore, error) {
	if s.announcementFiles == nil {
		return nil, xerr.Internal(errors.New("公告附件存储未装配"))
	}
	return s.announcementFiles, nil
}

// announcementFileURL 返回公告附件的鉴权下载地址（GET）：不暴露 /files/ 公共直链。
func (s *Service) announcementFileURL(announcementID, fileID int64) string {
	prefix := s.apiPrefix
	if prefix == "" {
		prefix = defaultAPIPrefix
	}
	return fmt.Sprintf("%s/oa/announcement/%d/files/%d/download",
		strings.TrimRight(prefix, "/"), announcementID, fileID)
}
