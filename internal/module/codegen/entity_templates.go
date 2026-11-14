package codegen

import (
	"bytes"
	"strings"
	"text/template"
)

// 本文件是 owner-scoped 实体脚手架的全部模板（文本模板，[[ ]] 分隔符）。
//
// 书写约定：
//   - Go 结构标签、TS 模板字符串、Markdown 代码围栏里的反引号统一用 ~ 占位，
//     mustRenderEntity 渲染后还原为 `，避免在 Go 源码里拼接原始字符串；
//   - 每个产物注明「由 codegen 生成」；生成的 per-entity 文件不写 package doc
//     （package doc 属于模块骨架：crm 手写样板或 ModuleScaffold 产物）；
//   - 语义对齐 crm 手写样板：owner-scoped 列表套数据范围谓词、详情/改/删
//     scope.Permits 范围外 404、租户插件管 tenant_id、错误文案中文可读。

const entityGenMark = "本文件由 codegen 生成（issue #10 样板）"

// mustRenderEntity 渲染实体模板：[[ ]] 分隔符，~ 还原为反引号。
func mustRenderEntity(tmpl string, data *entityData) string {
	t := template.Must(template.New("entity").Delims("[[", "]]").Parse(tmpl))
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(err)
	}
	return strings.ReplaceAll(buf.String(), "~", "`")
}

// ---------- 模型层 ----------

const tmplEntityModel = `package model

import (
[[if .NeedTime]]	"time"

[[end]]	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
[[if .NeedMoney]]	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
[[end]])
[[range $f := .EnumFields]]
// [[ $.Comment ]][[ $f.Comment ]]枚举（[[ $f.EnumConstPrefix ]]，生成自字段元信息：[[ $f.Comment ]]）。
const (
[[range $v := $f.Enum.Values]]	[[ $f.EnumConstPrefix ]][[ $v.Name ]] int8 = [[ $v.Value ]]
[[end]])

// Valid[[ $f.EnumConstPrefix ]] 报告[[ $.Comment ]][[ $f.Comment ]]枚举是否合法。
func Valid[[ $f.EnumConstPrefix ]](v int8) bool {
	return v >= [[ $f.EnumMinConst ]] && v <= [[ $f.EnumMaxConst ]]
}
[[end]]
// [[ .ClassName ]] 是[[ .Comment ]]（[[ .GenMark ]]，接入步骤见生成包 INTEGRATION.md）。
[[if .OwnerScoped]]// owner-scoped：owner_dept_id 是负责人部门的快照，随 owner 写入同步（ADR 0004）。
[[end]]type [[ .ClassName ]] struct {
	model.BaseEntity
	TenantID int64 ~gorm:"index;not null;default:1" json:"tenantId"~
[[if .HasSequence]]	[[ .NoGoName ]] string ~gorm:"size:32;not null" json:"[[ .NoJSON ]]"~
[[end]][[range .Fields]]	[[ .GoName ]] [[ .GoType ]] ~[[ .ModelTag ]]~
[[end]][[if .OwnerScoped]]	// OwnerUserID 为空（0）表示无负责人；快照列同理，无负责人时不落部门。
	OwnerUserID int64 ~gorm:"index" json:"ownerUserId"~
	OwnerDeptID int64 ~gorm:"index" json:"ownerDeptId"~

	// 非持久化字段：负责人昵称与部门名称，由 service 层批量补齐供展示。
	OwnerUserName string ~gorm:"-" json:"ownerUserName,omitempty"~
	OwnerDeptName string ~gorm:"-" json:"ownerDeptName,omitempty"~
[[end]]}

func ([[ .ClassName ]]) TableName() string { return "[[ .TableName ]]" }
`

// ---------- DTO 层 ----------

const tmplEntityDTO = `package dto

[[if .DTONeedMoney]]import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

[[end]]// ---------- [[ .Comment ]]（[[ .GenMark ]]，契约与 crm 客户/联系人同款）----------
[[range .EnumFields]]// [[ $.Comment ]][[ .Comment ]]枚举：[[ .EnumOptionsExpr ]]。
[[end]]// [[ .ClassName ]]CreateReq 新建[[ .Comment ]]。[[ if .HasSequence ]][[ .NoGoName ]] 由服务端发放，客户端不可指定。[[ end ]][[ if .OwnerScoped ]]ownerUserId 缺省当前登录用户（负责人快照随写落库）。[[ end ]][[ if .DTONeedMoney ]]金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。[[ end ]]
type [[ .ClassName ]]CreateReq struct {
[[range .Fields]]	[[ .GoName ]] [[ .DTOGoType ]] ~[[ .DTOTag ]]~
[[end]][[if .OwnerScoped]]	OwnerUserID int64 ~json:"ownerUserId"~
[[end]]}

// [[ .ClassName ]]UpdateReq 修改[[ .Comment ]]。[[ if .OwnerScoped ]]ownerUserId 传 0 表示保持现负责人不变。[[ end ]][[ if .HasSequence ]]编号不可改（无该字段）。[[ end ]]
type [[ .ClassName ]]UpdateReq struct {
	ID int64 ~json:"id" binding:"required"~
[[range .Fields]]	[[ .GoName ]] [[ .DTOGoType ]] ~[[ .DTOTag ]]~
[[end]][[if .OwnerScoped]]	OwnerUserID int64 ~json:"ownerUserId"~
[[end]]}

// [[ .ClassName ]]PageReq [[ .Comment ]]分页筛选[[ if .EnumFields ]]：枚举传非法值直接 400，绝不静默当成「无此筛选」[[ end ]]。
type [[ .ClassName ]]PageReq struct {
	PageNo   int ~form:"pageNo"~
	PageSize int ~form:"pageSize"~
[[range .SearchFields]]	[[ .GoName ]] string ~form:"[[ .JSONName ]]"~
[[end]][[range .SelectFields]][[ if .IsEnum]]	[[ .GoName ]] *int8 ~form:"[[ .JSONName ]]"~
[[else]]	[[ .GoName ]] string ~form:"[[ .JSONName ]]"~
[[end]][[end]][[if .OwnerScoped]]	OwnerUserID int64 ~form:"ownerUserId"~
	OwnerDeptID int64 ~form:"ownerDeptId"~
[[end]]}
`

// ---------- 仓储层 ----------

