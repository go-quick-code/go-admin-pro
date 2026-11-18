package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// address.go 是会员收货地址的管理端业务（issue #174，docs/mall-mvp-spec.md
// §3.1/§4）：按会员查地址（只读）。无写入端点——地址维护随 C 端自助
//（T2/T5）；权限随会员查询码 mall:member:query（地址是会员详情数据）。

// ListAddresses 分页查询地址（userId 过滤：指定会员时先做存在性校验，
// 不存在/跨租户统一 404——不泄露存在性）。
func (s *Service) ListAddresses(ctx context.Context, req *dto.AddressPageReq) ([]model.MemberAddress, int64, error) {
	if req.UserID > 0 {
		if _, err := s.findMember(ctx, req.UserID); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListAddressesByUser(ctx, mallrepo.AddressFilter{
		UserID:   req.UserID,
		Receiver: trimString(req.Receiver),
	}, page)
}

// ListAddressesByMember 按会员取全部地址（会员详情抽屉数据源，不分页）；
// 会员不存在/跨租户 404。
func (s *Service) ListAddressesByMember(ctx context.Context, userID int64) ([]model.MemberAddress, error) {
	if _, err := s.findMember(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListAllAddressesByUser(ctx, userID)
}
