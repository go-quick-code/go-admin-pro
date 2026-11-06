<template>
  <el-cascader
    ref="cascaderRef"
    :model-value="modelValue"
    :props="cascaderProps"
    :placeholder="placeholder"
    :clearable="clearable"
    :disabled="disabled"
    :style="{ width }"
    @update:model-value="handleChange"
  />
</template>

<script setup lang="ts">
// AreaCascader 是可复用的行政区划级联选择器（issue #82，spec §4.8.3）：
// 经 /system/area/children 按 parentCode 逐层懒加载（空 parentCode 返回
// 省级），不一次性拉全量树。
//   - v-model：最终选中的区划 code（清空为空串）；
//   - change：{ code, codes, labels }——完整路径便于表单拆省/市/区。
import { computed, ref } from 'vue'
import { listAreaChildren } from '@/api/system'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    placeholder?: string
    clearable?: boolean
    disabled?: boolean
    width?: string
  }>(),
  { modelValue: '', placeholder: '请选择省 / 市 / 区', clearable: true, disabled: false, width: '100%' },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: [payload: { code: string; codes: string[]; labels: string[] }]
}>()

const cascaderRef = ref<any>()

// 层级上限 3（省/市/区县，spec §3.9）：level ≥ 3 的节点标记叶子；
// 市级之下若未导入区县，懒加载返回空数组，面板自然收口。
const cascaderProps = computed(() => ({
  lazy: true,
  checkStrictly: false,
  lazyLoad: async (node: any, resolve: (nodes: any[]) => void) => {
    try {
      const parentCode = node.level === 0 ? '' : (node.value as string)
      const list: any[] = (await listAreaChildren(parentCode)) || []
      const leaf = node.level + 1 >= 3
      resolve(list.map((item) => ({ value: item.code, label: item.name, leaf })))
    } catch {
      resolve([])
    }
  },
}))

function handleChange(value: any) {
  // 单选：value 是路径 code 数组；清空为 null。
  const codes: string[] = Array.isArray(value) ? value.map(String) : []
  const code = codes.length ? codes[codes.length - 1] : ''
  // 从面板勾选节点取展示路径；清空时节点列表为空。
  const checked = cascaderRef.value?.getCheckedNodes?.(false) || []
  const labels: string[] = checked.length ? (checked[0].pathLabels as string[]) : []
  emit('update:modelValue', code)
  emit('change', { code, codes, labels })
}

defineExpose({ props })
</script>
