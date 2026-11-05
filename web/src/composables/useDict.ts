import { reactive } from 'vue'
import { listDictDataByType } from '@/api/system'

export interface DictOption {
  label: string
  value: any
  colorType?: string
}

// 按字典类型加载启用中的字典项，返回可响应数组：异步填充后，列表列、
// 搜索项与表单项会自动刷新。加载失败时保留空数组，由 dictLabel 回退显示原值，
// 不阻塞页面。字典数据随登录租户返回，切换租户后刷新页面即可。
export function useDictOptions(dictType: string) {
  const options = reactive<DictOption[]>([])
  listDictDataByType(dictType)
    .then((rows: any[]) => {
      const items = (rows || []).map((row) => ({
        label: row.label,
        value: row.value,
        colorType: row.colorType,
      }))
      options.splice(0, options.length, ...items)
    })
    .catch(() => {
      // 字典不可用不阻塞页面：保留空选项，页面按原值展示。
    })
  return options
}