const tmplEntityRepository = `package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/model"
)

// ---------- [[ .Comment ]]（[[ .GenMark ]]，接入步骤见生成包 INTEGRATION.md）----------
[[if .OwnerScoped]]
// [[ .ClassName ]]Filter 是[[ .Comment ]]列表查询条件。
// OwnerDeptIDs 与 SelfOwnerUserID 共同表达数据范围谓词（internal/pkg/datascope.Filter
// 到列 owner_dept_id / owner_user_id 的翻译，与 system 模块 UserFilter 同构）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
[[else]]
// [[ .ClassName ]]Filter 是[[ .Comment ]]列表查询条件。
[[end]]type [[ .ClassName ]]Filter struct {
[[range .SearchFields]]	[[ .GoName ]] string
[[end]][[range .SelectFields]][[ if .IsEnum]]	[[ .GoName ]] *int8
[[else]]	[[ .GoName ]] string
[[end]][[end]][[if .OwnerScoped]]
	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
[[end]]}

// Find[[ .ClassName ]]ByID 按 ID 查[[ .Comment ]]（租户插件自动过滤），不存在返回 nil。
func (r *Repository) Find[[ .ClassName ]]ByID(ctx context.Context, id int64) (*model.[[ .ClassName ]], error) {
	var row model.[[ .ClassName ]]
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// List[[ .ClassName ]]s 按筛选[[ if .OwnerScoped ]]与数据范围谓词[[ end ]]分页查询[[ .Comment ]]。
func (r *Repository) List[[ .ClassName ]]s(ctx context.Context, f [[ .ClassName ]]Filter, offset, limit int) ([]model.[[ .ClassName ]], int64, error) {
	q := r.db.WithContext(ctx).Model(&model.[[ .ClassName ]]{})
[[range .SearchFields]]	if f.[[ .GoName ]] != "" {
		q = q.Where("[[ .Name ]] LIKE ?", "%"+f.[[ .GoName ]]+"%")
	}
[[end]][[range .SelectFields]][[ if .IsEnum]]	if f.[[ .GoName ]] != nil {
		q = q.Where("[[ .Name ]] = ?", *f.[[ .GoName ]])
	}
[[else]]	if f.[[ .GoName ]] != "" {
		q = q.Where("[[ .Name ]] = ?", f.[[ .GoName ]])
	}
[[end]][[end]][[if .OwnerScoped]]	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(owner_dept_id IN ? OR owner_user_id = ?)", f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
	}
[[end]]
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.[[ .ClassName ]]
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create[[ .ClassName ]] 创建[[ .Comment ]]。[[ if .HasSequence ]]必须已在[[ .NoGoName ]]赋值后调用（见 service.Create[[ .ClassName ]]）。[[ end ]]
func (r *Repository) Create[[ .ClassName ]](ctx context.Context, row *model.[[ .ClassName ]]) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// Save[[ .ClassName ]] 全量保存[[ .Comment ]]（更新路径）[[ if .HasSequence ]]，编号列由 service 层保证不改写[[ end ]]。
func (r *Repository) Save[[ .ClassName ]](ctx context.Context, row *model.[[ .ClassName ]]) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// Delete[[ .ClassName ]] 按 ID 软删除[[ .Comment ]]（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) Delete[[ .ClassName ]](ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.[[ .ClassName ]]{}, id).Error
}
`

// ---------- 服务层 ----------

