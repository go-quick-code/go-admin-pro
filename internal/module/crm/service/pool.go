// pool.go 承载公海纵切（issue #13）的业务层：
// 释放/认领/转移/公海列表与负责人历史查询（docs/crm-mvp-spec.md §4.1/§4.2）。
package service

import (
	"context"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// 释放/转移的权限码：与 api 路由种子（internal/seed）保持一致。
// 释放/转移路由不挂对应权限中间件，改在 service 内做「负责人本人 或 权限码」
// 的组合校验（见 canOperateOwner）；认领固定认到自己名下，路由直接挂
// crm:customer:claim 中间件。
const (
	PermCustomerRelease  = "crm:customer:release"
	PermCustomerTransfer = "crm:customer:transfer"
	// PermCustomerPoolView 控制公海可见性（spec §4.1）：公海列表与仪表盘
	// 客户总数的公海口径都以该权限码为准。
	PermCustomerPoolView = "crm:customer:pool:view"
)

// canOperateOwner 校验当前用户可否对客户做负责人变更（释放/转移）：
// 负责人本人放行；否则须持有对应权限码（super_admin 以 "*" 通配）。
func (s *Service) canOperateOwner(ctx context.Context, ownerUserID int64, permission string) error {
	uid := CurrentUserID(ctx)
	if ownerUserID > 0 && uid == ownerUserID {
		return nil
	}
	ok, err := s.sys.UserHasPermission(ctx, uid, permission)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.Forbidden("没有操作权限：" + permission)
	}
	return nil
}

// ReleaseCustomer 释放客户到公海：owner/部门快照置空、落池字段（进入时间与
// 释放原因）、写历史（release）。操作权限：负责人本人或 crm:customer:release。
func (s *Service) ReleaseCustomer(ctx context.Context, req *dto.CustomerReleaseReq) error {
	c, err := s.findVisibleCustomer(ctx, req.ID)
	if err != nil {
		return err
	}
	if err := s.canOperateOwner(ctx, c.OwnerUserID, PermCustomerRelease); err != nil {
		return err
	}
	if c.OwnerUserID == 0 {
		return xerr.BadRequest("客户已在公海，无需重复释放")
	}
	now, fromOwner, fromDept := time.Now(), c.OwnerUserID, c.OwnerDeptID
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 条件更新即乐观并发：负责人已被并发操作变更时 0 行受影响 → 冲突。
		rows, err := tx.ReleaseCustomerToPool(ctx, c.ID, fromOwner, now, req.Reason)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("客户负责人已变更，请刷新后重试")
		}
		return tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
			TenantID:        currentTenantID(ctx),
			CustomerID:      c.ID,
			Action:          model.OwnerHistoryRelease,
			FromOwnerUserID: fromOwner,
			FromOwnerDeptID: fromDept,
			OperatorUserID:  CurrentUserID(ctx),
		})
	})
}

// ClaimCustomer 从公海认领客户到自己名下：校验认领上限（规格 §4.2，
// 上限按租户配置生效，缺省 50——#14 起从 crm_pool_config 读取）、
// 客户仍在公海（条件更新保证并发认领只成功一个）、写历史（claim）并落部门快照。
func (s *Service) ClaimCustomer(ctx context.Context, req *dto.CustomerClaimReq) (*model.Customer, error) {
	uid := CurrentUserID(ctx)
	c, err := s.repo.FindCustomerByID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, xerr.NotFound("客户不存在")
	}

	// 认领上限：名下未删除客户总数（不区分成交），达上限拒绝（CheckClaimLimit）。
	// 上限值来自租户级公海配置（#14）：行缺失按默认值 50，行为与 #13 一致。
	cfg, err := s.GetPoolConfig(ctx)
	if err != nil {
		return nil, err
	}
	owned, err := s.repo.CountCustomersByOwner(ctx, uid)
	if err != nil {
		return nil, err
	}
	if err := CheckClaimLimit(owned, cfg.ClaimLimit); err != nil {
		return nil, err
	}
	// 部门快照随新负责人落库（规格 §4.1：owner_dept_id 随 owner 同步快照）。
	deptID, err := s.ownerDeptSnapshot(ctx, uid)
	if err != nil {
		return nil, err
	}

	var claimed *model.Customer
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 条件 owner_user_id IS NULL：并发认领同一客户只有第一个事务生效。
		rows, err := tx.ClaimCustomerFromPool(ctx, c.ID, uid, deptID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("客户已被他人认领，请刷新公海")
		}
		if err := tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
			TenantID:      currentTenantID(ctx),
			CustomerID:    c.ID,
			Action:        model.OwnerHistoryClaim,
			ToOwnerUserID: uid,
			ToOwnerDeptID: deptID,
			// 认领固定操作人即新负责人。
			OperatorUserID: uid,
		}); err != nil {
			return err
		}
		claimed, err = tx.FindCustomerByID(ctx, c.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.fillOwnerName(ctx, claimed)
	return claimed, nil
}

