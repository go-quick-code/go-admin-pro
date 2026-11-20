package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// member_app.go 是会员 C 端自助身份业务（issue #178，T5；docs/mall-mvp-spec.md
// §2/§4/§7 裁决 2）：注册 / 登录 / 我的信息。公开组端点（/mall/app/auth/*），
// 无登录主体——注册与登录的租户上下文由本层显式落默认租户：
//
// **C 端 MVP 单租户口径（边界记录）**：注册/登录请求不带租户参数（芋道
// C 端靠域名/站点配置路由租户，MVP 无 C 端站点），公开端点统一落默认租户
//（tenant.DefaultID，与 mallTenantID 无主体时的兜底一致）；其余租户的会员
// 由管理端维护，C 端登录按 mobile 在默认租户内查号——跨租户 C 端登录页
// 随完整 member 认证体系后续票。令牌校验不受此限：tid 来自令牌自述，
// 任意租户的会员令牌都能自校验（跨租户 ID 顶替经 DB 回查拦截）。
//
// 失败文案不泄露存在性：登录的「mobile 不存在 / 密码错误 / 无密码（管理端
// 建号未设密）/ 会员停用」四种失败统一「账号或密码错误」（照 system login
// 的 Business 1004 信封）；注册唯一冲突是主动注册行为，409 明示「手机号已
// 存在」（与管理端 CreateMember 同文案——冲突本身就是注册方自证的存在性，
// 无需隐藏）。

// appLoginBizCode 是 C 端登录失败的业务码（照 system auth login 的 1004：
// HTTP 200 + 业务码，前端按 msg 提示；统一文案「账号或密码错误」）。
const appLoginBizCode = 1004

// appLoginFailedMsg 是登录失败的统一文案（不泄露存在性）。
const appLoginFailedMsg = "账号或密码错误"

// appLoginDummyHash 是固定 dummy bcrypt 哈希（原文 "mall-app-login-dummy"，
// DefaultCost 与 AppRegister 的哈希成本一致）：mobile 不存在时对它空跑一次
// CompareHashAndPassword 再回失败——登录响应的耗时口径与「账号存在但密码
// 错」恒等（真实路径是 bcrypt 比对，无 dummy 时不存在号直接短路返回，响应
// 时间差即 mobile 存在性枚举侧信道；恒时口径抹平该差，PR #184 审查 H1）。
// 常量固定而非运行时生成：启动成本为零，且哈希内容无保密需求（比对必败）。
const appLoginDummyHash = "$2a$10$/DOhY1x8/FaDAD7JIBIwF.cOgiwAi.8yT1hXJS79eccRSbKmWMwEK"

// AppRegister C 端注册（POST /mall/app/auth/register）：mobile/password/
// nickname → 自动建 member_user（status 启用，point_balance 0）+ 默认等级
// 「普通会员」（租户内 level=1 的启用等级，spec §3.1 种子行；无该行时落 0
// 未分配——等级纯手工维护域，注册不因主数据缺失而失败）+ 签发令牌（自动
// 登录，省一次往返）。mobile 租户内唯一：撞 UNIQUE(tenant_id, mobile) 409。
func (s *Service) AppRegister(ctx context.Context, req *dto.AppRegisterReq) (*dto.AppAuthResp, error) {
	ctx = tenant.WithTenantID(ctx, tenant.DefaultID)
	mobile := trimString(req.Mobile)
	if err := ValidateMemberMobile(mobile); err != nil {
		return nil, err
	}
	if err := ValidateMemberAppPassword(req.Password); err != nil {
		return nil, err
	}
	nickname := trimString(req.Nickname)
	if nickname == "" {
		// 昵称选填，缺省取手机号（与管理端 CreateMember 同款默认）。
		nickname = mobile
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, xerr.BadRequest("密码处理失败：" + err.Error())
	}
	// 默认等级「普通会员」按 level=1 查询（spec §7 裁决 2/T5 任务口径）：
	// 种子行「普通会员 level=1」在默认租户恒存在；管理员停用/删除后注册落
	// level_id=0（未分配，等级页可见可再指派），注册主流程不被主数据阻断。
	levelID := int64(0)
	level, err := s.repo.FindLevelByValue(ctx, 1)
	if err != nil {
		return nil, err
	}
	if level != nil && level.Status == model.StatusEnable {
		levelID = level.ID
	}
	row := &model.MemberUser{
		Mobile:       mobile,
		PasswordHash: string(hash),
		Nickname:     nickname,
		Status:       model.StatusEnable,
		LevelID:      levelID,
	}
	if err := s.repo.CreateMember(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("手机号已存在：" + mobile)
		}
		return nil, err
	}
	return s.appAuthResp(ctx, row)
}

