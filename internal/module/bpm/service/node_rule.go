// node_rule.go 承载审批人规则与节点结构的纯函数校验（issue #44，spec #42
// §3.1/§4.2）：保存与发布前的第一道关口，不依赖 DB——用户/角色/部门的存在性
// 校验在发布路径经 SystemGateway 完成（见 definition.go）。
package service

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 六种审批人规则常量定义在 model 包（与 Node 词汇同处，见 model.Assignee*）。
// 角色/权限码规则在运行期组成候选池（为每个候选人物化任务），其余规则
// 解析出单个人——解析逻辑属 #46。

// 节点结构边界：线性链 v1 的宽松上限，防手滑造出灾难定义。
const (
	MaxNodeCount     = 50
	MaxNodeName      = 64
	MaxAssigneeValue = 128
)

// assigneeTypeNames 是规则 → 中文说明，用于错误文案与运行期解析（#46）。
var assigneeTypeNames = map[string]string{
	model.AssigneeUser:                "指定用户",
	model.AssigneeRole:                "指定角色",
	model.AssigneePermission:          "权限码",
	model.AssigneeInitiator:           "发起人",
	model.AssigneeInitiatorDeptLeader: "发起人部门负责人",
	model.AssigneeDeptLeader:          "指定部门负责人",
}

// IsKnownAssigneeType 判断是否为已知审批人规则。
func IsKnownAssigneeType(assigneeType string) bool {
	_, ok := assigneeTypeNames[assigneeType]
	return ok
}

// ValidateNodes 校验节点数组的结构合法性（纯函数，表驱动测试锁定）：
//   - 至少 1 个、至多 MaxNodeCount 个节点（线性链）；
//   - 节点名称 1–64 字（去首尾空白后非空）；
//   - assigneeType 必须是六种规则之一；
//   - user/role 规则取值必须是数字 ID，permission 规则取值非空；
//   - dept_leader 规则必须携带 deptId。
//
// 引用存在性（用户/角色/部门）由发布路径的 DB 校验负责。
func ValidateNodes(nodes []model.Node) error {
	if len(nodes) == 0 {
		return xerr.BadRequest("流程定义至少需要一个审批节点")
	}
	if len(nodes) > MaxNodeCount {
		return xerr.BadRequest(fmt.Sprintf("审批节点数不能超过 %d 个", MaxNodeCount))
	}
	for i, node := range nodes {
		if err := validateNode(i, node); err != nil {
			return err
		}
	}
	return nil
}

// validateNode 校验单个节点；错误文案带节点序号（1 起）指明位置。
func validateNode(index int, node model.Node) error {
	position := fmt.Sprintf("第 %d 个节点", index+1)
	name := strings.TrimSpace(node.Name)
	if name == "" {
		return xerr.BadRequest(fmt.Sprintf("%s的节点名称不能为空", position))
	}
	if len([]rune(name)) > MaxNodeName {
		return xerr.BadRequest(fmt.Sprintf("%s的节点名称不能超过 %d 字", position, MaxNodeName))
	}
	if !IsKnownAssigneeType(node.AssigneeType) {
		return xerr.BadRequest(fmt.Sprintf("%s使用了未知的审批人规则 %q", position, node.AssigneeType))
	}
	value := strings.TrimSpace(node.AssigneeValue)
	if len([]rune(value)) > MaxAssigneeValue {
		return xerr.BadRequest(fmt.Sprintf("%s的审批人取值不能超过 %d 字", position, MaxAssigneeValue))
	}
	switch node.AssigneeType {
	case model.AssigneeUser:
		if value == "" {
			return xerr.BadRequest(fmt.Sprintf("%s（指定用户）需要填写用户 ID", position))
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return xerr.BadRequest(fmt.Sprintf("%s（指定用户）的用户 ID 必须是数字", position))
		}
	case model.AssigneeRole:
		if value == "" {
			return xerr.BadRequest(fmt.Sprintf("%s（指定角色）需要填写角色 ID", position))
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return xerr.BadRequest(fmt.Sprintf("%s（指定角色）的角色 ID 必须是数字", position))
		}
	case model.AssigneePermission:
		if value == "" {
			return xerr.BadRequest(fmt.Sprintf("%s（权限码）需要填写权限码", position))
		}
	case model.AssigneeDeptLeader:
		if node.DeptID <= 0 {
			return xerr.BadRequest(fmt.Sprintf("%s（指定部门负责人）需要选择部门", position))
		}
	case model.AssigneeInitiator, model.AssigneeInitiatorDeptLeader:
		// 无需取值；assigneeValue/deptId 忽略。
	}
	return nil
}

// NormalizeNodes 去除节点名称与取值的首尾空白（保存/发布前调用，
// 保证落库的 nodes 与校验口径一致）。
func NormalizeNodes(nodes []model.Node) []model.Node {
	out := make([]model.Node, len(nodes))
	for i, node := range nodes {
		node.Name = strings.TrimRightFunc(strings.TrimLeftFunc(node.Name, unicode.IsSpace), unicode.IsSpace)
		node.AssigneeValue = strings.TrimSpace(node.AssigneeValue)
		out[i] = node
	}
	return out
}

// AssigneeTypeName 返回规则的中文说明（未知规则回退原值；前端与错误文案复用）。
func AssigneeTypeName(assigneeType string) string {
	if name, ok := assigneeTypeNames[assigneeType]; ok {
		return name
	}
	return assigneeType
}
