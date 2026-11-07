<template>
  <el-tree
    ref="treeRef"
    :data="data"
    show-checkbox
    check-strictly
    node-key="id"
    :props="treeProps"
    default-expand-all
    class="menu-tree-select"
    @check="handleCheck"
  />
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue?: number[]
    data?: any[]
  }>(),
  { modelValue: () => [], data: () => [] },
)

const emit = defineEmits<{ 'update:modelValue': [value: number[]] }>()

const treeRef = ref<any>()
const treeProps = { label: 'name', children: 'children' }

watch(
  () => [props.modelValue, props.data],
  async () => {
    await nextTick()
    treeRef.value?.setCheckedKeys(props.modelValue || [], false)
  },
  { immediate: true, deep: true },
)

function handleCheck() {
  const checked = (treeRef.value?.getCheckedKeys() as number[]) || []
  // 套餐必须包含目录祖先：勾选子菜单时补齐父目录；
  // check-strictly 保证不会误勾同目录下的兄弟菜单。
  const result = new Set(checked)
  for (const id of checked) {
    let node = treeRef.value?.getNode(id)
    while (node?.parent?.data?.id) {
      result.add(node.parent.data.id)
      node = node.parent
    }
  }
  emit('update:modelValue', Array.from(result))
}
</script>

<style scoped>
.menu-tree-select {
  width: 100%;
  max-height: 280px;
  overflow: auto;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  padding: 4px;
}
</style>