const tmplEntityService = `package service

import (
	"context"
[[if .HasSequence]]	"fmt"
[[end]][[if .HasTime]]	"strings"
[[end]][[if or .HasSequence .HasTime]]	"time"
[[end]]
	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/repository"
[[if .OwnerScoped]]	"github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
[[end]]	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- [[ .Comment ]]（[[ .GenMark ]]，接入步骤见生成包 INTEGRATION.md）----------
[[if .OwnerScoped]]
// owner-scoped：列表套数据范围谓词（owner_dept_id / owner_user_id，ADR 0004），
// 详情/改/删 scope.Permits，范围外一律 404——与列表可见性一致（看不见的即不存在）。
[[end]][[if .HasSequence]]
// BizType[[ .ClassName ]] 是[[ .Comment ]]编号在 crm_sequence 中的 biz_type。
const BizType[[ .ClassName ]] = "[[ .BizType ]]"

// Format[[ .ClassName ]]No 格式化[[ .Comment ]]编号：[[ .SeqPrefix ]]+yyyyMMdd+4 位序号，租户内唯一。
func Format[[ .ClassName ]]No(dateKey string, seq int64) string {
	return "[[ .SeqPrefix ]]" + dateKey + fmt.Sprintf("%04d", seq)
}

[[end]]// List[[ .ClassName ]]s 分页查询[[ .Comment ]][[ if .OwnerScoped ]]，按数据范围谓词过滤[[ end ]]。
func (s *Service) List[[ .ClassName ]]s(ctx context.Context, req *dto.[[ .ClassName ]]PageReq) ([]model.[[ .ClassName ]], int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
[[range .EnumFields]]	if req.[[ .GoName ]] != nil {
		if err := Validate[[ $.ClassName ]][[ .GoName ]](*req.[[ .GoName ]]); err != nil {
			return nil, 0, err
		}
	}
[[end]]
	filter := repository.[[ .ClassName ]]Filter{
[[range .SearchFields]]		[[ .GoName ]]: req.[[ .GoName ]],
[[end]][[range .SelectFields]]		[[ .GoName ]]: req.[[ .GoName ]],
[[end]]	}
[[if .OwnerScoped]]
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	apply[[ .ClassName ]]Scope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
[[end]]
	list, total, err := s.repo.List[[ .ClassName ]]s(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
[[if .OwnerScoped]]	s.fill[[ .ClassName ]]OwnerNames(ctx, list)
[[end]]	return list, total, nil
}

// Get[[ .ClassName ]] 查询[[ .Comment ]]详情。[[ if .OwnerScoped ]]数据范围外的[[ .Comment ]]一律 404：
// 与列表可见性保持一致——看不见的即不存在，不泄露存在性。[[ end ]]
func (s *Service) Get[[ .ClassName ]](ctx context.Context, id int64) (*model.[[ .ClassName ]], error) {
	row, err := s.findVisible[[ .ClassName ]](ctx, id)
	if err != nil {
		return nil, err
	}
[[if .OwnerScoped]]	s.fill[[ .ClassName ]]OwnerName(ctx, row)
[[end]]	return row, nil
}

// Create[[ .ClassName ]] 创建[[ .Comment ]]。[[ if .OwnerScoped ]]负责人缺省当前登录用户，部门快照随负责人写入（ADR 0004）。[[ end ]][[ if .HasSequence ]]编号与落库同事务（失败回滚、不消耗序号）。[[ end ]]
func (s *Service) Create[[ .ClassName ]](ctx context.Context, req *dto.[[ .ClassName ]]CreateReq) (*model.[[ .ClassName ]], error) {
[[range .EnumFields]]	[[ .GoName ]] := [[ if .HasDefault ]]int8([[ .DefaultInt ]])[[ else ]][[ .EnumMinConst ]][[end]]
	if req.[[ .GoName ]] != nil {
		if err := Validate[[ $.ClassName ]][[ .GoName ]](*req.[[ .GoName ]]); err != nil {
			return nil, err
		}
		[[ .GoName ]] = *req.[[ .GoName ]]
	}
[[end]][[if .OwnerScoped]]
	ownerID := req.OwnerUserID
	if ownerID == 0 {
		// 无负责人缺省归属创建人，避免「建完即不可见」的空窗。
		ownerID = CurrentUserID(ctx)
	}
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
[[end]][[if .HasSequence]]
	now := time.Now()
	dateKey := DateKey(now)
[[end]]
	row := &model.[[ .ClassName ]]{
[[range .Fields]]		[[ .GoName ]]: [[ .ServiceAssign ]],
[[end]][[if .OwnerScoped]]		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
[[end]]	}
[[if .HasSequence]][[if .OwnerScoped]]	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
[[else]]	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
[[end]]		seq, err := tx.NextSequenceValue(ctx, currentTenantID(ctx), BizType[[ .ClassName ]], dateKey)
		if err != nil {
			return err
		}
		row.[[ .NoGoName ]] = Format[[ .ClassName ]]No(dateKey, seq)
		return tx.Create[[ .ClassName ]](ctx, row)
	})
	if err != nil {
		return nil, err
	}
[[else]]	if err := s.repo.Create[[ .ClassName ]](ctx, row); err != nil {
		return nil, err
	}
[[end]][[if .OwnerScoped]]	s.fill[[ .ClassName ]]OwnerName(ctx, row)
[[end]]	return row, nil
}

// Update[[ .ClassName ]] 修改[[ .Comment ]]：数据范围外一律 404[[ if .HasSequence ]]；编号不可改[[ end ]]；[[ if .OwnerScoped ]]ownerUserId 传 0 保持现负责人，传非 0 值重落部门快照。[[ else ]]字段全量覆盖。[[ end ]]
func (s *Service) Update[[ .ClassName ]](ctx context.Context, req *dto.[[ .ClassName ]]UpdateReq) error {
	row, err := s.findVisible[[ .ClassName ]](ctx, req.ID)
	if err != nil {
		return err
	}
[[range .EnumFields]]	if req.[[ .GoName ]] != nil {
		if err := Validate[[ $.ClassName ]][[ .GoName ]](*req.[[ .GoName ]]); err != nil {
			return err
		}
		row.[[ .GoName ]] = *req.[[ .GoName ]]
	}
[[end]][[if .OwnerScoped]]	if req.OwnerUserID > 0 && req.OwnerUserID != row.OwnerUserID {
		deptID, err := s.ownerDeptSnapshot(ctx, req.OwnerUserID)
		if err != nil {
			return err
		}
		row.OwnerUserID = req.OwnerUserID
		row.OwnerDeptID = deptID
	}
[[end]][[range .Fields]][[ if not .IsEnum]]	row.[[ .GoName ]] = [[ .ServiceAssign ]]
[[end]][[end]]	return s.repo.Save[[ .ClassName ]](ctx, row)
}

// Delete[[ .ClassName ]] 软删除[[ .Comment ]]。[[ if .OwnerScoped ]]数据范围外一律 404。[[ end ]]
func (s *Service) Delete[[ .ClassName ]](ctx context.Context, id int64) error {
	row, err := s.findVisible[[ .ClassName ]](ctx, id)
	if err != nil {
		return err
	}
	return s.repo.Delete[[ .ClassName ]](ctx, row.ID)
}

// findVisible[[ .ClassName ]] 取[[ .Comment ]]并做可见性校验：不存在[[ if .OwnerScoped ]]或范围外[[end]]都返回 404。
func (s *Service) findVisible[[ .ClassName ]](ctx context.Context, id int64) (*model.[[ .ClassName ]], error) {
	row, err := s.repo.Find[[ .ClassName ]]ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("[[ .Comment ]]不存在")
	}
[[if .OwnerScoped]]
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, err
	}
	if !scope.Permits(row.OwnerDeptID, row.OwnerUserID) {
		return nil, xerr.NotFound("[[ .Comment ]]不存在")
	}
[[end]]	return row, nil
}
[[if .OwnerScoped]]
// apply[[ .ClassName ]]Scope 把 datascope.Filter 翻译成 [[ .Entity ]] 的 owner_* 谓词：
// 空切片 + 无 self = 恒假（fail-closed，绝不回退全部）。
func apply[[ .ClassName ]]Scope(scope datascope.Filter, ownerDeptIDs *[]int64, selfOwnerUserID *int64) {
	if scope.All {
		return
	}
	if scope.IncludeSelf {
		*selfOwnerUserID = scope.UserID
	}
	switch {
	case len(scope.DeptIDs) > 0:
		*ownerDeptIDs = scope.DeptIDs
	case !scope.IncludeSelf:
		*ownerDeptIDs = []int64{}
	}
}

// fill[[ .ClassName ]]OwnerNames 批量补齐负责人昵称与部门名称，供列表展示。
// 必须按下标回写：for range 的元素是副本，改副本不影响调用方的切片。
func (s *Service) fill[[ .ClassName ]]OwnerNames(ctx context.Context, list []model.[[ .ClassName ]]) {
	userIDs, deptIDs := collect[[ .ClassName ]]OwnerIDs(list)
	names, deptNames := s.fetch[[ .ClassName ]]OwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].OwnerUserName = names[list[i].OwnerUserID]
		list[i].OwnerDeptName = deptNames[list[i].OwnerDeptID]
	}
}

// fill[[ .ClassName ]]OwnerName 补齐单个[[ .Comment ]]的负责人昵称与部门名称（创建/详情返回值用）。
func (s *Service) fill[[ .ClassName ]]OwnerName(ctx context.Context, row *model.[[ .ClassName ]]) {
	userIDs, deptIDs := collect[[ .ClassName ]]OwnerIDs([]model.[[ .ClassName ]]{*row})
	names, deptNames := s.fetch[[ .ClassName ]]OwnerNames(ctx, userIDs, deptIDs)
	row.OwnerUserName = names[row.OwnerUserID]
	row.OwnerDeptName = deptNames[row.OwnerDeptID]
}

// collect[[ .ClassName ]]OwnerIDs 收集非零的负责人用户 ID 与部门 ID（快照列为 0 表示无归属）。
func collect[[ .ClassName ]]OwnerIDs(list []model.[[ .ClassName ]]) (userIDs, deptIDs []int64) {
	userIDs = make([]int64, 0, len(list))
	deptIDs = make([]int64, 0, len(list))
	for _, row := range list {
		if row.OwnerUserID > 0 {
			userIDs = append(userIDs, row.OwnerUserID)
		}
		if row.OwnerDeptID > 0 {
			deptIDs = append(deptIDs, row.OwnerDeptID)
		}
	}
	return userIDs, deptIDs
}

// fetch[[ .ClassName ]]OwnerNames 批量拉取名称映射；网关查询失败时降级为空映射（缺名称不阻塞查询）。
func (s *Service) fetch[[ .ClassName ]]OwnerNames(ctx context.Context, userIDs, deptIDs []int64) (names, deptNames map[int64]string) {
	names = map[int64]string{}
	if len(userIDs) > 0 {
		if m, err := s.sys.UserNicknames(ctx, userIDs); err == nil {
			names = m
		}
	}
	deptNames = map[int64]string{}
	if len(deptIDs) > 0 {
		if m, err := s.sys.DeptNames(ctx, deptIDs); err == nil {
			deptNames = m
		}
	}
	return names, deptNames
}
[[end]][[if .HasTime]]
// parse[[ .ClassName ]]Time 把前端提交的时间字符串解析为可空时间：空串返回 nil；
// 格式非法返回 nil（选填时间字段按无值处理）；按业务时区解释（ADR 0006）。
// 业务时区由 app 装配时 Service.SetLocation 注入，未注入才退回 time.Local。
func (s *Service) parse[[ .ClassName ]]Time(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, value, s.businessLocation()); err == nil {
			return &t
		}
	}
	return nil
}
[[end]][[if .ImportExport]]
// 导入导出接线位（生成器只留位，不产出实现）：
// 接入时参考 internal/module/crm 的手写实现与 internal/pkg/excel（docs/crm-mvp-spec.md §7）——
//   - 导入：模板下载 + 先全量校验（行号+原因可读），有错整批拒绝；上限 5000 行/10MB；
//   - 导出：复用 List[[ .ClassName ]]s 的筛选与数据范围谓词，导出内容与权限一致。
[[end]]`

// ---------- 枚举校验（service 纯函数）与测试骨架 ----------

const tmplEntityRules = `package service

import (
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- [[ .Comment ]]枚举校验（[[ .GenMark ]]；非法值一律 400，文案说清字段与期望）----------
[[range .EnumFields]]
// Validate[[ $.ClassName ]][[ .GoName ]] 校验[[ $.Comment ]][[ .Comment ]]枚举，非法值返回用户可读的 400 错误。
func Validate[[ $.ClassName ]][[ .GoName ]](v int8) error {
	if !model.Valid[[ .EnumConstPrefix ]](v) {
		return xerr.BadRequest(fmt.Sprintf("[[ $.Comment ]][[ .Comment ]] %d 不合法：期望 [[ .EnumOptionsExpr ]]", v))
	}
	return nil
}
[[end]]`

