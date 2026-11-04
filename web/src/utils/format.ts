export function formatTime(value?: string | null) {
  if (!value) return '-'
  return value.replace('T', ' ').replace(/\.\d+Z?$/, '').slice(0, 19)
}

export function formatSize(size?: number) {
  if (!size && size !== 0) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(2)} MB`
}

export function dictLabel(options: { label: string; value: any }[], value: any) {
  const hit = options.find((item) => String(item.value) === String(value))
  return hit ? hit.label : value
}

export const statusOptions = [
  { label: '启用', value: 0 },
  { label: '停用', value: 1 },
]

export const dataScopeOptions = [
  { label: '全部数据', value: 1 },
  { label: '本部门及以下', value: 2 },
  { label: '本部门', value: 3 },
  { label: '仅本人', value: 4 },
]

export const menuTypeOptions = [
  { label: '目录', value: 1 },
  { label: '菜单', value: 2 },
  { label: '按钮', value: 3 },
]

export const operateTypeOptions = [
  { label: '新增', value: 1 },
  { label: '修改', value: 2 },
  { label: '删除', value: 3 },
  { label: '查询', value: 4 },
  { label: '导出', value: 5 },
  { label: '导入', value: 6 },
]