// AppLogin C 端登录（POST /mall/app/auth/login）：mobile/password → 会员
// 令牌。失败统一「账号或密码错误」：mobile 不存在 / 密码不符 / 管理端建号
// 未设密码 / 会员停用四种形态同答（HTTP 200 + 业务码 1004，照 system login
// 信封）——停用会员若回「账号已停用」即泄露存在性，统一文案下停用与密码
// 错误不可区分。
//
// 恒时口径：mobile 不存在也做一次等成本 bcrypt 比对（appLoginDummyHash，
// DefaultCost 与真实哈希一致）——文案统一防的是响应内容侧信道，dummy 比对
// 防的是响应耗时侧信道，两者互补（PR #184 审查 H1）。空密码哈希（管理端
// 建号未设密）不 dummy 空跑：显式短路，与真实「密码错误」路径的耗时差被
// 限速（注册/登录 10 次/分/IP）压在不构成实用枚举信号的程度。
func (s *Service) AppLogin(ctx context.Context, req *dto.AppLoginReq) (*dto.AppAuthResp, error) {
	ctx = tenant.WithTenantID(ctx, tenant.DefaultID)
	mobile := trimString(req.Mobile)
	row, err := s.repo.FindMemberByMobile(ctx, mobile)
	if err != nil {
		return nil, err
	}
	if row == nil {
		// 不存在号：dummy 哈希空跑等成本比对后同答（恒时口径，见函数头）。
		_ = bcrypt.CompareHashAndPassword([]byte(appLoginDummyHash), []byte(req.Password))
		return nil, xerr.Business(appLoginBizCode, appLoginFailedMsg)
	}
	if row.PasswordHash == "" {
		// 管理端建号可不设密码（T1 口径）：无密码会员不能 C 端登录，
		// 与「密码错误」同答（bcrypt 对空哈希比对也必失败，这里显式短路）。
		return nil, xerr.Business(appLoginBizCode, appLoginFailedMsg)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(req.Password)); err != nil {
		return nil, xerr.Business(appLoginBizCode, appLoginFailedMsg)
	}
	if row.Status != model.StatusEnable {
		// 停用会员不给新令牌（统一文案不区分——不泄露存在性）。
		return nil, xerr.Business(appLoginBizCode, appLoginFailedMsg)
	}
	return s.appAuthResp(ctx, row)
}

// AppGetMe 我的信息（GET /mall/app/auth/me）：令牌自校验后的会员基本信息
// + 等级名 + point_balance。会员行在 memberAuth 中间件已验证存在且启用，
// 这里 404 是防御路径（令牌校验与请求处理之间会员被删——软删域不存在该
// 路径，保持同管理端 GetMember 的 404 口径）。
func (s *Service) AppGetMe(ctx context.Context, memberID int64) (*dto.AppMemberProfile, error) {
	row, err := s.repo.FindMemberByID(ctx, memberID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("会员不存在")
	}
	return s.memberProfile(ctx, row)
}