const tmplEntityRulesTest = `package service_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/service"
)

// [[ .Comment ]]枚举校验（[[ .GenMark ]]的测试骨架，跑在 service 纯函数外部测试包）。
// 非法枚举 400 且文案说清字段；可见性等需要数据库的行为由集成测试矩阵覆盖。
[[range .EnumFields]]
func TestValidate[[ $.ClassName ]][[ .GoName ]](t *testing.T) {
	for _, v := range []int8{[[ .EnumValidValues ]]} {
		if err := service.Validate[[ $.ClassName ]][[ .GoName ]](v); err != nil {
			t.Fatalf("[[ .Comment ]] %d 应合法，实际报错 %v", v, err)
		}
	}
	for _, v := range []int8{[[ .EnumInvalidValues ]]} {
		err := service.Validate[[ $.ClassName ]][[ .GoName ]](v)
		if err == nil {
			t.Fatalf("[[ .Comment ]] %d 应被拒绝", v)
		}
		if msg := err.Error(); !strings.Contains(msg, "[[ $.Comment ]][[ .Comment ]]") {
			t.Fatalf("错误文案应说明[[ $.Comment ]][[ .Comment ]]，实际 %q", msg)
		}
	}
}
[[end]][[if .HasSequence]]
// 编号规则（[[ .GenMark ]]的测试骨架）：[[ .SeqPrefix ]]+yyyyMMdd+4 位序号，租户内唯一。
func TestFormat[[ .ClassName ]]No(t *testing.T) {
	cases := []struct {
		name    string
		dateKey string
		seq     int64
		want    string
	}{
		{name: "首号", dateKey: "20261105", seq: 1, want: "[[ .SeqPrefix ]]202611050001"},
		{name: "序号补零到 4 位", dateKey: "20261105", seq: 42, want: "[[ .SeqPrefix ]]202611050042"},
		{name: "序号超过 9999 自然扩位", dateKey: "20261105", seq: 10000, want: "[[ .SeqPrefix ]]2026110510000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.Format[[ .ClassName ]]No(tc.dateKey, tc.seq); got != tc.want {
				t.Fatalf("Format[[ .ClassName ]]No(%q, %d) = %q，期望 %q", tc.dateKey, tc.seq, got, tc.want)
			}
		})
	}
}
[[end]]`

// ---------- API 层 ----------

const tmplEntityAPI = `package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- [[ .Comment ]]（[[ .GenMark ]]，接入步骤见生成包 INTEGRATION.md）----------

// Page[[ .ClassName ]]s [[ .Comment ]]分页（筛选[[ if .OwnerScoped ]] + 数据范围过滤[[ end ]]）。
func (h *Handler) Page[[ .ClassName ]]s(c *gin.Context) {
	var req dto.[[ .ClassName ]]PageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.List[[ .ClassName ]]s(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// Get[[ .ClassName ]] [[ .Comment ]]详情（[[ if .OwnerScoped ]]数据范围外 404[[ else ]]不存在 404[[end]]）。
func (h *Handler) Get[[ .ClassName ]](c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.Get[[ .ClassName ]](c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// Create[[ .ClassName ]] 新建[[ .Comment ]]，返回完整实体。
func (h *Handler) Create[[ .ClassName ]](c *gin.Context) {
	var req dto.[[ .ClassName ]]CreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.Create[[ .ClassName ]](c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// Update[[ .ClassName ]] 修改[[ .Comment ]]。
func (h *Handler) Update[[ .ClassName ]](c *gin.Context) {
	var req dto.[[ .ClassName ]]UpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.Update[[ .ClassName ]](c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// Delete[[ .ClassName ]] 删除[[ .Comment ]]（软删除）。
func (h *Handler) Delete[[ .ClassName ]](c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete[[ .ClassName ]](c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// Register[[ .ClassName ]]Routes 注册[[ .Comment ]]路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（[[ .PermPrefix ]]:*）。
func (h *Handler) Register[[ .ClassName ]]Routes(rg *gin.RouterGroup) {
	[[ .VarName ]] := rg.Group("/[[ .RouteGroup ]]")
	[[ .VarName ]].GET("/page", h.requirePermission("[[ .PermPrefix ]]:query"), h.Page[[ .ClassName ]]s)
	[[ .VarName ]].GET("/:id", h.requirePermission("[[ .PermPrefix ]]:query"), h.Get[[ .ClassName ]])
	[[ .VarName ]].POST("", h.requirePermission("[[ .PermPrefix ]]:create"), h.Create[[ .ClassName ]])
	[[ .VarName ]].PUT("", h.requirePermission("[[ .PermPrefix ]]:update"), h.Update[[ .ClassName ]])
	[[ .VarName ]].DELETE("/:id", h.requirePermission("[[ .PermPrefix ]]:delete"), h.Delete[[ .ClassName ]])
[[if .ImportExport]]
	// 导入导出接线位（生成器只留位，不产出实现）：接线参考 internal/module/crm 的
	// 手写实现与 internal/pkg/excel，并补上对应的 Import/Export handler。
	//	[[ .VarName ]].POST("/import", h.requirePermission("[[ .PermPrefix ]]:import"), h.Import[[ .ClassName ]])
	//	[[ .VarName ]].GET("/export", h.requirePermission("[[ .PermPrefix ]]:export"), h.Export[[ .ClassName ]])
	//	[[ .VarName ]].GET("/import-template", h.requirePermission("[[ .PermPrefix ]]:import"), h.Import[[ .ClassName ]]Template)
[[end]]}
`

// ---------- 模块骨架（ModuleScaffold = true 时生成） ----------

const tmplScaffoldModelDoc = `// Package model 定义 [[ .Module ]] 模块的持久化模型。
// 实体统一嵌入 internal/pkg/model.BaseEntity（时间戳、软删除、creator/updater），
// 表结构由 goose 迁移管理，模型标签仅描述列类型供 GORM 使用。
// （[[ .GenMark ]]的模块骨架；per-entity 文件随后续生成追加。）
package model
`

const tmplScaffoldDTODoc = `// Package dto 定义 [[ .Module ]] 模块的传输对象（请求/响应结构）。
// （[[ .GenMark ]]的模块骨架；per-entity 文件随后续生成追加。）
package dto
`

const tmplScaffoldRepository = `// Package repository 是 [[ .Module ]] 模块的 DAL 层。
// 所有查询/写入通过租户插件自动带 tenant_id 条件（表须登记 internal/pkg/database
// 的 TenantTables），本层不手工拼主表的租户条件；编号发放是唯一例外
// （委托 internal/pkg/sequence 的裸 SQL 原语，租户值显式入参，见 NextSequenceValue）。
// （[[ .GenMark ]]的模块骨架；per-entity 文件随后续生成追加。）
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/sequence"
)

// Repository 是 [[ .Module ]] 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 [[ .Module ]] Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// Transaction 在一个数据库事务里执行 fn，fn 收到绑定同一事务的 Repository。
// 编号发放与落库必须同事务：任一步失败整体回滚，不消耗序号、不留脏行。
func (r *Repository) Transaction(ctx context.Context, fn func(tx *Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}

// NextSequenceValue 在 crm_sequence 的 (tenant_id, biz_type, date_key) 上原子发放下一个序号
// （薄包装；机制与并发说明见 internal/pkg/sequence，生成实体仍写 CRM 序列表）。
func (r *Repository) NextSequenceValue(ctx context.Context, tenantID int64, bizType, dateKey string) (int64, error) {
	return sequence.Next(ctx, r.db, "crm_sequence", tenantID, bizType, dateKey)
}
`

