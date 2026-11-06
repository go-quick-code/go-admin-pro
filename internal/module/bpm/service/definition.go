// definition.go 承载流程定义管理（issue #44，spec #42 §4.1/§4.2/§4.4）：
// 草稿 CRUD、发布/停用状态机、同 key 单发布版本、审批人规则引用校验。
//
// 状态机（spec §4.1）：
//
//	草稿 ──发布──▶ 发布 ──停用──▶ 停用
//	  ▲              │发布新版本时自动停用旧版本
//	  └── 仅草稿可编辑/删除；停用后不可发起新实例（#46 发起入口校验）
//
// 版本号在创建草稿时按同 key（含软删）max+1 发放，落库后不可变；
// 同 key 同时只有一个发布版本，发布新版本与旧版本停用在同一事务完成。
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// 定义 key / 名称 / 备注的取值边界。key 是版本历史的锚，创建后不可变。
const (
	MaxDefinitionKeyLen   = 64
	MaxDefinitionNameLen  = 64
	MaxDefinitionRemarkLn = 500
)

// CreateDefinition 创建流程定义草稿：结构校验（纯函数）→ 版本号发放 → 落库。
// 同 key 允许多次创建（这就是「发新版本」的入口），版本号自动递增；
// 并发同 key 创建撞唯一键 (tenant_id, key, version) 时翻译为 409。
func (s *Service) CreateDefinition(ctx context.Context, req *dto.DefinitionCreateReq) (*model.ProcessDefinition, error) {
	if err := validateDefinitionMeta(req.Key, req.Name, req.BusinessType, req.Remark); err != nil {
		return nil, err
	}
	nodes := NormalizeNodes(req.Nodes)
	if err := ValidateNodes(nodes); err != nil {
		return nil, err
	}

	var created model.ProcessDefinition
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		version, err := tx.NextVersion(ctx, req.Key)
		if err != nil {
			return err
		}
		created = model.ProcessDefinition{
			Key:          req.Key,
			Version:      version,
			Name:         req.Name,
			BusinessType: req.BusinessType,
			Nodes:        nodes,
			Status:       model.DefinitionStatusDraft,
			Remark:       req.Remark,
		}
		if err := tx.CreateDefinition(ctx, &created); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return xerr.Conflict("流程版本发放冲突，请重试")
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// UpdateDefinition 编辑草稿（仅草稿，spec §4.1）。key 与 businessType 不可变：
// 更新请求不携带这两个字段，DTO 层即杜绝。状态机校验先于字段校验——
// 非草稿一律 409，与请求内容无关。
func (s *Service) UpdateDefinition(ctx context.Context, req *dto.DefinitionUpdateReq) (*model.ProcessDefinition, error) {
	var updated *model.ProcessDefinition
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindDefinitionByIDForUpdate(ctx, req.ID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("流程定义不存在")
		}
		if row.Status != model.DefinitionStatusDraft {
			return xerr.Conflict(fmt.Sprintf("定义已%s，不可编辑，只能创建新版本", model.DefinitionStatusName(row.Status)))
		}
		if err := validateText("流程名称", req.Name, MaxDefinitionNameLen, true); err != nil {
			return err
		}
		if len([]rune(req.Remark)) > MaxDefinitionRemarkLn {
			return xerr.BadRequest(fmt.Sprintf("备注不能超过 %d 字", MaxDefinitionRemarkLn))
		}
		nodes := NormalizeNodes(req.Nodes)
		if err := ValidateNodes(nodes); err != nil {
			return err
		}
		if err := tx.UpdateDefinitionDraft(ctx, req.ID, req.Name, nodes, req.Remark); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	updated, err = s.repo.FindDefinitionByID(ctx, req.ID)
	return updated, err
}

// PublishDefinition 发布草稿（spec §4.1/§4.2/§4.4）：
//  1. 行锁读定义，状态机校验（仅草稿可发布）；
//  2. 节点结构校验 + 审批人引用校验（用户存在且启用/角色存在/部门存在）；
//  3. BusinessHandler 注册 guard：未注册 business_type 的定义不允许发布；
//  4. 同一事务内：同 key 旧发布版本自动停用 + 本版本置发布。
//
// 校验失败不改变草稿状态，修正后可再次发布。
func (s *Service) PublishDefinition(ctx context.Context, id int64) (*model.ProcessDefinition, error) {
	var published *model.ProcessDefinition
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindDefinitionByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("流程定义不存在")
		}
		if row.Status != model.DefinitionStatusDraft {
			return xerr.Conflict(fmt.Sprintf("定义已%s，仅草稿可发布", model.DefinitionStatusName(row.Status)))
		}

		nodes := NormalizeNodes(row.Nodes)
		if err := ValidateNodes(nodes); err != nil {
			return err
		}
		if err := s.validateNodeRefs(ctx, nodes); err != nil {
			return err
		}
		if _, ok := s.handlerFor(row.BusinessType); !ok {
			return xerr.BadRequest(fmt.Sprintf("业务类型 %s 未注册处理器，不能发布", row.BusinessType))
		}

		// 行锁锁定同 key 当前发布版本：并发发布时后到者在此排队，
		// 提交后读到前者已发布再将其停用，保证同 key 单发布版本。
		old, err := tx.LockPublishedByKeyForUpdate(ctx, row.Key, row.ID)
		if err != nil {
			return err
		}
		_ = old // 存在即被本事务锁定；统一由 DisableDefinition 置停用。
		now := time.Now()
		if old != nil {
			if err := tx.DisableDefinition(ctx, old.ID); err != nil {
				return err
			}
		}
		if err := tx.PublishDefinition(ctx, row.ID, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	published, err = s.repo.FindDefinitionByID(ctx, id)
	return published, err
}

// DisableDefinition 停用发布版本（spec §4.1）：停用后不可发起新实例
// （#46 发起入口校验），在途实例继续。仅发布状态可停用。
func (s *Service) DisableDefinition(ctx context.Context, id int64) (*model.ProcessDefinition, error) {
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindDefinitionByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("流程定义不存在")
		}
		if row.Status != model.DefinitionStatusPublished {
			return xerr.Conflict(fmt.Sprintf("定义处于%s，仅发布状态可停用", model.DefinitionStatusName(row.Status)))
		}
		return tx.DisableDefinition(ctx, row.ID)
	})
	if err != nil {
		return nil, err
	}
	row, err := s.repo.FindDefinitionByID(ctx, id)
	return row, err
}

