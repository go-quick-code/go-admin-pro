package service

import (
	"context"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// member.go 是会员用户的管理端业务（issue #174，docs/mall-mvp-spec.md §3.1/§4）：
// 分页/详情/创建/更新/停启用；mobile 租户内唯一（撞唯一键 409）；密码
// bcrypt 散列（明文不落库不回传，model 层 json:"-" 双保险）。无删除端点：
// 会员是业务身份（积分流水/订单随会员存续），生命周期由停启用覆盖。
//
// C 端注册/登录（mobile 唯一冲突 409 同语义复用 FindMemberByMobile）随 T2；
// point_balance 只经 T2 唯一入账入口变动，本文件不提供任何直改路径。

// ListMembers 分页查询会员。
func (s *Service) ListMembers(ctx context.Context, req *dto.MemberPageReq) ([]model.MemberUser, int64, error) {
	if req.Status != nil {
		if err := ValidateMemberStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListMembers(ctx, mallrepo.MemberFilter{
		Mobile:   trimString(req.Mobile),
		Nickname: trimString(req.Nickname),
		LevelID:  req.LevelID,
		Status:   req.Status,
	}, page)
}

// GetMember 会员详情（含收货地址清单，spec §4「地址列表（按会员）」）。
// 不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetMember(ctx context.Context, id int64) (*dto.MemberDetail, error) {
	row, err := s.findMember(ctx, id)
	if err != nil {
		return nil, err
	}
	addresses, err := s.repo.ListAllAddressesByUser(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return &dto.MemberDetail{MemberUser: row, Addresses: addresses}, nil
}

// CreateMember 创建会员：mobile 租户内唯一（撞唯一键 409，issue #174 验收项）；
// 密码选填（空 = 不设密码，C 端注册自设——T2），填了 bcrypt 散列。
func (s *Service) CreateMember(ctx context.Context, req *dto.MemberSaveReq) (*model.MemberUser, error) {
	fields, passwordHash, err := s.validateMemberReq(ctx, req)
	if err != nil {
		return nil, err
	}
	status := model.StatusEnable
	if req.Status != nil {
		status = *req.Status
	}
	row := &model.MemberUser{
		Mobile:       fields["mobile"].(string),
		PasswordHash: passwordHash,
		Nickname:     fields["nickname"].(string),
		AvatarURL:    req.AvatarURL,
		Status:       status,
		LevelID:      req.LevelID,
		Remark:       req.Remark,
	}
	if err := s.repo.CreateMember(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("手机号已存在：" + row.Mobile)
		}
		return nil, err
	}
	return row, nil
}

// UpdateMember 修改会员（id 在 body，与 CRM/ProTable 一致）：password 空 =
// 不改密码（非空则重散列）；mobile 可改，唯一键兜底并发冲突（409）。
func (s *Service) UpdateMember(ctx context.Context, req *dto.MemberUpdateReq) error {
	row, err := s.findMember(ctx, req.ID)
	if err != nil {
		return err
	}
	fields, passwordHash, err := s.validateMemberReq(ctx, &req.MemberSaveReq)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"mobile":     fields["mobile"],
		"nickname":   fields["nickname"],
		"avatar_url": req.AvatarURL,
		"level_id":   req.LevelID,
		"remark":     req.Remark,
	}
	if passwordHash != "" {
		updates["password_hash"] = passwordHash
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if err := s.repo.UpdateMemberFields(ctx, row.ID, updates); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("手机号已存在：" + fields["mobile"].(string))
		}
		return err
	}
	return nil
}

// UpdateMemberStatus 停启用会员（PUT /mall/member/:id/status）：状态位更新，
// 挂 mall:member:update 权限码（不设独立码，spec §7 权限码仅 query/create/
// update——停启用是更新语义的子集）。
func (s *Service) UpdateMemberStatus(ctx context.Context, id int64, status int8) error {
	row, err := s.findMember(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateMemberStatus(status); err != nil {
		return err
	}
	return s.repo.UpdateMemberFields(ctx, row.ID, map[string]any{"status": status})
}

// findMember 取会员并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findMember(ctx context.Context, id int64) (*model.MemberUser, error) {
	row, err := s.repo.FindMemberByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("会员不存在")
	}
	return row, nil
}

// validateMemberReq 校验会员公共字段并归一化：mobile 格式 + 密码规则 +
// 等级存在性（level_id 非零时必须是本租户在册等级——跨租户/不存在 404）。
// 返回 mobile/nickname 归一化值与散列后的密码哈希（空串 = 未设/不改）。
func (s *Service) validateMemberReq(ctx context.Context, req *dto.MemberSaveReq) (map[string]any, string, error) {
	mobile := trimString(req.Mobile)
	if err := ValidateMemberMobile(mobile); err != nil {
		return nil, "", err
	}
	if err := ValidateMemberPassword(req.Password); err != nil {
		return nil, "", err
	}
	nickname := trimString(req.Nickname)
	if nickname == "" {
		// 昵称选填，缺省取手机号（C 端注册同款默认，芋道惯例）。
		nickname = mobile
	}
	if req.LevelID > 0 {
		level, err := s.repo.FindLevelByID(ctx, req.LevelID)
		if err != nil {
			return nil, "", err
		}
		if level == nil {
			return nil, "", xerr.NotFound("会员等级不存在")
		}
	}
	var passwordHash string
	if password := trimString(req.Password); password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, "", xerr.BadRequest("密码处理失败：" + err.Error())
		}
		passwordHash = string(hash)
	}
	// create 与 update 的「空密码」语义一致：创建不设密码（C 端注册自设，T2）、
	// 更新不改密码；散列只在明文非空时发生。
	return map[string]any{"mobile": mobile, "nickname": nickname}, passwordHash, nil
}

// trimString 去首尾空白（与 pay service 同名助手同语义）。
func trimString(v string) string { return strings.TrimSpace(v) }