const tmplScaffoldService = `// Package service 是 [[ .Module ]] 模块的业务层。
// （[[ .GenMark ]]的模块骨架；per-entity 文件随后续生成追加。）
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// SystemGateway 是 [[ .Module ]] 对 system 模块的依赖面（依赖接口而非实现，避免反向耦合）：
// 数据范围解析（internal/pkg/datascope 引擎的宿主）与负责人归属快照所需的用户/部门信息。
// 由 internal/app 装配时注入 system 模块的 Service 实现。
type SystemGateway interface {
	// DataScopeFilter 计算当前用户的数据范围谓词（多角色并集，见 ADR 0004）。
	DataScopeFilter(ctx context.Context, userID int64) (datascope.Filter, error)
	// UserDeptID 返回用户所属部门 ID；用户不存在时 ok=false。
	UserDeptID(ctx context.Context, userID int64) (deptID int64, ok bool, err error)
	// UserNicknames 批量返回用户昵称（列表/详情展示补齐）。
	UserNicknames(ctx context.Context, ids []int64) (map[int64]string, error)
	// DeptNames 批量返回部门名称。
	DeptNames(ctx context.Context, ids []int64) (map[int64]string, error)
}

// Service 汇聚 [[ .Module ]] 模块的业务能力。
type Service struct {
	repo *repository.Repository
	sys  SystemGateway
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入；
	// nil 表示未注入（退回 time.Local，见 businessLocation）。
	location *time.Location
}

// New 创建 [[ .Module ]] Service。
func New(repo *repository.Repository, sys SystemGateway) *Service {
	return &Service{repo: repo, sys: sys}
}

// SetLocation 注入业务时区（ADR 0006：唯一来源是 config.Location()）。nil 忽略。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区；未注入时退回 time.Local（测试/工具场景）。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// CurrentUserID 返回 context 中的当前用户 ID。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}

// currentTenantID 返回 context 中的当前租户 ID；租户中间件保证登录链路必有值。
func currentTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}

// DateKey 返回 yyyyMMdd 日期键（编号按天发放；按调用方 Location 解释）。
func DateKey(at time.Time) string { return at.Format("20060102") }

// ownerDeptSnapshot 解析负责人的部门快照；负责人必须是当前租户内存在的用户。
func (s *Service) ownerDeptSnapshot(ctx context.Context, ownerUserID int64) (int64, error) {
	deptID, ok, err := s.sys.UserDeptID(ctx, ownerUserID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, xerr.BadRequest(fmt.Sprintf("负责人不存在：用户 %d", ownerUserID))
	}
	return deptID, nil
}
`

const tmplScaffoldRouter = `// Package api 是 [[ .Module ]] 模块的 HTTP 入口：路由、参数绑定与统一响应。
// （[[ .GenMark ]]的模块骨架；per-entity 文件随后续生成追加。）
package api

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/[[ .Module ]]/service"
)

// Handler 是 [[ .Module ]] 模块的 HTTP 入口。
// requirePermission 复用 system 模块的权限中间件（crm 同款装配方式），
// 权限码与菜单种子保持一致。
// location 是应用业务时区（ADR 0006），与 crm/oa 同款装配契约
//（api.New(svc, requirePermission, location)）；Excel/日期逻辑接入时直接可用。
type Handler struct {
	svc               *service.Service
	requirePermission func(string) gin.HandlerFunc
	location          *time.Location
}

// New 创建 [[ .Module ]] Handler。location 为 nil 时回退 time.Local
//（生产装配固定传 config.Location()）。
func New(svc *service.Service, requirePermission func(string) gin.HandlerFunc, location *time.Location) *Handler {
	if location == nil {
		location = time.Local
	}
	return &Handler{svc: svc, requirePermission: requirePermission, location: location}
}

// RegisterProtected 注册需要登录的 [[ .Module ]] 接口，rg 上应已挂载 Auth 中间件。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	h.Register[[ .ClassName ]]Routes(rg)
}
`

const tmplScaffoldHelpers = `package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// 通用绑定助手（与 crm/system 模块同构，保持模块自包含）。

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}
`

// ---------- 集成测试骨架 ----------

