package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

func (s *Service) ListDictTypes(ctx context.Context, req *dto.DictTypePageReq) ([]model.DictType, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.DictTypeFilter{Name: req.Name, Type: req.Type, Status: req.Status}
	return s.repo.ListDictTypes(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) CreateDictType(ctx context.Context, req *dto.DictTypeCreateReq) error {
	total, err := s.repo.CountDictTypeByType(ctx, req.Type, 0)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("字典类型已存在")
	}
	item := &model.DictType{
		TenantID: s.TenantID(ctx),
		Name:     req.Name,
		Type:     req.Type,
		Status:   req.Status,
		Remark:   req.Remark,
	}
	return s.repo.CreateDictType(ctx, item)
}

func (s *Service) UpdateDictType(ctx context.Context, req *dto.DictTypeUpdateReq) error {
	item, err := s.repo.FindDictTypeByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if item == nil {
		return xerr.NotFound("字典类型不存在")
	}
	total, err := s.repo.CountDictTypeByType(ctx, req.Type, req.ID)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("字典类型已存在")
	}
	oldType := item.Type
	item.Name = req.Name
	item.Type = req.Type
	item.Status = req.Status
	item.Remark = req.Remark
	if err := s.repo.SaveDictType(ctx, item); err != nil {
		return err
	}
	// 字典类型编码变化时，同步更新字典数据的归属。
	if oldType != req.Type {
		dataList, err := s.repo.ListDictData(ctx, oldType, "", nil)
		if err != nil {
			return err
		}
		for i := range dataList {
			dataList[i].DictType = req.Type
			if err := s.repo.SaveDictData(ctx, &dataList[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) DeleteDictType(ctx context.Context, id int64) error {
	item, err := s.repo.FindDictTypeByID(ctx, id)
	if err != nil {
		return err
	}
	if item == nil {
		return xerr.NotFound("字典类型不存在")
	}
	total, err := s.repo.CountDictDataByType(ctx, item.Type)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Business(1601, "字典类型下存在字典数据，无法删除")
	}
	return s.repo.DeleteDictType(ctx, id)
}

func (s *Service) ListDictData(ctx context.Context, dictType, label string, status *int8) ([]model.DictData, error) {
	return s.repo.ListDictData(ctx, dictType, label, status)
}

func (s *Service) CreateDictData(ctx context.Context, req *dto.DictDataCreateReq) error {
	item := &model.DictData{
		TenantID:  s.TenantID(ctx),
		DictType:  req.DictType,
		Label:     req.Label,
		Value:     req.Value,
		Sort:      req.Sort,
		Status:    req.Status,
		ColorType: req.ColorType,
		CSSClass:  req.CSSClass,
		Remark:    req.Remark,
	}
	return s.repo.CreateDictData(ctx, item)
}

func (s *Service) UpdateDictData(ctx context.Context, req *dto.DictDataUpdateReq) error {
	item, err := s.repo.FindDictDataByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if item == nil {
		return xerr.NotFound("字典数据不存在")
	}
	item.DictType = req.DictType
	item.Label = req.Label
	item.Value = req.Value
	item.Sort = req.Sort
	item.Status = req.Status
	item.ColorType = req.ColorType
	item.CSSClass = req.CSSClass
	item.Remark = req.Remark
	return s.repo.SaveDictData(ctx, item)
}

// RegisterDictUsageChecker 注册字典项引用检查（issue #64 review，spec §4.14）：
// 按 dictType 维度登记一个计数函数，DeleteDictData 删除前调用它统计业务数据
// 对「该字典值」的引用数；>0 返回 409 并提示改为停用。system 不反向依赖业务
// 模块，由业务方在 internal/app 装配期注册（如 OA 的 oa_expense_category）。
// 同一 dictType 重复注册以后者为准；空 dictType 或 nil fn 忽略。
func (s *Service) RegisterDictUsageChecker(dictType string, fn func(ctx context.Context, code string) (int64, error)) {
	dictType = strings.TrimSpace(dictType)
	if dictType == "" || fn == nil {
		return
	}
	s.dictUsageMu.Lock()
	defer s.dictUsageMu.Unlock()
	if s.dictUsageCheckers == nil {
		s.dictUsageCheckers = make(map[string]func(ctx context.Context, code string) (int64, error))
	}
	s.dictUsageCheckers[dictType] = fn
}

// dictUsageChecker 取字典类型的引用计数函数（未注册返回 nil）。
func (s *Service) dictUsageChecker(dictType string) func(ctx context.Context, code string) (int64, error) {
	s.dictUsageMu.RLock()
	defer s.dictUsageMu.RUnlock()
	return s.dictUsageCheckers[dictType]
}

func (s *Service) DeleteDictData(ctx context.Context, id int64) error {
	item, err := s.repo.FindDictDataByID(ctx, id)
	if err != nil {
		return err
	}
	if item == nil {
		return xerr.NotFound("字典数据不存在")
	}
	// 被业务数据引用时只能停用不能删除（spec §4.14）；检查失败按内部错误
	// fail-closed，避免静默放宽引用保护。
	if checker := s.dictUsageChecker(item.DictType); checker != nil {
		used, err := checker(ctx, item.Value)
		if err != nil {
			return xerr.Internal(fmt.Errorf("字典项引用检查失败: %w", err))
		}
		if used > 0 {
			return xerr.Conflict(fmt.Sprintf(
				"字典项「%s」已被 %d 条业务数据引用，不能删除；请改为停用",
				item.Label, used,
			))
		}
	}
	return s.repo.DeleteDictData(ctx, id)
}
