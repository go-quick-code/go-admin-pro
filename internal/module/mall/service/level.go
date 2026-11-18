package service

import (
	"context"
	"strconv"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// level.go 是会员等级的管理端业务（issue #174，docs/mall-mvp-spec.md §3.1/§4）：
// CRUD；name 与 level 双租户内唯一（撞唯一键 409）；删除守卫「被会员引用
// 禁删」（409）。等级纯手工维护（MVP 无经验值体系，experience 是纯维护列）。
// 种子一行「普通会员 level=1 experience=0」（internal/seed，表空时幂等写入）。

// ListLevels 分页查询等级（level 升序，排序语义）。
func (s *Service) ListLevels(ctx context.Context, req *dto.LevelPageReq) ([]model.MemberLevel, int64, error) {
	if req.Status != nil {
		if err := ValidateMemberStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListLevels(ctx, mallrepo.LevelFilter{
		Name:   trimString(req.Name),
		Status: req.Status,
	}, page)
}

// ListLevelOptions 全量启用等级（level 升序）：会员表单等级下拉的数据源
// （同 pay ListChannelOptions 的「清单只读」形状，挂 mall:member:query——
// 持有会员管理菜单即可读）。
func (s *Service) ListLevelOptions(ctx context.Context) ([]model.MemberLevel, error) {
	return s.repo.ListEnabledLevels(ctx)
}

// GetLevel 等级详情。不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetLevel(ctx context.Context, id int64) (*model.MemberLevel, error) {
	row, err := s.findLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// CreateLevel 创建等级：name/level 租户内唯一（撞唯一键 409）。
func (s *Service) CreateLevel(ctx context.Context, req *dto.LevelSaveReq) (*model.MemberLevel, error) {
	name, levelValue, experience, err := validateLevelReq(req)
	if err != nil {
		return nil, err
	}
	status := model.StatusEnable
	if req.Status != nil {
		status = *req.Status
	}
	row := &model.MemberLevel{
		Name:       name,
		Level:      levelValue,
		Experience: experience,
		Status:     status,
		Remark:     req.Remark,
	}
	if err := s.repo.CreateLevel(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("等级名称或等级值已存在：" + name + " / " + strconv.Itoa(levelValue))
		}
		return nil, err
	}
	return row, nil
}

// UpdateLevel 修改等级（id 在 body，与 CRM/ProTable 一致）：name/level 可改，
// 唯一键兜底并发冲突。
func (s *Service) UpdateLevel(ctx context.Context, req *dto.LevelUpdateReq) error {
	row, err := s.findLevel(ctx, req.ID)
	if err != nil {
		return err
	}
	name, levelValue, experience, err := validateLevelReq(&req.LevelSaveReq)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"name":       name,
		"level":      levelValue,
		"experience": experience,
		"remark":     req.Remark,
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if err := s.repo.UpdateLevelFields(ctx, row.ID, fields); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("等级名称或等级值已存在：" + name + " / " + strconv.Itoa(levelValue))
		}
		return err
	}
	return nil
}

// DeleteLevel 删除等级（软删）：被会员引用 409（引用计数不带 deleted_at
// 过滤——会员不可删，引用永久）；已停用/未引用的等级可删。
func (s *Service) DeleteLevel(ctx context.Context, id int64) error {
	row, err := s.findLevel(ctx, id)
	if err != nil {
		return err
	}
	referenced, err := s.repo.CountMembersByLevel(ctx, row.ID)
	if err != nil {
		return err
	}
	if referenced > 0 {
		return xerr.Conflict("等级存在会员引用，请先调整会员等级再删除")
	}
	return s.repo.DeleteLevel(ctx, row.ID)
}

// findLevel 取等级并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findLevel(ctx context.Context, id int64) (*model.MemberLevel, error) {
	row, err := s.repo.FindLevelByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("会员等级不存在")
	}
	return row, nil
}

// validateLevelReq 校验等级公共字段：名称必填 ≤64、等级值 ≥1、经验阈值 ≥0。
// 返回归一化名称与数值。
func validateLevelReq(req *dto.LevelSaveReq) (name string, levelValue, experience int, err error) {
	name = trimString(req.Name)
	if err = ValidateLevelName(name); err != nil {
		return "", 0, 0, err
	}
	if err = ValidateLevelValue(req.Level); err != nil {
		return "", 0, 0, err
	}
	if err = ValidateLevelExperience(req.Experience); err != nil {
		return "", 0, 0, err
	}
	return name, req.Level, req.Experience, nil
}
