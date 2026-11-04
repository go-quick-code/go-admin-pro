<template>
  <el-sub-menu v-if="item.type === 1" :index="fullPath">
    <template #title>
      <el-icon v-if="item.icon"><component :is="item.icon" /></el-icon>
      <span>{{ item.name }}</span>
    </template>
    <SidebarItem
      v-for="child in item.children"
      :key="child.id"
      :item="child"
      :parent-path="fullPath"
    />
  </el-sub-menu>

  <el-menu-item v-else :index="fullPath">
    <el-icon v-if="item.icon"><component :is="item.icon" /></el-icon>
    <template #title>{{ item.name }}</template>
  </el-menu-item>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  item: any
  parentPath?: string
}>()

const fullPath = computed(() => {
  const path = props.item.path || ''
  if (!path) return props.parentPath || '/'
  if (path.startsWith('/')) return path
  const parent = props.parentPath || ''
  return `${parent.replace(/\/$/, '')}/${path}`.replace(/\/+/g, '/')
})
</script>
