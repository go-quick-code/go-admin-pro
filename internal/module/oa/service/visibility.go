// visibility.go 承载 OA 业务记录的共享可见范围判定（docs/oa-mvp-spec.md §4.6）：
//
//  1. 持有 `oa:<resource>:manage` → 全租户；
//  2. 当前用户是某部门负责人 → 该部门子树内用户的数据（多个部门取并集）；
//  3. 否则 → 仅本人。
//
// ResolveVisibility 是纯函数（无存储访问），锁定上述优先级与并集规则；
// 数据访问编排（权限码、部门子树、子树用户集合）由 VisibilityScopeOf 经
// SystemGateway 取数后喂给它。请假/报销/考勤等业务工单在拼列表谓词时
// 直接消费 VisibilityScope（All 不加条件，否则 owner_user_id IN UserIDs），
// 范围外/跨租户归属一律不可见并在详情路径表现为 404，不泄露存在性。
package service

import "sort"

// VisibilityScope 是可见范围的存储无关描述：
//   - All 为 true：全租户可见，不做归属过滤（UserIDs 无意义）；
//   - 否则可见行 = 归属用户 ∈ UserIDs（升序去重；空集合表示什么都看不到）。
type VisibilityScope struct {
	All     bool    `json:"all"`
	UserIDs []int64 `json:"userIds"`
}

// Permits 判断归属 ownerUserID 的记录是否可见（内存过滤用）。
// All 时不加过滤恒真；归属用户 0（无归属）只可能被 All 放行。
func (s VisibilityScope) Permits(ownerUserID int64) bool {
	if s.All {
		return true
	}
	for _, id := range s.UserIDs {
		if id == ownerUserID {
			return true
		}
	}
	return false
}

// ResolveVisibility 按可见范围优先级合成结果（纯函数，表驱动测试锁定）：
//
//   - hasManage（持有 oa:<resource>:manage，super_admin 以 "*" 通配）→ 全租户；
//   - 否则取各被负责部门子树内用户集合的并集；本人永远附加在内——
//     负责人可能负责 A 部门而归属 B 部门，「我的单据」不能因此消失；
//   - 都不命中则仅本人；selfUserID 非法（0，异常上下文）时输出空集合
//     （fail-closed，宁可少看）。
func ResolveVisibility(hasManage bool, ledSubtreeUserIDs [][]int64, selfUserID int64) VisibilityScope {
	if hasManage {
		return VisibilityScope{All: true}
	}

	set := make(map[int64]struct{})
	if selfUserID > 0 {
		set[selfUserID] = struct{}{}
	}
	for _, ids := range ledSubtreeUserIDs {
		for _, id := range ids {
			if id > 0 {
				set[id] = struct{}{}
			}
		}
	}

	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return VisibilityScope{UserIDs: out}
}

// visibilityResources 是登记了可见范围判定的 OA 资源（spec §4.6：请假/报销/考勤）。
// 公告走受众、日程走可见性、文档走目录 ACL，不经本 helper；后续工单如需
// 扩充（例如新业务记录），在此登记并沿用 oa:<resource>:manage 约定。
var visibilityResources = map[string]struct{}{
	"leave":      {},
	"expense":    {},
	"attendance": {},
}

// IsVisibilityResource 报告资源是否登记了可见范围判定。
func IsVisibilityResource(resource string) bool {
	_, ok := visibilityResources[resource]
	return ok
}

// ManagePermission 拼接资源的管理权限码 oa:<resource>:manage。
// 码值必须与菜单种子（后续工单的 500 段按钮）保持一致。
func ManagePermission(resource string) string {
	return "oa:" + resource + ":manage"
}
