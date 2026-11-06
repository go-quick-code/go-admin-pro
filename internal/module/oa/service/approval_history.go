package service

import bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"

// currentInstanceIndex 返回历次审批实例中「当前实例」的下标（spec §4.3：
// 每次重新提交创建新实例，业务详情倒序展示历次审批尝试）。优先匹配业务行
// 保存的 approval_instance_id；缺失或未命中时退化为列表首位——bpm 的
// ListInstanceDetailsForBusiness 按 id 倒序返回，首位即最新实例。
func currentInstanceIndex(approvals []bpmdto.InstanceDetail, approvalInstanceID *int64) int {
	if approvalInstanceID != nil {
		for i := range approvals {
			if approvals[i].Instance != nil && approvals[i].Instance.ID == *approvalInstanceID {
				return i
			}
		}
	}
	return 0
}
