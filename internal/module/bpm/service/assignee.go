// assignee.go 承载审批人解析（issue #46，spec #42 §4.2）：六种规则把节点
// 翻译为办理人集合——角色/权限码规则组成候选池（每人一条待办），其余规则
// 解析出单个人。解析为纯函数：SystemGateway 提供部门树/角色/权限码数据，
// 规则语义由表驱动测试锁定（assignee_test.go）。
//
// 统一语义：
//   - 停用用户不能作为审批人（#44 网关注释口径）：候选池由网关预过滤；
//     指定用户/部门负责人命中的用户再经 ActiveUserExists 复核，停用视为
//     空缺（部门负责人继续向上，指定用户直接解析失败）；
//   - 候选人去重、升序排序（任务物化顺序确定，集成用例可断言）；
//   - 解析结果为空 → 400 中文错误，不跳过节点、不自动通过（spec §4.2）；
//   - 允许发起人成为自己的审批人（自审批，v1 不自动跳过）。
package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// maxDeptWalkDepth 是部门树向上查找的深度上限：防御脏数据成环把解析变成死循环。
// 正常部门树深度远小于该值；触顶按「到根仍为空」处理（解析失败）。
const maxDeptWalkDepth = 32

// ResolveAssignees 按节点规则解析办理人集合（升序去重）。
// initiatorID 是实例发起人（initiator 规则与发起人部门规则使用）。
func ResolveAssignees(ctx context.Context, sys SystemGateway, node model.Node, initiatorID int64) ([]int64, error) {
	ids, err := resolveAssignees(ctx, sys, node, initiatorID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("节点「%s」解析不到审批人，无法提交（不跳过节点、不自动通过）", node.Name))
	}
	// 去重 + 升序：候选池物化与断言的确定性基础。
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	dedup := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			dedup = append(dedup, id)
		}
	}
	return dedup, nil
}

func resolveAssignees(ctx context.Context, sys SystemGateway, node model.Node, initiatorID int64) ([]int64, error) {
	switch node.AssigneeType {
	case model.AssigneeUser:
		// 指定用户：单人；发布期已校验存在性，运行期复核启用性（停用即失败）。
		userID, err := strconv.ParseInt(node.AssigneeValue, 10, 64)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("节点「%s」指定用户取值非法：%s", node.Name, node.AssigneeValue))
		}
		ok, err := sys.ActiveUserExists(ctx, userID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return []int64{userID}, nil

	case model.AssigneeRole:
		// 指定角色：该角色启用用户组成候选池（网关预过滤停用用户）。
		roleID, err := strconv.ParseInt(node.AssigneeValue, 10, 64)
		if err != nil {
			return nil, xerr.BadRequest(fmt.Sprintf("节点「%s」指定角色取值非法：%s", node.Name, node.AssigneeValue))
		}
		return sys.ActiveUserIDsByRole(ctx, roleID)

	case model.AssigneePermission:
		// 权限码：持有人组成候选池（super_admin 视为持有全部，网关口径同权限中间件）。
		return sys.ActiveUserIDsByPermission(ctx, node.AssigneeValue)

	case model.AssigneeInitiator:
		// 发起人本人：单节点单人，自审批 v1 允许（spec §4.2）。
		return []int64{initiatorID}, nil

	case model.AssigneeInitiatorDeptLeader:
		// 发起人部门负责人：从发起人部门沿 parent_id 向上找 leader_user_id > 0
		// 且启用的部门；到根仍为空则失败。
		deptID, ok, err := sys.UserDeptID(ctx, initiatorID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return resolveDeptLeader(ctx, sys, node, deptID)

	case model.AssigneeDeptLeader:
		// 指定部门负责人：从 DeptID 沿 parent_id 向上查找。
		if node.DeptID <= 0 {
			return nil, nil
		}
		return resolveDeptLeader(ctx, sys, node, node.DeptID)

	default:
		return nil, xerr.BadRequest(fmt.Sprintf("节点「%s」审批人规则非法：%s", node.Name, node.AssigneeType))
	}
}

// resolveDeptLeader 从 deptID 沿部门树向上查找「负责人存在且启用」的部门。
// 停用负责人视为空缺（停用用户不能作为审批人），继续向上；到根仍为空
// 返回空集合（上层翻译为 400）。
func resolveDeptLeader(ctx context.Context, sys SystemGateway, node model.Node, deptID int64) ([]int64, error) {
	current := deptID
	for depth := 0; depth < maxDeptWalkDepth; depth++ {
		leader, ok, err := sys.DeptLeaderUserID(ctx, current)
		if err != nil {
			return nil, err
		}
		if ok {
			active, err := sys.ActiveUserExists(ctx, leader)
			if err != nil {
				return nil, err
			}
			if active {
				return []int64{leader}, nil
			}
		}
		parent, ok, err := sys.DeptParentID(ctx, current)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		current = parent
	}
	// 触发深度上限：按到根仍为空处理。
	return nil, nil
}
