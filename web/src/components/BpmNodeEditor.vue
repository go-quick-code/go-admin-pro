<template>
  <div class="node-editor">
    <el-table :data="nodes" border size="small" row-key="rowKey">
      <el-table-column label="顺序" width="70" align="center">
        <template #default="{ $index }">
          <span>{{ $index + 1 }}</span>
        </template>
      </el-table-column>
      <el-table-column label="节点名称" min-width="140">
        <template #default="{ row }">
          <el-input v-model="row.name" maxlength="64" placeholder="如 部门负责人审批" />
        </template>
      </el-table-column>
      <el-table-column label="审批人规则" width="170">
        <template #default="{ row }">
          <el-select v-model="row.assigneeType" placeholder="请选择规则" @change="onTypeChange(row)">
            <el-option v-for="opt in typeOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="审批人取值" min-width="180">
        <template #default="{ row }">
          <!-- 指定用户/角色：下拉选择（值存 ID）；权限码：输入码；其余规则无需取值 -->
          <el-select
            v-if="row.assigneeType === 'user'"
            v-model="row.assigneeValue"
            filterable
            clearable
            placeholder="请选择用户"
          >
            <el-option v-for="u in users" :key="u.id" :label="`${u.nickname}(${u.id})`" :value="String(u.id)" />
          </el-select>
          <el-select
            v-else-if="row.assigneeType === 'role'"
            v-model="row.assigneeValue"
            filterable
            clearable
            placeholder="请选择角色"
          >
            <el-option v-for="r in roles" :key="r.id" :label="`${r.name}(${r.id})`" :value="String(r.id)" />
          </el-select>
          <el-input
            v-else-if="row.assigneeType === 'permission'"
            v-model="row.assigneeValue"
            maxlength="128"
            placeholder="如 crm:contract:approve"
          />
          <el-tree-select
            v-else-if="row.assigneeType === 'dept_leader'"
            v-model="row.deptId"
            :data="depts"
            :props="{ label: 'name', value: 'id', children: 'children' }"
            check-strictly
            clearable
            placeholder="请选择部门"
            style="width: 100%"
          />
          <span v-else class="rule-hint">{{ ruleHint(row.assigneeType) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" align="center">
        <template #default="{ $index }">
          <el-button link type="primary" :disabled="$index === 0" @click="move($index, -1)">上移</el-button>
          <el-button link type="primary" :disabled="$index === nodes.length - 1" @click="move($index, 1)">
            下移
          </el-button>
          <el-button link type="danger" @click="remove($index)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-button class="add-btn" type="primary" plain size="small" @click="add">添加节点</el-button>
  </div>
</template>

<script setup lang="ts">
// BpmNodeEditor 是流程定义的有序节点编辑器（issue #44，spec §4.2）：
// 节点按行编辑、可上下移（线性审批链的顺序即语义）；六种审批人规则中
// 指定用户/角色下拉选择（存 ID 字符串）、权限码手输、指定部门负责人选部门、
// 发起人/发起人部门负责人无需取值。引用存在性由后端发布时校验。
import { onMounted, ref, watch } from 'vue'
import { listSimpleUsers, listSimpleRoles, treeDepts } from '@/api/system'

interface BpmNode {
  name: string
  assigneeType: string
  assigneeValue: string
  deptId?: number
}

const props = defineProps<{ modelValue?: BpmNode[] }>()
const emit = defineEmits<{ 'update:modelValue': [nodes: BpmNode[]] }>()

const typeOptions = [
  { label: '指定用户', value: 'user' },
  { label: '指定角色（候选池）', value: 'role' },
  { label: '权限码（候选池）', value: 'permission' },
  { label: '发起人', value: 'initiator' },
  { label: '发起人部门负责人', value: 'initiator_dept_leader' },
  { label: '指定部门负责人', value: 'dept_leader' },
]

const users = ref<any[]>([])
const roles = ref<any[]>([])
const depts = ref<any[]>([])

const nodes = ref<BpmNode[]>([])
let syncing = false

function emptyNode(): BpmNode {
  return { name: '', assigneeType: 'user', assigneeValue: '' }
}

function ruleHint(type: string): string {
  if (type === 'initiator') return '发起人本人办理'
  if (type === 'initiator_dept_leader') return '发起人部门负责人，空缺沿部门树向上'
  return ''
}

function sync(next: BpmNode[]) {
  nodes.value = next
  emit('update:modelValue', next.map((n) => ({ ...n })))
}

watch(
  () => props.modelValue,
  (value) => {
    // 外部（编辑回填）变化时整体重建；编辑器自身触发的 emit 不回灌。
    const incoming = value || []
    if (JSON.stringify(incoming) !== JSON.stringify(nodes.value)) {
      nodes.value = incoming.map((n) => ({ ...n }))
    }
  },
  { immediate: true },
)

function add() {
  sync([...nodes.value, emptyNode()])
}
function remove(index: number) {
  const next = nodes.value.filter((_, i) => i !== index)
  sync(next)
}
function move(index: number, delta: number) {
  const next = [...nodes.value]
  const target = index + delta
  if (target < 0 || target >= next.length) return
  ;[next[index], next[target]] = [next[target], next[index]]
  sync(next)
}
function onTypeChange(row: BpmNode) {
  // 切换规则时清空旧取值，避免把用户 ID 留给权限码规则。
  row.assigneeValue = ''
  row.deptId = undefined
  sync([...nodes.value])
}

onMounted(async () => {
  if (!nodes.value.length) {
    nodes.value = props.modelValue?.length ? props.modelValue.map((n) => ({ ...n })) : [emptyNode()]
    // 新建弹窗初始就同步一个空节点行，避免「未编辑即提交」拿到 undefined。
    emit('update:modelValue', nodes.value.map((n) => ({ ...n })))
  }
  // 下拉数据缺失时静默降级为空选项（无 system:user:simple-list 权限的
  // 管理员仍可手输场景有限，后端发布校验兜底）。
  try {
    users.value = (await listSimpleUsers()) || []
  } catch {
    users.value = []
  }
  try {
    roles.value = (await listSimpleRoles()) || []
  } catch {
    roles.value = []
  }
  try {
    depts.value = (await treeDepts()) || []
  } catch {
    depts.value = []
  }
})
</script>

<style scoped>
.node-editor {
  width: 100%;
}
.add-btn {
  margin-top: 8px;
}
.rule-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
