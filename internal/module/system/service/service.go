package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/authn"
	"github.com/lautung/go-admin-pro/internal/pkg/jwt"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"golang.org/x/crypto/bcrypt"
)

// Service 汇聚 system 模块的业务能力。
type Service struct {
	repo *repository.Repository
	jwt  *jwt.Manager
	cfg  *config.Config

	permCache sync.Map // map[int64]permSnapshot
	failCache sync.Map // map[string]*loginFailState
}

func New(repo *repository.Repository, jwtManager *jwt.Manager, cfg *config.Config) *Service {
	return &Service{repo: repo, jwt: jwtManager, cfg: cfg}
}

type permSnapshot struct {
	roles   []string
	codes   []string
	expires time.Time
}

type loginFailState struct {
	count    int
	lockedAt time.Time
}

const permCacheTTL = time.Minute

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
	codes := make([]string, 0, len(menus))
	for _, m := range menus {
		if m.Permission != "" {
			codes = append(codes, m.Permission)
		}
	}
	if IsSuperAdmin(roles) {
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

// UserCenter 获取用户所属部门及允许访问的部门集合。
// deptIDs == nil 表示不限部门；onlySelf 表示仅本人数据。
func (s *Service) UserCenter(ctx context.Context, userID int64) (deptIDs []int64, onlySelf bool, err error) {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if user == nil {
		return nil, false, nil
	}
	roles, err := s.repo.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if len(roles) == 0 {
		return nil, true, nil
	}

	best := int8(model.DataScopeSelf)
	for _, role := range roles {
		if role.Code == "super_admin" || role.DataScope == model.DataScopeAll {
			return nil, false, nil
		}
		if role.DataScope < best {
			best = role.DataScope
		}
	}

	switch best {
	case model.DataScopeDeptAndSub:
		ids, err := s.deptWithChildren(ctx, user.DeptID)
		if err != nil {
			return nil, false, err
		}
		if len(ids) == 0 {
			return nil, true, nil
		}
		return ids, false, nil
	case model.DataScopeDept:
		if user.DeptID == 0 {
			return nil, true, nil
		}
		return []int64{user.DeptID}, false, nil
	default:
		return nil, true, nil
	}
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