const tmplEntityIntegrationTest = `//go:build integration

// [[ .Comment ]]集成测试骨架（[[ .GenMark ]]；矩阵模式参照 crm 客户的
// customer_scope_integration_test.go）[[ if .OwnerScoped ]]：5 种数据范围（对齐芋道 1–5，
// ADR 0004）在 owner_dept_id / owner_user_id 列上生效，范围外详情/修改/删除一律 404。[[ else ]]：CRUD 生命周期冒烟。[[end]]
//
// 前置（生成包 INTEGRATION.md 的接线步骤全部完成后才可运行）：
// 迁移 + TenantTables 登记 + 路由注册 + 菜单/权限码种子；
// 测试库经 GAP_TEST_DATABASE_DSN 指向独立库，用 make test-integration 运行。
package [[ .Module ]]_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// [[ .Module ]][[ .ClassName ]]MenuID 是种子中的[[ .Comment ]]菜单 ID（含 [[ .PermPrefix ]]:query 权限码）。
[[if .HasMenu]]// 取值来自实体元信息的 menu 配置，与 seed/menus.append.go 片段一致。
[[else]]// TODO: 接线时把实体元信息补上 menu 配置（或手工改这里）为实际菜单 ID。
[[end]]const [[ .Module ]][[ .ClassName ]]MenuID = int64([[ .MenuID ]])

// [[ .Entity ]]MustOK 断言业务成功（HTTP 200 且 code 0）。
func [[ .Entity ]]MustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// [[ .Entity ]]ScopeCreateUser 用 admin 创建启用用户并绑定角色。
func [[ .Entity ]]ScopeCreateUser(t *testing.T, env *testutil.IntegrationEnv, token, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	[[ .Entity ]]MustOK(t, "创建用户 "+username, status, api)
}

// [[ .Entity ]]ScopeFindUserID 按用户名精确回查用户 ID。
func [[ .Entity ]]ScopeFindUserID(t *testing.T, env *testutil.IntegrationEnv, token, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", token, nil)
	[[ .Entity ]]MustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID       int64  ~json:"id"~
			Username string ~json:"username"~
		} ~json:"list"~
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	for _, u := range page.List {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("用户 %s 精确回查未命中", username)
	return 0
}

// [[ .Entity ]]ScopeCreateRole 用 admin 创建角色（数据范围 + 菜单绑定）并回读 ID。
func [[ .Entity ]]ScopeCreateRole(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", token, payload)
	[[ .Entity ]]MustOK(t, fmt.Sprintf("创建角色 %v", payload["code"]), status, api)

	status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+fmt.Sprint(payload["code"])+"&pageNo=1&pageSize=10", token, nil)
	[[ .Entity ]]MustOK(t, "查询角色 "+fmt.Sprint(payload["code"]), status, api)
	var page struct {
		List []struct {
			ID int64 ~json:"id"~
		} ~json:"list"~
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 {
		t.Fatalf("按编码 %s 查询角色：命中 %d 条，期望唯一", payload["code"], len(page.List))
	}
	return page.List[0].ID
}

// [[ .Entity ]]ScopeTreeNode 是部门树节点（回查测试创建的子部门用）。
type [[ .Entity ]]ScopeTreeNode struct {
	ID       int64                        ~json:"id"~
	Name     string                       ~json:"name"~
	Children [][[ .Entity ]]ScopeTreeNode ~json:"children"~
}

// [[ .Entity ]]ScopeCreateDept 用 admin 在 parentID 下创建子部门并回读 ID。
func [[ .Entity ]]ScopeCreateDept(t *testing.T, env *testutil.IntegrationEnv, token, name string, parentID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/dept", token, map[string]any{
		"name": name, "parentId": parentID,
	})
	[[ .Entity ]]MustOK(t, "创建部门 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/dept/tree", token, nil)
	[[ .Entity ]]MustOK(t, "查询部门树", status, api)
	var tree [][[ .Entity ]]ScopeTreeNode
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析部门树: %v（data=%s）", err, string(api.Data))
	}
	var find func(nodes [][[ .Entity ]]ScopeTreeNode) (int64, bool)
	find = func(nodes [][[ .Entity ]]ScopeTreeNode) (int64, bool) {
		for _, n := range nodes {
			if n.Name == name {
				return n.ID, true
			}
			if id, ok := find(n.Children); ok {
				return id, true
			}
		}
		return 0, false
	}
	if id, ok := find(tree); ok {
		return id
	}
	t.Fatalf("部门树中未找到 %s（父部门 %d）", name, parentID)
	return 0
}

// [[ .Entity ]]ScopeCreate[[ .ClassName ]] 用指定账号创建[[ .Comment ]]（必填字段填占位值，extra 覆盖任意字段）。
func [[ .Entity ]]ScopeCreate[[ .ClassName ]](t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{
		"[[ .NameFieldJSON ]]": name,
[[range .RequiredFillFields]]		"[[ .JSONName ]]": [[ .TestLiteral ]],
[[end]]	}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/[[ .RouteGroup ]]", token, payload)
	[[ .Entity ]]MustOK(t, "创建[[ .Comment ]] "+name, status, api)
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建[[ .Comment ]]响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// [[ .Entity ]]ScopePage[[ .ClassName ]]s 拉取[[ .Comment ]]分页（query 追加在固定分页参数后）。
func [[ .Entity ]]ScopePage[[ .ClassName ]]s(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/[[ .RouteGroup ]]/page?pageNo=1&pageSize=50"+query, token, nil)
	[[ .Entity ]]MustOK(t, "[[ .Comment ]]分页", status, api)
	var page struct {
		List  []map[string]any ~json:"list"~
		Total int64            ~json:"total"~
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析[[ .Comment ]]分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// [[ .Entity ]]ScopeGet[[ .ClassName ]] 查询[[ .Comment ]]详情，返回 HTTP 状态码（404 时不解析响应体）。
func [[ .Entity ]]ScopeGet[[ .ClassName ]](t *testing.T, env *testutil.IntegrationEnv, token string, id int64) int {
	t.Helper()
	status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/[[ .RouteGroup ]]/%d", id), token, nil)
	return status
}
[[if .OwnerScoped]]
// [[ .Entity ]]ScopeVisibleNames 以指定账号登录并拉取[[ .Comment ]]分页，返回可见[[ .Comment ]]名集合。
func [[ .Entity ]]ScopeVisibleNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	list, _ := [[ .Entity ]]ScopePage[[ .ClassName ]]s(t, env, token, "")
	set := make(map[string]bool, len(list))
	for _, row := range list {
		set[row["[[ .NameFieldJSON ]]"].(string)] = true
	}
	return set
}

// [[ .Entity ]]ScopeAssertSet 断言可见[[ .Comment ]]名集合完全匹配期望。
func [[ .Entity ]]ScopeAssertSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：可见集合缺少 %q，实际 %v", phase, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：不应可见 %q，实际 %v", phase, name, got)
		}
	}
}

// [[ .Entity ]]ScopeSetup 搭建标准观察场景（seed 部门 100 总公司 / 101 研发 / 102 市场 / 103 财务）：
//   - 研发一名销售，其[[ .Comment ]]在部门 101；
//   - 市场两名销售（含观察者的同事），其[[ .Comment ]]在部门 102；
//   - 市场子部门「市场二组」一名销售，其[[ .Comment ]]在子部门；
//   - 观察者（观察者角色）在部门 102，自己的[[ .Comment ]]归属本人。
//
// 返回观察者用户名，供各数据范围子测试断言。
func [[ .Entity ]]ScopeSetup(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64) string {
	t.Helper()
	rndDept, mktDept := int64(101), int64(102)
	[[ .Entity ]]ScopeCreateUser(t, env, admin, "owner_rnd_"+roleCode, rndDept, nil)
	[[ .Entity ]]ScopeCreateUser(t, env, admin, "owner_mkt_"+roleCode, mktDept, nil)
	[[ .Entity ]]ScopeCreateUser(t, env, admin, "owner_mkt2_"+roleCode, mktDept, nil)
	subDept := [[ .Entity ]]ScopeCreateDept(t, env, admin, "市场二组_"+roleCode, mktDept)
	[[ .Entity ]]ScopeCreateUser(t, env, admin, "owner_sub_"+roleCode, subDept, nil)

	for _, u := range []string{"owner_rnd_" + roleCode, "owner_mkt_" + roleCode, "owner_mkt2_" + roleCode, "owner_sub_" + roleCode} {
		id := [[ .Entity ]]ScopeFindUserID(t, env, admin, u)
		[[ .Entity ]]ScopeCreate[[ .ClassName ]](t, env, admin, "[[ .Comment ]]_of_"+u, map[string]any{"ownerUserId": id})
	}

	roleID := [[ .Entity ]]ScopeCreateRole(t, env, admin, map[string]any{
		"name": "观察者角色_" + roleCode, "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs,
		"menuIds": []int64{[[ .MenuIDsExpr ]]},
	})
	observer := "observer_" + roleCode
	[[ .Entity ]]ScopeCreateUser(t, env, admin, observer, mktDept, []int64{roleID})
	observerID := [[ .Entity ]]ScopeFindUserID(t, env, admin, observer)
	[[ .Entity ]]ScopeCreate[[ .ClassName ]](t, env, admin, "[[ .Comment ]]_of_"+observer, map[string]any{"ownerUserId": observerID})
	return observer
}

// TestIntegration[[ .ClassName ]]DataScopeMatrix [[ .Comment ]]列表按 5 种数据范围过滤。
func TestIntegration[[ .ClassName ]]DataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_all", 1, nil)
		got := [[ .Entity ]]ScopeVisibleNames(t, env, observer)
		[[ .Entity ]]ScopeAssertSet(t, "全部数据", got, []string{
			"[[ .Comment ]]_of_owner_rnd_[[ .Entity ]]_ds_all",
			"[[ .Comment ]]_of_owner_mkt_[[ .Entity ]]_ds_all",
			"[[ .Comment ]]_of_owner_mkt2_[[ .Entity ]]_ds_all",
			"[[ .Comment ]]_of_owner_sub_[[ .Entity ]]_ds_all",
			"[[ .Comment ]]_of_observer_[[ .Entity ]]_ds_all",
		})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_custom", 2, []int64{101, 103})
		got := [[ .Entity ]]ScopeVisibleNames(t, env, observer)
		// 仅 101、103 的[[ .Comment ]]可见：102（观察者本部门）与 101 的子部门不在集合内。
		[[ .Entity ]]ScopeAssertSet(t, "自定义部门", got, []string{
			"[[ .Comment ]]_of_owner_rnd_[[ .Entity ]]_ds_custom",
		},
			"[[ .Comment ]]_of_owner_mkt_[[ .Entity ]]_ds_custom",
			"[[ .Comment ]]_of_owner_mkt2_[[ .Entity ]]_ds_custom",
			"[[ .Comment ]]_of_owner_sub_[[ .Entity ]]_ds_custom",
			"[[ .Comment ]]_of_observer_[[ .Entity ]]_ds_custom")
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_dept", 3, nil)
		got := [[ .Entity ]]ScopeVisibleNames(t, env, observer)
		[[ .Entity ]]ScopeAssertSet(t, "本部门", got, []string{
			"[[ .Comment ]]_of_owner_mkt_[[ .Entity ]]_ds_dept",
			"[[ .Comment ]]_of_owner_mkt2_[[ .Entity ]]_ds_dept",
			"[[ .Comment ]]_of_observer_[[ .Entity ]]_ds_dept",
		},
			"[[ .Comment ]]_of_owner_rnd_[[ .Entity ]]_ds_dept",
			"[[ .Comment ]]_of_owner_sub_[[ .Entity ]]_ds_dept")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_deptsub", 4, nil)
		got := [[ .Entity ]]ScopeVisibleNames(t, env, observer)
		[[ .Entity ]]ScopeAssertSet(t, "本部门及以下", got, []string{
			"[[ .Comment ]]_of_owner_mkt_[[ .Entity ]]_ds_deptsub",
			"[[ .Comment ]]_of_owner_mkt2_[[ .Entity ]]_ds_deptsub",
			"[[ .Comment ]]_of_owner_sub_[[ .Entity ]]_ds_deptsub",
			"[[ .Comment ]]_of_observer_[[ .Entity ]]_ds_deptsub",
		},
			"[[ .Comment ]]_of_owner_rnd_[[ .Entity ]]_ds_deptsub")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_self", 5, nil)
		got := [[ .Entity ]]ScopeVisibleNames(t, env, observer)
		[[ .Entity ]]ScopeAssertSet(t, "仅本人", got, []string{
			"[[ .Comment ]]_of_observer_[[ .Entity ]]_ds_self",
		},
			"[[ .Comment ]]_of_owner_rnd_[[ .Entity ]]_ds_self",
			"[[ .Comment ]]_of_owner_mkt_[[ .Entity ]]_ds_self",
			"[[ .Comment ]]_of_owner_mkt2_[[ .Entity ]]_ds_self",
			"[[ .Comment ]]_of_owner_sub_[[ .Entity ]]_ds_self")
	})
}

// TestIntegration[[ .ClassName ]]OutsideScope404 数据范围外的详情/修改/删除一律 404：
// 观察者绑定完整按钮权限码——403（无权限码）由权限中间件语义覆盖，这里验证
// 「有权限码但范围外」；与列表可见性一致（看不见的即不存在，不泄露存在性）。
func TestIntegration[[ .ClassName ]]OutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := [[ .Entity ]]ScopeSetup(t, env, admin, "[[ .Entity ]]_ds_404", 5, nil)
	observerToken := env.Login(t, observer, "123456")

	// 观察者自己可见自己的[[ .Comment ]]。
	list, total := [[ .Entity ]]ScopePage[[ .ClassName ]]s(t, env, observerToken, "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人列表 total=%d，期望 1", total)
	}
	ownID := int64(list[0]["id"].(float64))

	// admin 建一个归属他人的[[ .Comment ]]，拿其 ID。
	otherID := [[ .Entity ]]ScopeFindUserID(t, env, admin, "owner_rnd_[[ .Entity ]]_ds_404")
	other := [[ .Entity ]]ScopeCreate[[ .ClassName ]](t, env, admin, "[[ .Comment ]]_研发专属",
		map[string]any{"ownerUserId": otherID})
	otherID64 := int64(other["id"].(float64))

	// 详情 404。
	if status := [[ .Entity ]]ScopeGet[[ .ClassName ]](t, env, observerToken, otherID64); status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	// 修改 404。
	status, _ := env.Request(t, http.MethodPut, "/[[ .RouteGroup ]]", observerToken, map[string]any{
		"id": otherID64,
		"[[ .NameFieldJSON ]]": "[[ .Comment ]]越权改名",
[[range .RequiredFillFields]]		"[[ .JSONName ]]": [[ .TestLiteral ]],
[[end]]	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	// 删除 404。
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/[[ .RouteGroup ]]/%d", otherID64), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}

	// 对照：观察者范围内操作正常。
	if status := [[ .Entity ]]ScopeGet[[ .ClassName ]](t, env, observerToken, ownID); status != http.StatusOK {
		t.Fatalf("范围内详情：HTTP %d，期望 200", status)
	}
}
[[else]]
// TestIntegration[[ .ClassName ]]CRUDSmoke 非 owner 实体的 CRUD 冒烟骨架：
// 创建 → 分页 → 详情 → 修改 → 删除 → 详情 404（数据范围矩阵不适用于本实体）。
func TestIntegration[[ .ClassName ]]CRUDSmoke(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	row := [[ .Entity ]]ScopeCreate[[ .ClassName ]](t, env, admin, "[[ .Comment ]]_冒烟", nil)
	id := int64(row["id"].(float64))

	list, total := [[ .Entity ]]ScopePage[[ .ClassName ]]s(t, env, admin, "")
	if total < 1 || len(list) < 1 {
		t.Fatalf("分页应至少 1 条，实际 total=%d", total)
	}

	if status := [[ .Entity ]]ScopeGet[[ .ClassName ]](t, env, admin, id); status != http.StatusOK {
		t.Fatalf("详情：HTTP %d，期望 200", status)
	}

	status, _ := env.Request(t, http.MethodPut, "/[[ .RouteGroup ]]", admin, map[string]any{
		"id": id,
		"[[ .NameFieldJSON ]]": "[[ .Comment ]]_改名",
[[range .RequiredFillFields]]		"[[ .JSONName ]]": [[ .TestLiteral ]],
[[end]]	})
	if status != http.StatusOK {
		t.Fatalf("修改：HTTP %d，期望 200", status)
	}

	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/[[ .RouteGroup ]]/%d", id), admin, nil)
	if status != http.StatusOK {
		t.Fatalf("删除：HTTP %d，期望 200", status)
	}
	if status := [[ .Entity ]]ScopeGet[[ .ClassName ]](t, env, admin, id); status != http.StatusNotFound {
		t.Fatalf("删除后详情：HTTP %d，期望 404", status)
	}
}
[[end]]`

