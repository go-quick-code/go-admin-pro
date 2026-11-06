package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/datascope"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"golang.org/x/crypto/bcrypt"
)

// Service 汇聚 system 模块的业务能力。
type Service struct {
	repo *repository.Repository
	jwt  *jwt.Manager
	cfg  *config.Config

	permCache sync.Map // map[int64]permSnapshot
	pkgCache  sync.Map // map[int64]packageMenuSnapshot：租户套餐菜单快照
	failCache sync.Map // map[string]*loginFailState

	// dictUsageMu 保护 dictUsageCheckers：字典项引用检查扩展点
	//（issue #64 review，spec §4.14「被引用时只能停用」）。system 不反向依赖
	// 业务模块，由业务模块在 internal/app 装配期按 dictType 注册计数函数。
	dictUsageMu       sync.RWMutex
	dictUsageCheckers map[string]func(ctx context.Context, code string) (int64, error)
}

func New(repo *repository.Repository, jwtManager *jwt.Manager, cfg *config.Config) *Service {
	return &Service{repo: repo, jwt: jwtManager, cfg: cfg}
}

type permSnapshot struct {
	roles   []string
	codes   []string
	expires time.Time
}

// packageMenuSnapshot 是租户套餐允许的菜单集合快照。
// allowed == nil 表示不做套餐过滤（平台租户或未绑定套餐）；
// 非 nil 空集合表示套餐缺失时的 fail-closed 结果（该租户无任何菜单权限）。
type packageMenuSnapshot struct {
	allowed map[int64]struct{}
	expires time.Time
}

type loginFailState struct {
	count    int
	lockedAt time.Time
}

const (
	permCacheTTL = time.Minute
	pkgCacheTTL  = time.Minute
)

func (s *Service) Repo() *repository.Repository { return s.repo }

// CurrentUserID 返回 context 中的当前用户 ID。
func CurrentUserID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.UserID
	}
	return 0
}

// CurrentUsername 返回 context 中的当前用户名。
func CurrentUsername(ctx context.Context) string {
	if p, ok := authn.From(ctx); ok {
		return p.Username
	}
	return ""
}

// ParseToken 校验并解析 JWT。
func (s *Service) ParseToken(token string) (*jwt.Claims, error) {
	return s.jwt.Parse(token)
}

// HashPassword 生成 bcrypt 密码哈希。
func HashPassword(raw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码。
func CheckPassword(hash, raw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)) == nil
}

func (s *Service) TenantID(ctx context.Context) int64 {
	return tenant.ID(ctx)
}

