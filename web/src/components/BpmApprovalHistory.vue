<template>
  <div class="bpm-approval-history">
    <!-- spec §4.3：同一业务记录保留全部历史实例，详情按时间倒序展示历次
         审批尝试。后端 approvals 已按 id 倒序（最新在前），默认展开当前/最新
         一次，历史可折叠。 -->
    <el-collapse v-if="approvals.length" v-model="activeNames">
      <el-collapse-item
        v-for="(item, index) in approvals"
        :key="item.instance?.id ?? index"
        :name="String(item.instance?.id ?? index)"
      >
        <template #title>
          <div class="history-title">
            <span class="history-round">第 {{ approvals.length - index }} 次审批</span>
            <el-tag v-if="item.current" type="primary" size="small" effect="dark">当前</el-tag>
            <el-tag :type="instanceStatusTag(item.instance?.status)" size="small">
              {{ instanceStatusText(item.instance?.status) }}
            </el-tag>
            <span v-if="item.instance?.createTime" class="history-time">
              {{ formatTime(item.instance.createTime) }}
            </span>
          </div>
        </template>
        <el-timeline v-if="item.records?.length" class="timeline">
          <el-timeline-item
            v-for="rec in item.records"
            :key="rec.id"
            :timestamp="formatTime(rec.createTime)"
            :type="timelineType(rec.action)"
          >
            <div>
              <el-tag :type="recordTag(rec.action)" size="small">{{ recordText(rec.action) }}</el-tag>
              <span class="timeline-operator">{{ rec.operatorName || `用户#${rec.operatorId}` }}</span>
            </div>
            <div class="timeline-node">{{ rec.nodeName || '（实例级动作）' }}</div>
            <div v-if="rec.comment" class="timeline-comment">{{ rec.comment }}</div>
          </el-timeline-item>
        </el-timeline>
        <div v-else class="history-empty">暂无审批记录（等待首个审批动作）</div>
      </el-collapse-item>
    </el-collapse>
    <el-empty v-else :description="emptyText" :image-size="60" />
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { formatTime } from '@/utils/format'
import { recordTag, recordText } from '@/utils/bpm-record'

// 审批历史时间线（issue #64 code-review P1）：请假/报销/合同详情共用。
// approvals 由后端按实例倒序返回，每项含 instance/records/tasks/current；
// 无历史时退化为 el-empty。
const props = withDefaults(
  defineProps<{
    approvals: any[]
    emptyText?: string
  }>(),
  { emptyText: '尚未发起审批' },
)

// 默认展开最新一次（若父级传入的当前实例不是首个，则展开 current）。
// 详情是异步加载的，use watch 而不是初始化一次：数据到达前 approvals 为空，
// 挂载后 prop 变化时再定位 current。
const activeNames = ref<string[]>([])
watch(
  () => props.approvals,
  (list) => {
    if (!list?.length) {
      activeNames.value = []
      return
    }
    const current = list.find((item) => item?.current) ?? list[0]
    const id = current?.instance?.id
    activeNames.value = id != null ? [String(id)] : []
  },
  { immediate: true },
)

function instanceStatusText(status: number): string {
  return ['运行中', '通过', '驳回', '撤回'][status] ?? `未知(${status})`
}

function instanceStatusTag(status: number): string {
  if (status === 1) return 'success'
  if (status === 2 || status === 3) return 'danger'
  return 'primary'
}

function timelineType(action: number): 'success' | 'danger' | 'primary' {
  const tag = recordTag(action)
  if (tag === 'success') return 'success'
  if (tag === 'danger') return 'danger'
  return 'primary'
}
</script>

<style scoped>
.bpm-approval-history {
  margin-top: 8px;
}
.history-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.history-round {
  color: var(--el-text-color-primary);
}
.history-time {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.history-empty {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  padding: 4px 0;
}
.timeline {
  padding-left: 4px;
}
.timeline-operator {
  margin-left: 8px;
  color: var(--el-text-color-regular);
}
.timeline-node {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.timeline-comment {
  margin-top: 4px;
  color: var(--el-text-color-primary);
  white-space: pre-wrap;
}
</style>