// ---------- 前端 ----------

const tmplEntityAPITS = `// ---------- [[ .Module ]] [[ .Comment ]]（[[ .GenMark ]]；把以下片段追加到 web/src/api/[[ .Module ]].ts 末尾） ----------
export function page[[ .ClassName ]]s(params: any) {
  return request.get('/[[ .RouteGroup ]]/page', { params }) as Promise<any>
}
export function get[[ .ClassName ]](id: number) {
  return request.get(~/[[ .RouteGroup ]]/${id}~) as Promise<any>
}
export function create[[ .ClassName ]](data: any) {
  return request.post('/[[ .RouteGroup ]]', data) as Promise<any>
}
export function update[[ .ClassName ]](data: any) {
  return request.put('/[[ .RouteGroup ]]', data) as Promise<any>
}
export function delete[[ .ClassName ]](id: number) {
  return request.delete(~/[[ .RouteGroup ]]/${id}~) as Promise<any>
}
`

const tmplEntityVue = `<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="[[ .PermPrefix ]]"
      title="[[ .Comment ]]"
      :action-width="170"
    >
[[if .OwnerScoped]]      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <el-tag v-else type="warning" size="small">未分配</el-tag>
      </template>
      <template #form-ownerUserId="{ form }">
        <el-select v-model="form.ownerUserId" clearable filterable style="width: 100%" placeholder="缺省为当前登录用户">
          <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
        </el-select>
      </template>
[[end]]    </ProTable>
  </div>
</template>

<script setup lang="ts">
[[if .OwnerScoped]]import { computed, onMounted, ref } from 'vue'
[[else]]import { ref } from 'vue'
[[end]]import ProTable from '@/components/ProTable.vue'
[[if .HasDict]]import { useDictOptions } from '@/composables/useDict'
[[end]][[if or .HasEnum .HasDict]]import { dictLabel, formatTime } from '@/utils/format'
[[else]]import { formatTime } from '@/utils/format'
[[end]]import {
  page[[ .ClassName ]]s,
  get[[ .ClassName ]],
  create[[ .ClassName ]],
  update[[ .ClassName ]],
  delete[[ .ClassName ]],
} from '@/api/[[ .Module ]]'
[[if .OwnerScoped]]import { treeDepts, listSimpleUsers } from '@/api/system'

[[end]][[range .EnumFields]]// [[ $.Comment ]][[ .Comment ]]（生成自字段元信息：[[ .Comment ]]）。
const [[ .OptionsVar ]] = [
[[range .Enum.Values]]  { label: '[[ .Label ]]', value: [[ .Value ]] },
[[end]]]
[[end]][[range .DictFields]]// [[ .Comment ]]选项来自系统字典 [[ .Dict ]]（异步加载，失败时回退显示原值）。
const [[ .OptionsVar ]] = useDictOptions('[[ .Dict ]]')
[[end]]
const tableRef = ref()
[[if .OwnerScoped]]const users = ref<any[]>([])
const deptOptions = ref<{ label: string; value: number }[]>([])

// searchItems 用 computed：负责人/部门选项在 onMounted 异步加载后刷新下拉。
const searchItems = computed(() => [
[[range .SearchFields]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]' },
[[end]][[range .SelectFields]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', type: 'select', options: [[ .OptionsVar ]] },
[[end]]  {
    prop: 'ownerUserId',
    label: '负责人',
    type: 'select',
    options: users.value.map((u: any) => ({ label: u.nickname, value: u.id })),
  },
  { prop: 'ownerDeptId', label: '部门', type: 'select', options: deptOptions.value },
])
[[else]]const searchItems = [
[[range .SearchFields]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]' },
[[end]][[range .SelectFields]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', type: 'select', options: [[ .OptionsVar ]] },
[[end]]]
[[end]]
const columns = [
[[range .ListFields]][[ if .IsEnum]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', width: 90, formatter: (row: any) => dictLabel([[ .OptionsVar ]], row.[[ .JSONName ]]) },
[[else if .IsDict]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', width: 110, formatter: (row: any) => dictLabel([[ .OptionsVar ]], row.[[ .JSONName ]]) },
[[else if .IsTime]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', minWidth: 170, formatter: (row: any) => formatTime(row.[[ .JSONName ]]) },
[[else]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]', minWidth: 120 },
[[end]][[end]][[if .OwnerScoped]]  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
[[end]]  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
[[range .Fields]]  { prop: '[[ .JSONName ]]', label: '[[ .Comment ]]'[[ if .Required ]], required: true[[end]][[ if .IsEnum]], type: 'select', options: [[ .OptionsVar ]][[end]][[ if .IsDict ]], type: 'select', options: [[ .OptionsVar ]][[end]][[ if and (not .IsEnum) (not .IsDict) (ne .FormType "input") ]], type: '[[ .FormType ]]'[[end]] },
[[end]][[if .OwnerScoped]]  { prop: 'ownerUserId', label: '负责人', type: 'select', slot: true },
[[end]]]

const defaultForm: Record<string, any> = {
[[range .Fields]][[ if ne .TSDefault "" ]]  [[ .JSONName ]]: [[ .TSDefault ]],
[[end]][[end]][[if .OwnerScoped]]  ownerUserId: 0,
[[end]]}

const api = {
  page: page[[ .ClassName ]]s,
  detail: get[[ .ClassName ]],
  create: create[[ .ClassName ]],
  update: update[[ .ClassName ]],
  remove: delete[[ .ClassName ]],
}
[[if .OwnerScoped]]
function flattenDeptTree(nodes: any[], out: { label: string; value: number }[] = []) {
  for (const n of nodes || []) {
    out.push({ label: n.name, value: n.id })
    if (n.children?.length) flattenDeptTree(n.children, out)
  }
  return out
}

onMounted(async () => {
  const [depts, simpleUsers] = await Promise.all([treeDepts(), listSimpleUsers()])
  deptOptions.value = flattenDeptTree(depts)
  users.value = simpleUsers
})
[[end]]</script>
`