// Permissions 返回用户的角色编码与权限码，带 1 分钟本地缓存。
func (s *Service) Permissions(ctx context.Context, userID int64) ([]string, []string, error) {
	if v, ok := s.permCache.Load(userID); ok {
		snap := v.(permSnapshot)
		if time.Now().Before(snap.expires) {
			return snap.roles, snap.codes, nil
		}
	}

	roles, err := s.repo.GetUserRoleCodes(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	roleModels, err := s.repo.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	roleIDs := make([]int64, 0, len(roleModels))
	for _, r := range roleModels {
		roleIDs = append(roleIDs, r.ID)
	}
	menus, err := s.repo.GetMenusByRoleIDs(ctx, roleIDs)
	if err != nil {
		return nil, nil, err
	}
	allowed, err := s.allowedMenuIDs(ctx, tenant.ID(ctx))
	if err != nil {
		return nil, nil, err
	}
	codes := make([]string, 0, len(menus))
	for _, m := range menus {
		if allowed != nil {
			if _, ok := allowed[m.ID]; !ok {
				continue
			}
		}
		if m.Permission != "" {
			codes = append(codes, m.Permission)
		}
	}
	// 只有平台租户（ID=1）的超管保留通配；其他租户超管按套餐/role_menu 取码
	//（ADR 0012，spec §4.1）。
	if IsSuperAdmin(roles) && tenant.ID(ctx) == tenant.DefaultID {
		codes = append(codes, "*")
	}

	s.permCache.Store(userID, permSnapshot{roles: roles, codes: codes, expires: time.Now().Add(permCacheTTL)})
	return roles, codes, nil
}

func (s *Service) ClearPermissionCache(userID int64) {
	s.permCache.Delete(userID)
}

func (s *Service) ClearAllPermissionCache() {
	s.permCache.Range(func(key, _ interface{}) bool {
		s.permCache.Delete(key)
		return true
	})
	s.pkgCache.Range(func(key, _ interface{}) bool {
		s.pkgCache.Delete(key)
		return true
	})
}

func (s *Service) logError(msg string, err error) {
	slog.Error(msg, "err", err)
}

// IsSuperAdmin 判断角色中是否包含超级管理员。
func IsSuperAdmin(roles []string) bool {
	for _, r := range roles {
		if r == "super_admin" {
			return true
		}
	}
	return false
}

// HasPermission 判断权限码是否满足。
func HasPermission(codes []string, required string) bool {
	for _, code := range codes {
		if code == "*" || code == required {
			return true
		}
		if strings.HasSuffix(code, ":*") && strings.HasPrefix(required, strings.TrimSuffix(code, "*")) {
			return true
		}
	}
	return false
}

// deptResolverAdapter 把 system 模块的部门子树查询适配成 datascope.DeptResolver。
type deptResolverAdapter struct{ s *Service }

func (a deptResolverAdapter) DeptWithChildren(ctx context.Context, deptID int64) ([]int64, error) {
	return a.s.deptWithChildren(ctx, deptID)
}

// DataScopeFilter 计算当前用户的数据范围谓词（internal/pkg/datascope 行级引擎，
// 供 system 与后续 CRM 复用）：多角色取可见部门并集，任一角色「全部」则全可见。
func (s *Service) DataScopeFilter(ctx context.Context, userID int64) (datascope.Filter, error) {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return datascope.Filter{}, err
	}
	if user == nil {
		// 用户不存在（异常上下文）：按仅本人处理，宁可少看。
		return datascope.Filter{IncludeSelf: true, UserID: userID}, nil
	}
	roles, err := s.repo.GetUserRoles(ctx, userID)
	if err != nil {
		return datascope.Filter{}, err
	}
	inputs := make([]datascope.RoleInput, 0, len(roles))
	for _, role := range roles {
		scope := datascope.Scope(role.DataScope)
		if role.Code == "super_admin" && tenant.ID(ctx) == tenant.DefaultID {
			// 只有平台租户的内置超管强制全部可见；租户超管按角色 data_scope
			//（引导时为全部），不能越过套餐语义（ADR 0012）。
			scope = datascope.ScopeAll
		}
		input := datascope.RoleInput{Scope: scope}
		if scope == datascope.ScopeCustom {
			if input.CustomDeptIDs, err = s.repo.GetRoleDeptIDs(ctx, role.ID); err != nil {
				return datascope.Filter{}, err
			}
		}
		inputs = append(inputs, input)
	}
	subject := datascope.Subject{UserID: userID, DeptID: user.DeptID}
	return datascope.Resolve(ctx, subject, inputs, deptResolverAdapter{s: s})
}

func (s *Service) deptWithChildren(ctx context.Context, deptID int64) ([]int64, error) {
	if deptID == 0 {
		return nil, nil
	}
	depts, err := s.repo.ListDepts(ctx, nil)
	if err != nil {
		return nil, err
	}
	children := make(map[int64][]int64, len(depts))
	for _, d := range depts {
		children[d.ParentID] = append(children[d.ParentID], d.ID)
	}
	result := make([]int64, 0, len(depts))
	queue := []int64{deptID}
	seen := map[int64]bool{deptID: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, cur)
		for _, child := range children[cur] {
			if seen[child] {
				continue
			}
			seen[child] = true
			queue = append(queue, child)
		}
	}
	return result, nil
}

// allowedMenuIDs 返回租户套餐允许的菜单 ID 集合（含按钮）。
//   - 平台租户 / 未绑定套餐：nil（不限制，兼容旧行为）；
//   - 非平台租户 + 套餐：选中菜单及其按钮；
//   - 套餐记录丢失：空集合（fail-closed），避免异常时反而放开全部权限。
//
// 结果按租户缓存 1 分钟；套餐/租户变更时 ClearAllPermissionCache 清空。
func (s *Service) allowedMenuIDs(ctx context.Context, tenantID int64) (map[int64]struct{}, error) {
	if tenantID <= 0 || tenantID == tenant.DefaultID {
		return nil, nil
	}
	if v, ok := s.pkgCache.Load(tenantID); ok {
		snap := v.(packageMenuSnapshot)
		if time.Now().Before(snap.expires) {
			return snap.allowed, nil
		}
	}

	t, err := s.repo.FindTenantByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if t == nil || t.PackageID == 0 {
		s.pkgCache.Store(tenantID, packageMenuSnapshot{allowed: nil, expires: time.Now().Add(pkgCacheTTL)})
		return nil, nil
	}
	pkg, err := s.repo.FindTenantPackageByID(ctx, t.PackageID)
	if err != nil {
		return nil, err
	}
	allowed := map[int64]struct{}{}
	if pkg != nil {
		menus, err := s.repo.ListMenus(ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range expandPackageMenuIDs(menus, pkg.MenuIDs) {
			allowed[id] = struct{}{}
		}
	}
	s.pkgCache.Store(tenantID, packageMenuSnapshot{allowed: allowed, expires: time.Now().Add(pkgCacheTTL)})
	return allowed, nil
}