// appAuthResp 组装注册/登录响应：令牌 + 会员档案。
func (s *Service) appAuthResp(ctx context.Context, row *model.MemberUser) (*dto.AppAuthResp, error) {
	profile, err := s.memberProfile(ctx, row)
	if err != nil {
		return nil, err
	}
	token, expiresIn, err := s.IssueMemberToken(row)
	if err != nil {
		return nil, err
	}
	return &dto.AppAuthResp{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
		Member:      *profile,
	}, nil
}

// memberProfile 组装 C 端会员档案：基本信息 + 等级名（level_id 非零时反查，
// 跨租户/不存在落空串不报错——等级名是展示数据）+ point_balance。
func (s *Service) memberProfile(ctx context.Context, row *model.MemberUser) (*dto.AppMemberProfile, error) {
	levelName := ""
	if row.LevelID > 0 {
		level, err := s.repo.FindLevelByID(ctx, row.LevelID)
		if err != nil {
			return nil, err
		}
		if level != nil {
			levelName = level.Name
		}
	}
	return &dto.AppMemberProfile{
		ID:           row.ID,
		Mobile:       row.Mobile,
		Nickname:     row.Nickname,
		AvatarURL:    row.AvatarURL,
		LevelID:      row.LevelID,
		LevelName:    levelName,
		PointBalance: row.PointBalance,
		Status:       row.Status,
		CreatedAt:    row.CreatedAt,
	}, nil
}

// appPasswordMinLen/appPasswordMaxLen 是 C 端注册密码的长度界（6–32，
// issue #178 任务口径）：与管理端 ValidateMemberPassword（6–64）分立的
// 原因——管理端建号可设 64 位密码（T1 已收敛的单测口径不动），C 端自助
// 注册是新入口，按更紧的自助密码界收敛；登录是 bcrypt 比对不做长度校验，
// 管理端设置的更长密码不受影响。
const (
	appPasswordMinLen = 6
	appPasswordMaxLen = 32
)

// ValidateMemberAppPassword 校验 C 端注册密码：必填，6–32 位（rune 口径，
// 照 ValidateLevelName 的 utf8mb4 字符计——中文字符不被字节长度误伤）。
func ValidateMemberAppPassword(password string) error {
	trimmed := strings.TrimSpace(password)
	if trimmed == "" {
		return xerr.BadRequest("密码不能为空")
	}
	if utf8.RuneCountInString(trimmed) < appPasswordMinLen {
		return xerr.BadRequest("密码至少 6 位")
	}
	if utf8.RuneCountInString(trimmed) > appPasswordMaxLen {
		return xerr.BadRequest("密码不能超过 32 位")
	}
	return nil
}

// ---------- C 端收货地址自助（规格 §4；写入唯一入口，管理端 T1 只读）----------
// 上限对齐芋道 member_address 默认 20 条；归属谓词 (id, user_id) 全程携带，
// 越权/不存在统一 404 不泄露存在性（照 T6 订单越权口径）。

// appAddressLimit 是单会员收货地址上限。
const appAddressLimit = 20

// AppListAddresses 我的地址列表（默认置顶，上限内不分页）。
func (s *Service) AppListAddresses(ctx context.Context, memberID int64) ([]model.MemberAddress, error) {
	return s.repo.ListAllAddressesByUser(ctx, memberID)
}

