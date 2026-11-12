<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :before-submit="normalizeAmounts"
      perm-prefix="pay:wallet-recharge-package"
      title="充值套餐"
    >
      <template #toolbar>
        <!-- 充值单导出（issue #138，spec §4.12）：充值单管理列表无独立页面
            （#136 只建 822/823 两页），导出按钮随 822 菜单挂本页工具栏
           （pay:wallet-recharge:export 显隐；导出对象是充值单，非套餐）。 -->
        <ExcelActions
          title="充值单"
          export-perm="pay:wallet-recharge:export"
          :export-action="exportPayRecharges"
        />
      </template>
      <template #cell-payPrice="{ row }">
        <span class="amount">{{ row.payPrice }}</span>
      </template>
      <template #cell-bonusPrice="{ row }">
        <span class="amount bonus">+{{ row.bonusPrice }}</span>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
    </ProTable>

    <el-alert
      class="snapshot-tip"
      type="info"
      :closable="false"
      show-icon
      title="套餐档位在建充值单时快照进单：修改档位只影响后续新建充值单；被充值单引用的套餐禁止删除。"
    />
  </div>
</template>

<script setup lang="ts">
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageRechargePackages,
  getRechargePackage,
  createRechargePackage,
  updateRechargePackage,
  deleteRechargePackage,
  exportPayRecharges,
} from '@/api/pay'

// 充值套餐页（issue #136，spec §6/§7 菜单 822）：档位 CRUD（name/pay_price/
// bonus_price/status）。金额一律十进制字符串（后端 money 只接受 JSON 字符串，
// 精度不经 float）；快照语义见页内提示（spec 验收：改套餐不影响已建单）。
// 充值单导出（issue #138，spec §4.12）：管理端充值单列表导出入口挂本页
//（按钮 seed 8225 挂 822 菜单；自助「我的充值」不在导出清单内）。

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'name', label: '套餐名称', minWidth: 160 },
  { prop: 'payPrice', label: '实付档位', width: 120, slot: true },
  { prop: 'bonusPrice', label: '赠送档位', width: 120, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '套餐名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '套餐名称', required: true, placeholder: '如：充值 100 送 20' },
  {
    prop: 'payPrice',
    label: '实付档位',
    required: true,
    placeholder: '十进制金额（元），如 100.00',
  },
  { prop: 'bonusPrice', label: '赠送档位', placeholder: '十进制金额（元），如 20.00，缺省 0' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
]

const api = {
  page: pageRechargePackages,
  detail: getRechargePackage,
  create: createRechargePackage,
  update: updateRechargePackage,
  remove: deleteRechargePackage,
}

// 赠送档位缺省 0.00（后端 money 只接受十进制字符串，空串提交会被拒）。
function normalizeAmounts(payload: any) {
  return {
    ...payload,
    bonusPrice:
      payload.bonusPrice === '' || payload.bonusPrice === null || payload.bonusPrice === undefined
        ? '0.00'
        : payload.bonusPrice,
  }
}
</script>

<style scoped>
.amount {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.amount.bonus {
  color: var(--el-color-success);
}

.snapshot-tip {
  margin-top: 12px;
}
</style>