// TransferCustomer 把客户转移给同租户其他用户：重落部门快照、写历史（transfer）。
// 操作权限：负责人本人或 crm:customer:transfer；公海客户须先认领再转移。
func (s *Service) TransferCustomer(ctx context.Context, req *dto.CustomerTransferReq) error {
	c, err := s.findVisibleCustomer(ctx, req.ID)
	if err != nil {
		return err
	}
	if err := s.canOperateOwner(ctx, c.OwnerUserID, PermCustomerTransfer); err != nil {
		return err
	}
	if c.OwnerUserID == 0 {
		return xerr.BadRequest("公海客户请先认领，再转移")
	}
	if req.ToOwnerUserID == c.OwnerUserID {
		return xerr.BadRequest("新负责人与当前负责人相同")
	}
	toDept, err := s.ownerDeptSnapshot(ctx, req.ToOwnerUserID)
	if err != nil {
		return err
	}
	fromOwner, fromDept := c.OwnerUserID, c.OwnerDeptID
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		rows, err := tx.TransferCustomerOwner(ctx, c.ID, fromOwner, req.ToOwnerUserID, toDept)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("客户负责人已变更，请刷新后重试")
		}
		return tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
			TenantID:        currentTenantID(ctx),
			CustomerID:      c.ID,
			Action:          model.OwnerHistoryTransfer,
			FromOwnerUserID: fromOwner,
			FromOwnerDeptID: fromDept,
			ToOwnerUserID:   req.ToOwnerUserID,
			ToOwnerDeptID:   toDept,
			OperatorUserID:  CurrentUserID(ctx),
		})
	})
}

// ListPoolCustomers 公海分页：owner IS NULL 的客户（规格 §4.1），
// 不受数据范围过滤——可见性由路由上的 crm:customer:pool:view 权限码控制。
func (s *Service) ListPoolCustomers(ctx context.Context, req *dto.CustomerPoolPageReq) ([]model.Customer, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if req.Status != nil {
		if err := ValidateCustomerStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	filter := repository.CustomerFilter{Name: req.Name, Status: req.Status, PoolOnly: true}
	list, total, err := s.repo.ListCustomers(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	// 公海行负责人恒空，补齐为幂等空操作，保持列表装配路径一致。
	s.fillOwnerNames(ctx, list)
	return list, total, nil
}

// ListCustomerOwnerHistory 按客户分页查负责人历史（新→旧）。
// 可见性与客户详情一致：范围外/租户外一律 404，不泄露存在性。
func (s *Service) ListCustomerOwnerHistory(ctx context.Context, customerID int64, req *dto.OwnerHistoryPageReq) ([]model.CustomerOwnerHistory, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if _, err := s.findVisibleCustomer(ctx, customerID); err != nil {
		return nil, 0, err
	}
	list, total, err := s.repo.ListOwnerHistory(ctx, customerID, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillHistoryNames(ctx, list)
	return list, total, nil
}

// fillHistoryNames 批量补齐历史行的负责人/操作人昵称与部门名称（展示用）。
func (s *Service) fillHistoryNames(ctx context.Context, list []model.CustomerOwnerHistory) {
	userSet := map[int64]bool{}
	deptSet := map[int64]bool{}
	for _, h := range list {
		for _, id := range []int64{h.FromOwnerUserID, h.ToOwnerUserID, h.OperatorUserID} {
			if id > 0 {
				userSet[id] = true
			}
		}
		for _, id := range []int64{h.FromOwnerDeptID, h.ToOwnerDeptID} {
			if id > 0 {
				deptSet[id] = true
			}
		}
	}
	userIDs := setToSlice(userSet)
	deptIDs := setToSlice(deptSet)
	names, deptNames := s.fetchOwnerNames(ctx, userIDs, deptIDs)
	for i := range list {
		list[i].FromOwnerUserName = names[list[i].FromOwnerUserID]
		list[i].ToOwnerUserName = names[list[i].ToOwnerUserID]
		list[i].OperatorUserName = names[list[i].OperatorUserID]
		list[i].FromOwnerDeptName = deptNames[list[i].FromOwnerDeptID]
		list[i].ToOwnerDeptName = deptNames[list[i].ToOwnerDeptID]
	}
}

// setToSlice 去重集合转切片（map 迭代无序，仅用于批量查询入参）。
func setToSlice(set map[int64]bool) []int64 {
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}