// DeleteDefinition 删除草稿（spec §4.1/§4.14）：仅草稿且无实例可删；
// 已发布/停用的定义只能停用，保留版本历史。
func (s *Service) DeleteDefinition(ctx context.Context, id int64) error {
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindDefinitionByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("流程定义不存在")
		}
		if row.Status != model.DefinitionStatusDraft {
			return xerr.Conflict(fmt.Sprintf("定义已%s，不可删除（有版本历史的定义只能停用）", model.DefinitionStatusName(row.Status)))
		}
		count, err := tx.CountInstancesByDefinition(ctx, row.ID)
		if err != nil {
			return err
		}
		if count > 0 {
			return xerr.Conflict("定义已存在流程实例，不能删除")
		}
		return tx.DeleteDefinition(ctx, row.ID)
	})
}

// GetDefinition 查询定义详情（租户隔离由插件保证，跨租户 404）。
func (s *Service) GetDefinition(ctx context.Context, id int64) (*model.ProcessDefinition, error) {
	row, err := s.repo.FindDefinitionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("流程定义不存在")
	}
	return row, nil
}

// ListDefinitions 分页查询定义；key 筛选即版本历史（含全部状态版本）。
func (s *Service) ListDefinitions(ctx context.Context, req *dto.DefinitionPageReq) ([]model.ProcessDefinition, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.DefinitionFilter{
		Key:          req.Key,
		Name:         req.Name,
		BusinessType: req.BusinessType,
		Status:       req.Status,
	}
	return s.repo.PageDefinitions(ctx, filter, page.Offset(), page.PageSize)
}

// validateNodeRefs 校验审批人规则的引用存在性（发布期，spec §4.2）：
// 指定用户 → 存在且启用；指定角色 → 存在；指定部门负责人 → 部门存在。
// permission 规则的取值合法性由结构校验把关；「权限码无持有人」在运行期
// 提交时拒绝（候选池解析为空 → 400，spec §4.2），不在发布期耦合菜单表。
func (s *Service) validateNodeRefs(ctx context.Context, nodes []model.Node) error {
	for i, node := range nodes {
		position := fmt.Sprintf("第 %d 个节点", i+1)
		switch node.AssigneeType {
		case model.AssigneeUser:
			userID, _ := strconv.ParseInt(node.AssigneeValue, 10, 64)
			ok, err := s.sys.ActiveUserExists(ctx, userID)
			if err != nil {
				return err
			}
			if !ok {
				return xerr.BadRequest(fmt.Sprintf("%s（指定用户）的用户 %s 不存在或已停用", position, node.AssigneeValue))
			}
		case model.AssigneeRole:
			roleID, _ := strconv.ParseInt(node.AssigneeValue, 10, 64)
			ok, err := s.sys.RoleExists(ctx, roleID)
			if err != nil {
				return err
			}
			if !ok {
				return xerr.BadRequest(fmt.Sprintf("%s（指定角色）的角色 %s 不存在", position, node.AssigneeValue))
			}
		case model.AssigneeDeptLeader:
			ok, err := s.sys.DeptExists(ctx, node.DeptID)
			if err != nil {
				return err
			}
			if !ok {
				return xerr.BadRequest(fmt.Sprintf("%s（指定部门负责人）的部门 %d 不存在", position, node.DeptID))
			}
		}
	}
	return nil
}

// validateDefinitionMeta 校验创建请求的元字段（key/name/businessType/remark）。
func validateDefinitionMeta(key, name, businessType, remark string) error {
	if err := validateText("流程标识", key, MaxDefinitionKeyLen, true); err != nil {
		return err
	}
	for _, r := range key {
		// key 落 URL 与唯一键，限定安全字符集：字母/数字/下划线/中划线。
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return xerr.BadRequest("流程标识只能包含字母、数字、下划线与中划线")
		}
	}
	if err := validateText("流程名称", name, MaxDefinitionNameLen, true); err != nil {
		return err
	}
	if err := validateText("业务类型", businessType, MaxDefinitionKeyLen, true); err != nil {
		return err
	}
	if err := validateText("备注", remark, MaxDefinitionRemarkLn, false); err != nil {
		return err
	}
	return nil
}

// validateText 校验单值文本长度（required 时去首尾空白后非空）。
func validateText(field, value string, maxLen int, required bool) error {
	if required && value == "" {
		return xerr.BadRequest(fmt.Sprintf("%s不能为空", field))
	}
	if len([]rune(value)) > maxLen {
		return xerr.BadRequest(fmt.Sprintf("%s不能超过 %d 字", field, maxLen))
	}
	return nil
}