// ---------- 种子片段 / 迁移模板 ----------

const tmplEntitySeed = `package seed

// [[ .GenMark ]]：菜单/权限码种子片段（非独立文件——把以下注释行解开追加到
// internal/seed/seed.go 的 seedMenus → menus 切片，父目录须已存在于种子；
// 带 package 声明是为了本片段自身始终是合法 Go 文件）。
// ID 来自实体元信息的 menu 配置，与生成的集成测试骨架中的菜单常量一致；
// 与既有 ID 冲突时先调整，再同步集成测试常量与角色绑定。
//
// menu([[ .Menu.MenuID ]], [[ .Menu.ParentID ]], "[[ .Menu.Name ]]", "[[ .Menu.Path ]]", "[[ .Module ]]/[[ .Entity ]]/index", "[[ .PermPrefix ]]:query", "[[ .Menu.Icon ]]", [[ .Menu.Sort ]]),
// btn([[ index .Menu.ButtonIDs 0 ]], [[ .Menu.MenuID ]], "[[ .MenuShortName ]]查询", "[[ .PermPrefix ]]:query", 1),
// btn([[ index .Menu.ButtonIDs 1 ]], [[ .Menu.MenuID ]], "[[ .MenuShortName ]]新增", "[[ .PermPrefix ]]:create", 2),
// btn([[ index .Menu.ButtonIDs 2 ]], [[ .Menu.MenuID ]], "[[ .MenuShortName ]]修改", "[[ .PermPrefix ]]:update", 3),
// btn([[ index .Menu.ButtonIDs 3 ]], [[ .Menu.MenuID ]], "[[ .MenuShortName ]]删除", "[[ .PermPrefix ]]:delete", 4),
`

const tmplEntityMigration = `-- +goose Up
-- [[ .Comment ]]表（[[ .GenMark ]]的迁移模板，供实施工单参考与调整；
-- 迁移编号由实施工单分配，不得使用本文件名中的占位编号 0000X）。
-- 表结构沿用实体底座：created_at/updated_at/deleted_at + creator/updater + tenant_id（ADR 0002）。

CREATE TABLE ~[[ .TableName ]]~ (
  ~id~ bigint NOT NULL AUTO_INCREMENT,
  ~created_at~ datetime(3) DEFAULT NULL,
  ~updated_at~ datetime(3) DEFAULT NULL,
  ~deleted_at~ datetime(3) DEFAULT NULL,
  ~creator~ varchar(64) NOT NULL DEFAULT '0',
  ~updater~ varchar(64) NOT NULL DEFAULT '0',
  ~tenant_id~ bigint NOT NULL DEFAULT '1',
[[if .HasSequence]]  ~[[ .NoColumn ]]~ varchar(32) NOT NULL COMMENT '[[ .Comment ]]编号：[[ .SeqPrefix ]]+yyyyMMdd+4位序号，租户内唯一',
[[end]][[range .Fields]]  ~[[ .Name ]]~ [[ .MigrationType ]] [[ if ne .MigrationDefault "" ]]NOT NULL DEFAULT '[[ .MigrationDefault ]]'[[ else if .Required ]]NOT NULL[[ else ]]DEFAULT NULL[[end]] COMMENT '[[ .Comment ]]'[[ if or (not .IsLastField) $.OwnerScoped ]],[[end]]
[[end]][[if .OwnerScoped]]  ~owner_user_id~ bigint DEFAULT NULL COMMENT '负责人用户 ID',
  ~owner_dept_id~ bigint DEFAULT NULL COMMENT '负责人部门快照，随负责人同步（ADR 0004）',
[[end]]  PRIMARY KEY (~id~),
[[if .HasSequence]]  UNIQUE KEY ~uk_[[ .TableName ]]_tenant_no~ (~tenant_id~, ~[[ .NoColumn ]]~),
[[end]]  KEY ~idx_[[ .TableName ]]_deleted_at~ (~deleted_at~)[[if .OwnerScoped]],
  KEY ~idx_[[ .TableName ]]_owner_user_id~ (~owner_user_id~),
  KEY ~idx_[[ .TableName ]]_owner_dept_id~ (~owner_dept_id~)[[end]]
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
-- 回滚：删除本迁移新建的表。
DROP TABLE IF EXISTS ~[[ .TableName ]]~;
`
