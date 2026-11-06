// 审批记录展示工具（issue #64 code-review P2：四处逐字重复的 recordText/
// recordTag 抽到此处，bpm 待办、请假、报销、合同详情共用）。
//
// 动作编号与 bpm 模型一致：1 通过 / 2 驳回 / 3 撤回 / 4 转办。

const RECORD_TEXTS = ['通过', '驳回', '撤回', '转办']

export function recordText(action: number): string {
  return RECORD_TEXTS[action - 1] ?? `未知(${action})`
}

export function recordTag(action: number): string {
  if (action === 1) return 'success'
  if (action === 2) return 'danger'
  return 'warning'
}

// 业务详情里的实例时间线：后端详情同时返回 approvals（历次实例，倒序，
// current 标记当前实例）与 approval（当前实例的便捷引用）。老数据/单实例
// 场景只有 approval 时退化为单元素数组。
export function approvalInstancesOf(detail: any): any[] {
  const approvals = Array.isArray(detail?.approvals) ? detail.approvals : []
  if (approvals.length) return approvals
  return detail?.approval ? [detail.approval] : []
}

// 当前实例：优先 current 标记，其次首个（后端倒序返回，最新在前）。
export function currentApprovalOf(detail: any): any | null {
  const instances = approvalInstancesOf(detail)
  if (!instances.length) return null
  return instances.find((item) => item?.current) ?? instances[0]
}
