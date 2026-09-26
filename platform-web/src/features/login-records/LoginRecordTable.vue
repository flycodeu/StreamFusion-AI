<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { ElButton, ElPagination, ElTable, ElTableColumn, ElTag, vLoading } from 'element-plus'
import { getLoginRecords } from '../../api/login-records/api'
import type { LoginRecord } from '../../api/login-records/types'
import TablePanel from '../../components/table/TablePanel.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { useColumns } from '../../composables/table/useColumns'
import { loginEndReason, loginTime, sessionDuration } from './presentation'

const props = defineProps<{ userId?: string }>()
const options = [
  { key: 'loginAt', label: '登录时间' },
  { key: 'sourceIp', label: '登录IP' },
  { key: 'region', label: '地区' },
  { key: 'browser', label: '浏览器' },
  { key: 'os', label: '操作系统' },
  { key: 'lastActivityAt', label: '最后活动' },
  { key: 'duration', label: '会话时长' },
  { key: 'status', label: '会话状态' },
  { key: 'endReason', label: '结束原因' },
  { key: 'endedAt', label: '结束时间' },
]
const columns = useColumns(
  'login-records',
  options.slice(0, 9).map((item) => item.key),
)
const rows = ref<LoginRecord[]>([])
const page = ref(1)
const size = ref(20)
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
let requestId = 0
let controller: InstanceType<typeof globalThis.AbortController> | undefined

async function load() {
  const current = ++requestId
  controller?.abort()
  controller = new globalThis.AbortController()
  loading.value = true
  error.value = null
  try {
    const result = await getLoginRecords(
      props.userId,
      { page: page.value, size: size.value },
      controller.signal,
    )
    if (current !== requestId) return
    rows.value = result.items
    total.value = result.total
  } catch (cause) {
    if (current === requestId) error.value = cause
  } finally {
    if (current === requestId) loading.value = false
  }
}
watch(
  () => props.userId,
  () => {
    page.value = 1
    rows.value = []
    void load()
  },
  { immediate: true },
)
onUnmounted(() => {
  requestId++
  controller?.abort()
})
</script>

<template>
  <div class="login-records">
    <RequestError :error="error" />
    <TablePanel>
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
        <ColumnPicker v-model="columns" :options="options" />
      </template>
      <ElTable v-loading="loading" :data="rows" row-key="id" border empty-text="暂无登录记录">
        <ElTableColumn v-if="columns.includes('loginAt')" label="登录时间" min-width="175"
          ><template #default="{ row }">{{ loginTime(row.loginAt) }}</template></ElTableColumn
        >
        <ElTableColumn
          v-if="columns.includes('sourceIp')"
          prop="sourceIp"
          label="登录IP"
          min-width="140"
        />
        <ElTableColumn
          v-if="columns.includes('region')"
          prop="region"
          label="地区"
          min-width="125"
        />
        <ElTableColumn
          v-if="columns.includes('browser')"
          prop="browser"
          label="浏览器"
          min-width="130"
        />
        <ElTableColumn v-if="columns.includes('os')" prop="os" label="操作系统" min-width="125" />
        <ElTableColumn v-if="columns.includes('lastActivityAt')" label="最后活动" min-width="175"
          ><template #default="{ row }">{{
            loginTime(row.lastActivityAt)
          }}</template></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('duration')" label="会话时长" min-width="115"
          ><template #default="{ row }">{{
            sessionDuration(row.durationSeconds)
          }}</template></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('status')" label="会话状态" min-width="100"
          ><template #default="{ row }"
            ><ElTag :type="row.status === 'ACTIVE' ? 'success' : 'info'" effect="plain">{{
              row.status === 'ACTIVE' ? '有效' : row.status === 'EXPIRED' ? '已过期' : '已结束'
            }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('endReason')" label="结束原因" min-width="115"
          ><template #default="{ row }">{{
            loginEndReason(row.endReason)
          }}</template></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('endedAt')" label="结束时间" min-width="175"
          ><template #default="{ row }">{{ loginTime(row.endedAt) }}</template></ElTableColumn
        >
      </ElTable>
      <template #footer
        ><ElPagination
          v-model:current-page="page"
          v-model:page-size="size"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @change="load"
      /></template>
    </TablePanel>
  </div>
</template>
