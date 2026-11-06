// announcement_rule.go 承载公告域的核心判定规则（issue #48，
// docs/oa-mvp-spec.md §3.4/§4.7/§4.14）。全部为**纯函数**（无存储访问、
// 无 HTTP 上下文），表驱动测试锁定：
//
//   - 受众可见性：全员直接命中；自定义受众取所选部门子树与所选角色的并集，
//     命中任一即可见（spec §4.7）；
//   - 发布受众的收件人集合：所选部门子树用户 ∪ 所选角色用户（发布通知用）；
//   - 状态机：草稿 → 发布 → 撤回 → 重新发布；发布后不可编辑/删除，只能撤回，
//     草稿可编辑/删除（spec §4.7/§4.14）。
//
// 列表查询在仓储层用单一 EXISTS 谓词复刻同一语义（见
// repository/announcement.go 的 applyAudience），本文件的 Permits 供详情
// 判定与测试锁定规则，二者共享同一组常量（model.Announcement*）。
package service

import (
	"sort"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// Audience 是公告受众的存储无关描述：
//   - All 为 true：全员可见（DeptIDs/RoleIDs 无意义）；
//   - 否则 = 所选部门（含子树语义）与所选角色的并集，任一所选命中即可见。
//
// DeptIDs 存的是「被选中的部门 ID」：查看者所在部门在该部门子树内即命中，
// 因此判定时传查看者的祖先部门链（含自身）。
type Audience struct {
	All     bool
	DeptIDs []int64
	RoleIDs []int64
}

// AudienceViewer 是可见性判定的查看者输入（存储无关）：
//   - DeptIDs：查看者所在部门及其祖先部门 ID（含自身；部门链路缺失时为空）；
//   - RoleIDs：查看者持有的角色 ID。
type AudienceViewer struct {
	UserID  int64
	DeptIDs []int64
	RoleIDs []int64
}

// Permits 判断查看者是否命中受众（纯函数；spec §4.7 的并集语义）。
// 查看者无登录主体（UserID<=0）时 fail-closed 返回 false；自定义受众但
// 未选择任何目标时同样无人可见。
func (a Audience) Permits(v AudienceViewer) bool {
	if v.UserID <= 0 {
		return false
	}
	if a.All {
		return true
	}
	return intersects(a.DeptIDs, v.DeptIDs) || intersects(a.RoleIDs, v.RoleIDs)
}

// ResolveAudienceUserIDs 把受众展开成收件人集合（发布通知用，纯函数）：
// 全员时返回 allUserIDs；自定义时取所选部门子树用户与所选角色用户的并集。
// 输出升序去重，忽略非正 ID。
func ResolveAudienceUserIDs(all bool, deptSubtreeUserIDs, roleUserIDs, allUserIDs []int64) []int64 {
	if all {
		return uniqueSortedIDs(allUserIDs)
	}
	merged := make([]int64, 0, len(deptSubtreeUserIDs)+len(roleUserIDs))
	merged = append(merged, deptSubtreeUserIDs...)
	merged = append(merged, roleUserIDs...)
	return uniqueSortedIDs(merged)
}

// CanEditAnnouncement 报告公告是否可编辑（仅草稿，spec §5 PUT 说明）。
func CanEditAnnouncement(status int8) bool {
	return status == model.AnnouncementStatusDraft
}

// CanDeleteAnnouncement 报告公告是否可删除（仅草稿；发布后不可删除，
// 只能撤回，spec §4.7/§4.14）。
func CanDeleteAnnouncement(status int8) bool {
	return status == model.AnnouncementStatusDraft
}

// CanPublishAnnouncement 报告公告是否可发布/重新发布
// （草稿首次发布、撤回后重新发布）。
func CanPublishAnnouncement(status int8) bool {
	return status == model.AnnouncementStatusDraft || status == model.AnnouncementStatusWithdrawn
}

// CanWithdrawAnnouncement 报告公告是否可撤回（仅已发布）。
func CanWithdrawAnnouncement(status int8) bool {
	return status == model.AnnouncementStatusPublished
}

// intersects 报告两个 ID 集合是否有交集（空集合恒 false）。
func intersects(a, b []int64) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[int64]struct{}, len(a))
	for _, id := range a {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	for _, id := range b {
		if _, ok := set[id]; ok && id > 0 {
			return true
		}
	}
	return false
}

// uniqueSortedIDs 升序去重并忽略非正 ID。
func uniqueSortedIDs(ids []int64) []int64 {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