// AppCreateAddress 新增收货地址：校验 → 上限 409 → 首地址自动默认 / 显式
// 默认时同事务先清旧默认（互斥不变量：单会员至多一个 is_default=1）。
func (s *Service) AppCreateAddress(ctx context.Context, memberID int64, req *dto.AppAddressSaveReq) (*model.MemberAddress, error) {
	if err := ValidateAddressInput(req.ReceiverName, req.ReceiverMobile, req.Province, req.City, req.District, req.Detail); err != nil {
		return nil, err
	}
	count, err := s.repo.CountAddressesByUser(ctx, memberID)
	if err != nil {
		return nil, err
	}
	if count >= appAddressLimit {
		return nil, xerr.Conflict(fmt.Sprintf("收货地址最多 %d 条，请先删除不需要的地址", appAddressLimit))
	}
	isDefault := req.IsDefault
	if count == 0 {
		isDefault = 1 // 首地址强制默认：结算页默认地址语义需要非空集
	}
	row := &model.MemberAddress{
		UserID:         memberID,
		ReceiverName:   req.ReceiverName,
		ReceiverMobile: req.ReceiverMobile,
		Province:       req.Province,
		City:           req.City,
		District:       req.District,
		Detail:         req.Detail,
		IsDefault:      isDefault,
	}
	err = s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if isDefault == 1 {
			if err := tx.ClearDefaultAddress(ctx, memberID); err != nil {
				return err
			}
		}
		return tx.CreateAddress(ctx, row)
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// AppUpdateAddress 更新收货地址：归属 404 → 校验 → 置默认时同事务清旧默认
// → 白名单列更新（is_default=0 的更新不清他人默认位——谓词只打本行）。
func (s *Service) AppUpdateAddress(ctx context.Context, memberID, id int64, req *dto.AppAddressSaveReq) error {
	if err := ValidateAddressInput(req.ReceiverName, req.ReceiverMobile, req.Province, req.City, req.District, req.Detail); err != nil {
		return err
	}
	if _, err := s.findAppAddress(ctx, memberID, id); err != nil {
		return err
	}
	fields := map[string]any{
		"receiver_name":   req.ReceiverName,
		"receiver_mobile": req.ReceiverMobile,
		"province":        req.Province,
		"city":            req.City,
		"district":        req.District,
		"detail":          req.Detail,
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if req.IsDefault == 1 {
			if err := tx.ClearDefaultAddress(ctx, memberID); err != nil {
				return err
			}
			fields["is_default"] = 1
		}
		rows, err := tx.UpdateAddressFields(ctx, memberID, id, fields)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.NotFound("收货地址不存在")
		}
		return nil
	})
}

// AppSetDefaultAddress 设默认地址：归属 404 → 同事务清旧默认 + 置本行
// （0 行 = 并发删除，报不存在）。
func (s *Service) AppSetDefaultAddress(ctx context.Context, memberID, id int64) error {
	if _, err := s.findAppAddress(ctx, memberID, id); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if err := tx.ClearDefaultAddress(ctx, memberID); err != nil {
			return err
		}
		rows, err := tx.SetDefaultAddressByID(ctx, memberID, id)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.NotFound("收货地址不存在")
		}
		return nil
	})
}

// AppDeleteAddress 删除收货地址：归属 404 → 软删（0 行 = 并发删除同答）；
// 删的是默认地址且仍有剩余时自动顶位最早一条（结算页「默认地址」语义需要
// 非空集）。删除与顶位包进同一事务（照 AppSetDefaultAddress 形状）——两步
// 是「默认地址互斥不变量」的原子迁移：非事务下删除与顶位之间出错、或与
// 并发 AppSetDefaultAddress 交错，可出现「会员无默认地址」或两个
// is_default=1，结算页默认地址取值随之漂移（PR #184 审查 P2）。
func (s *Service) AppDeleteAddress(ctx context.Context, memberID, id int64) error {
	row, err := s.findAppAddress(ctx, memberID, id)
	if err != nil {
		return err
	}
	wasDefault := row.IsDefault == 1
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		rows, err := tx.DeleteAddressByID(ctx, memberID, id)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.NotFound("收货地址不存在")
		}
		if wasDefault {
			if _, err := tx.PromoteOldestAddress(ctx, memberID); err != nil {
				return err
			}
		}
		return nil
	})
}

// findAppAddress 归属校验：地址不存在/跨会员/跨租户统一 404（租户插件隔离
// + user_id 比对双防线——插件漏登记 member_address 时不静默放行）。
func (s *Service) findAppAddress(ctx context.Context, memberID, id int64) (*model.MemberAddress, error) {
	row, err := s.repo.FindAddressByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.UserID != memberID {
		return nil, xerr.NotFound("收货地址不存在")
	}
	return row, nil
}
