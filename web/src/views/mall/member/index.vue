<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="mall:member"
      title="会员管理"
      dialog-width="680px"
      @row-click="openDetail"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #cell-levelId="{ row }">
        {{ levelName(row.levelId) }}
      </template>
      <template #form-levelId="{ form }">
        <el-select v-model="form.levelId" clearable placeholder="未分配" style="width: 100%">
          <el-option v-for="opt in levelOptions" :key="opt.id" :label="opt.name" :value="opt.id" />
        </el-select>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button
          v-if="canUpdate"
          link
          :type="row.status === 0 ? 'warning' : 'success'"
          @click.stop="toggleStatus(row)"
        >
          {{ row.status === 0 ? '停用' : '启用' }}
        </el-button>
      </template>
    </ProTable>

    <!-- 会员详情抽屉：基本信息 + 收货地址 tab（spec §4「地址列表（按会员）」；
        地址只读，维护随 C 端自助）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.nickname || detail?.mobile || '会员详情'" size="760px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="ID">{{ detail.id }}</el-descriptions-item>
            <el-descriptions-item label="手机号">{{ detail.mobile }}</el-descriptions-item>
            <el-descriptions-item label="昵称">{{ detail.nickname || '-' }}</el-descriptions-item>
            <el-descriptions-item label="等级">{{ levelName(detail.levelId) }}</el-descriptions-item>
            <el-descriptions-item label="积分">{{ detail.pointBalance }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="detail.status === 0 ? 'success' : 'info'" size="small">
                {{ detail.status === 0 ? '启用' : '停用' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="头像">{{ detail.avatarUrl || '-' }}</el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="收货地址" name="addresses">
          <el-table :data="detail.addresses || []" border stripe>
            <el-table-column label="收货人" prop="receiverName" min-width="90" />
            <el-table-column label="手机号" prop="receiverMobile" min-width="120" />
            <el-table-column label="地区" min-width="150">
              <template #default="{ row }">
                {{ [row.province, row.city, row.district].filter(Boolean).join(' / ') }}
              </template>
            </el-table-column>
            <el-table-column label="详细地址" prop="detail" min-width="180" show-overflow-tooltip />
            <el-table-column label="默认" width="70">
              <template #default="{ row }">
                <el-tag v-if="row.isDefault === 1" type="success" size="small">默认</el-tag>
                <span v-else>-</span>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-if="!(detail.addresses || []).length" description="暂无收货地址" :image-size="80" />
        </el-tab-pane>
      </el-tabs>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageMembers,
  getMember,
  createMember,
  updateMember,
  updateMemberStatus,
  listLevelOptions,
} from '@/api/mall'

// 会员管理页（issue #174，spec §4/§6）：ProTable 列表 + 详情抽屉（基本信息 /
// 收货地址两个 tab）。mobile 租户内唯一（后端 409）；密码选填（编辑留空 =
// 不改密码）；停启用走独立端点（mall:member:update），无删除（业务身份禁删，
// 生命周期由停启用覆盖）。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('mall:member:query'))
const canUpdate = computed(() => userStore.hasPerm('mall:member:update'))

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'mobile', label: '手机号', minWidth: 130 },
  { prop: 'nickname', label: '昵称', minWidth: 130 },
  { prop: 'levelId', label: '等级', width: 110, slot: true },
  { prop: 'pointBalance', label: '积分', width: 90 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 130 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'mobile', label: '手机号' },
  { prop: 'nickname', label: '昵称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'mobile', label: '手机号', required: true, placeholder: '租户内唯一，11 位大陆手机号' },
  { prop: 'password', label: '密码', type: 'password', placeholder: '选填；编辑留空 = 不改密码（6-64 位）' },
  { prop: 'nickname', label: '昵称', placeholder: '缺省取手机号' },
  { prop: 'levelId', label: '等级', slot: true },
  { prop: 'avatarUrl', label: '头像 URL' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageMembers, create: createMember, update: updateMember }

// ---------- 等级下拉（启用等级清单，登录 + mall:member:query） ----------

const levelOptions = ref<any[]>([])
const levelName = (id: number) => {
  const hit = levelOptions.value.find((opt) => opt.id === id)
  return hit ? hit.name : id ? `等级 ${id}` : '-'
}

onMounted(async () => {
  try {
    levelOptions.value = (await listLevelOptions()) || []
  } catch {
    levelOptions.value = []
  }
})

// ---------- 停启用 ----------

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateMemberStatus(row.id, next)
  ElMessage.success(next === 0 ? '已启用' : '已停用')
}

// ---------- 详情抽屉 ----------

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')

async function openDetail(row: any) {
  const data = await getMember(row.id)
  detail.value = data
  detailVisible.value = true
}
</script>
