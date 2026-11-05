package app

import (
	"context"

	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// bpmSystemGateway 把 system Service 适配成 bpm 的 SystemGateway（spec §4.2）：
// 审批人规则的引用存在性校验与运行期解析（issue #46）。system 的 Get* 对
// 缺失/停用返回 404/401 业务错误，「引用不存在」对 bpm 是合法的校验结论
// （false），只有基础设施错误才向上传播。
type bpmSystemGateway struct {
	sys *systemservice.Service
}

func (g bpmSystemGateway) ActiveUserExists(ctx context.Context, userID int64) (bool, error) {
	user, err := g.sys.GetActiveUser(ctx, userID)
	if err == nil {
		return user != nil, nil
	}
	if isReferenceMiss(err) {
		return false, nil
	}
	return false, err
}

func (g bpmSystemGateway) RoleExists(ctx context.Context, roleID int64) (bool, error) {
	role, err := g.sys.GetRole(ctx, roleID)
	if err == nil {
		return role != nil, nil
	}
	if isReferenceMiss(err) {
		return false, nil
	}
	return false, err
}

func (g bpmSystemGateway) DeptExists(ctx context.Context, deptID int64) (bool, error) {
	dept, err := g.sys.GetDept(ctx, deptID)
	if err == nil {
		return dept != nil, nil
	}
	if isReferenceMiss(err) {
		return false, nil
	}
	return false, err
}

// ---------- 运行期审批人解析（issue #46，spec §4.2）----------

func (g bpmSystemGateway) UserDeptID(ctx context.Context, userID int64) (int64, bool, error) {
	return g.sys.UserDeptID(ctx, userID)
}

func (g bpmSystemGateway) DeptLeaderUserID(ctx context.Context, deptID int64) (int64, bool, error) {
	return g.sys.DeptLeaderUserID(ctx, deptID)
}

func (g bpmSystemGateway) DeptParentID(ctx context.Context, deptID int64) (int64, bool, error) {
	return g.sys.DeptParentID(ctx, deptID)
}

func (g bpmSystemGateway) ActiveUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error) {
	return g.sys.ListActiveUserIDsByRole(ctx, roleID)
}

func (g bpmSystemGateway) ActiveUserIDsByPermission(ctx context.Context, permission string) ([]int64, error) {
	return g.sys.ListActiveUserIDsByPermission(ctx, permission)
}

func (g bpmSystemGateway) UserHasPermission(ctx context.Context, userID int64, permission string) (bool, error) {
	return g.sys.UserHasPermission(ctx, userID, permission)
}

func (g bpmSystemGateway) UserNicknames(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	return g.sys.UserNicknames(ctx, userIDs)
}

// isReferenceMiss 判断是否为「引用不存在/已停用」类业务错误（404/401）。
func isReferenceMiss(err error) bool {
	xe, ok := xerr.As(err)
	return ok && (xe.Status == 404 || xe.Status == 401)
}
