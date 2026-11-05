// Package datascope 提供行级数据权限谓词引擎：把角色的数据范围（对齐芋道 1–5）
// 组合解析成「可见范围谓词」，供各模块按自身列名翻译成 SQL 条件或内存过滤。
//
// 最小用法：
//
//	filter, err := datascope.Resolve(ctx, subject, roleInputs, resolver)
//	if filter.All { /* 不加过滤 */ } else { /* dept_id IN filter.DeptIDs OR 自身列 = filter.UserID */ }
//
// 设计要点（见 docs/adr/0004-data-permission-model.md）：
//   - 多角色取可见部门并集；任一角色「全部」则整体全部可见（短路）；
//   - 「仅本人」输出 IncludeSelf + UserID，由调用方映射到自身归属列
//     （系统用户列表是 id，CRM 是 owner_user_id）；
//   - 无角色视为「仅本人」：宁可少看，绝不回退「全部」；
//   - 本包 HTTP 无关、不做任何存储访问，部门子树通过 DeptResolver 由宿主模块提供。
package datascope

import (
	"context"
	"fmt"
	"sort"
)

// Scope 是角色数据范围枚举，对齐芋道：1 全部 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人。
type Scope int8

const (
	// ScopeAll 全部数据。
	ScopeAll Scope = 1
	// ScopeCustom 自定义部门：可见 deptIDs 为角色配置的精确部门集合，不自动扩展子部门。
	ScopeCustom Scope = 2
	// ScopeDept 本部门：仅用户所属部门。
	ScopeDept Scope = 3
	// ScopeDeptAndSub 本部门及以下：用户所属部门及其子树。
	ScopeDeptAndSub Scope = 4
	// ScopeSelf 仅本人：按用户 ID 过滤。
	ScopeSelf Scope = 5
)

// Valid 报告该值是否为合法的数据范围枚举。
func (s Scope) Valid() bool { return s >= ScopeAll && s <= ScopeSelf }

// RoleInput 是单个角色的数据范围输入。
type RoleInput struct {
	Scope Scope
	// CustomDeptIDs 是 Scope == ScopeCustom 时的自定义部门集合，其他范围忽略。
	CustomDeptIDs []int64
}

// Subject 是数据主体（当前用户）。
type Subject struct {
	UserID int64
	DeptID int64 // 0 表示未分配部门，部门类范围不贡献可见集合
}

// DeptResolver 解析「本部门及以下」的部门子树，返回集合须含 deptID 自身。
// deptID <= 0 时应返回空集合；由宿主模块实现，本包不做存储访问。
type DeptResolver interface {
	DeptWithChildren(ctx context.Context, deptID int64) ([]int64, error)
}

// Filter 是行级数据权限谓词引擎的输出：与存储无关的可见范围描述。
//
//   - All 为 true：不做过滤，全部可见；
//   - 否则可见行 = (行部门 ∈ DeptIDs) OR (IncludeSelf 且行归属用户 == UserID)；
//   - DeptIDs 与 IncludeSelf 同时为空表示什么都看不到（fail-closed）。
//
// 调用方按自身表结构翻译：系统用户列表是 `dept_id IN (...) OR id = UserID`，
// CRM 主表则是 `owner_dept_id IN (...) OR owner_user_id = UserID`。
type Filter struct {
	All         bool
	DeptIDs     []int64 // 仅 !All 时有效；升序去重；空集合表示无可见部门
	IncludeSelf bool
	UserID      int64 // IncludeSelf 时使用
}

// Permits 判断一行（归属部门 deptID、归属用户 userID）是否可见，供内存过滤使用。
func (f Filter) Permits(deptID, userID int64) bool {
	if f.All {
		return true
	}
	if f.IncludeSelf && userID > 0 && userID == f.UserID {
		return true
	}
	for _, id := range f.DeptIDs {
		if id == deptID {
			return true
		}
	}
	return false
}

// Resolve 按多角色并集语义计算可见范围谓词：
// 任一角色「全部」则整体全部可见；否则可见部门取各角色并集；
// 任一角色「仅本人」则附加本人数据。无角色时回退「仅本人」（fail-closed）。
func Resolve(ctx context.Context, subject Subject, roles []RoleInput, resolver DeptResolver) (Filter, error) {
	if len(roles) == 0 {
		return Filter{IncludeSelf: true, UserID: subject.UserID}, nil
	}

	deptSet := make(map[int64]struct{})
	includeSelf := false
	for _, r := range roles {
		if !r.Scope.Valid() {
			return Filter{}, fmt.Errorf("datascope: 非法数据范围 %d", int8(r.Scope))
		}
		switch r.Scope {
		case ScopeAll:
			return Filter{All: true}, nil
		case ScopeCustom:
			for _, id := range r.CustomDeptIDs {
				if id > 0 {
					deptSet[id] = struct{}{}
				}
			}
		case ScopeDept:
			if subject.DeptID > 0 {
				deptSet[subject.DeptID] = struct{}{}
			}
		case ScopeDeptAndSub:
			ids, err := resolver.DeptWithChildren(ctx, subject.DeptID)
			if err != nil {
				return Filter{}, err
			}
			for _, id := range ids {
				if id > 0 {
					deptSet[id] = struct{}{}
				}
			}
		case ScopeSelf:
			includeSelf = true
		}
	}

	f := Filter{UserID: subject.UserID, IncludeSelf: includeSelf}
	if len(deptSet) > 0 {
		f.DeptIDs = make([]int64, 0, len(deptSet))
		for id := range deptSet {
			f.DeptIDs = append(f.DeptIDs, id)
		}
		sort.Slice(f.DeptIDs, func(i, j int) bool { return f.DeptIDs[i] < f.DeptIDs[j] })
	}
	return f, nil
}
