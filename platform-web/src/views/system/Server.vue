<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import { getServerStatus } from '../../api/server/api'
import type { ServerSnapshot } from '../../api/server/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'SystemServer' })
const snapshot = ref<ServerSnapshot | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)
const statusNames: Record<string, string> = {
  RUNNING: '运行中',
  REACHABLE: '端口可达',
  UNREACHABLE: '端口不可达',
  NOT_CONFIGURED: '未配置',
  UNAVAILABLE: '不可用',
}
async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    snapshot.value = await getServerStatus()
  } catch (cause) {
    error.value = cause
  } finally {
    loading.value = false
  }
}
function percent(value?: number | null): string {
  return value == null ? '不可用' : `${value.toFixed(1)}%`
}
function bytes(value?: number | null): string {
  return value == null || value < 0 ? '不可用' : `${(value / 1024 ** 3).toFixed(2)} GB`
}
function uptime(value?: number): string {
  if (value == null) return '不可用'
  const minutes = Math.floor(value / 60000)
  return `${Math.floor(minutes / 1440)}天 ${Math.floor(minutes / 60) % 24}小时 ${minutes % 60}分钟`
}
onMounted(load)
</script>

<template>
  <div class="content-page server-page">
    <div class="server-toolbar">
      <span>采集时间：{{ formatDateTime(snapshot?.sampledAt) }}</span
      ><ElButton type="primary" :loading="loading" @click="load">刷新</ElButton>
    </div>
    <RequestError :error="error" />
    <ElAlert
      v-if="snapshot?.stale"
      :title="snapshot.sampledAt ? '采集暂不可用，当前显示上次结果' : '采集暂不可用，请稍后刷新'"
      type="warning"
      :closable="false"
      show-icon
    />
    <div v-loading="loading && !snapshot" class="server-metrics">
      <section class="server-section">
        <h2>运行环境</h2>
        <ElDescriptions :column="1" border>
          <ElDescriptionsItem label="操作系统">{{
            snapshot?.host?.os || '不可用'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="架构">{{
            snapshot?.host?.architecture || '不可用'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="逻辑处理器">{{
            snapshot?.host?.processors ?? '不可用'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="CPU使用率">{{
            percent(snapshot?.host?.cpuPercent)
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="内存使用"
            >{{ bytes(snapshot?.host?.memoryUsedBytes) }} /
            {{ bytes(snapshot?.host?.memoryTotalBytes) }}</ElDescriptionsItem
          >
        </ElDescriptions>
      </section>
      <section class="server-section">
        <h2>Platform API</h2>
        <ElDescriptions :column="2" border>
          <ElDescriptionsItem label="Java版本">{{
            snapshot?.jvm?.javaVersion || '不可用'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="进程ID">{{
            snapshot?.jvm?.pid || '不可用'
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="CPU使用率">{{
            percent(snapshot?.jvm?.cpuPercent)
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="非堆内存">{{
            bytes(snapshot?.jvm?.nonHeapUsedBytes)
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="堆内存" :span="2"
            >{{ bytes(snapshot?.jvm?.heapUsedBytes) }} /
            {{ bytes(snapshot?.jvm?.heapMaxBytes) }}</ElDescriptionsItem
          >
          <ElDescriptionsItem label="启动时间" :span="2">{{
            formatDateTime(snapshot?.jvm?.startedAt)
          }}</ElDescriptionsItem>
          <ElDescriptionsItem label="运行时长" :span="2">{{
            uptime(snapshot?.jvm?.uptimeMillis)
          }}</ElDescriptionsItem>
        </ElDescriptions>
      </section>
    </div>
    <TablePanel>
      <template #actions><h2 class="table-section-title">项目服务</h2></template>
      <ElTable :data="snapshot?.services || []" border row-key="key" empty-text="暂无采集结果">
        <ElTableColumn prop="name" label="服务" min-width="190" />
        <ElTableColumn label="地址" min-width="160"
          ><template #default="{ row }">{{ row.host || '—' }}</template></ElTableColumn
        >
        <ElTableColumn label="监听端口" width="120"
          ><template #default="{ row }">{{ row.port ?? '—' }}</template></ElTableColumn
        >
        <ElTableColumn label="探测状态" min-width="130"
          ><template #default="{ row }"
            ><ElTag
              :type="
                ['RUNNING', 'REACHABLE'].includes(row.status)
                  ? 'success'
                  : row.status === 'NOT_CONFIGURED'
                    ? 'info'
                    : 'warning'
              "
              effect="plain"
              >{{ statusNames[row.status] || row.status }}</ElTag
            ></template
          ></ElTableColumn
        >
      </ElTable>
    </TablePanel>
    <TablePanel>
      <template #actions><h2 class="table-section-title">GPU</h2></template>
      <template #tools
        ><ElTag v-if="snapshot?.gpuStatus !== 'AVAILABLE'" type="info" effect="plain">{{
          snapshot?.gpuStatus === 'DISABLED' ? '未启用采集' : '不可用'
        }}</ElTag></template
      >
      <ElTable
        :data="snapshot?.gpus || []"
        border
        row-key="index"
        empty-text="当前主机未提供GPU信息"
      >
        <ElTableColumn prop="index" label="序号" width="80" />
        <ElTableColumn prop="name" label="设备" min-width="220" />
        <ElTableColumn label="使用率" min-width="120"
          ><template #default="{ row }">{{
            percent(row.utilizationPercent)
          }}</template></ElTableColumn
        >
        <ElTableColumn label="显存使用" min-width="180"
          ><template #default="{ row }"
            >{{ row.memoryUsedMiB == null ? '不可用' : `${row.memoryUsedMiB} MiB` }} /
            {{ row.memoryTotalMiB == null ? '不可用' : `${row.memoryTotalMiB} MiB` }}</template
          ></ElTableColumn
        >
      </ElTable>
    </TablePanel>
  </div>
</template>

<style scoped>
.server-page {
  display: grid;
  gap: 16px;
}
.server-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.server-toolbar > span {
  color: var(--text-secondary);
  font-size: 14px;
}
.server-metrics {
  display: grid;
  grid-template-columns: 1fr 1.3fr;
  gap: 20px;
}
.server-section {
  min-width: 0;
}
.server-section h2,
.table-section-title {
  margin: 0 0 12px;
  font-size: 15px;
  font-weight: 600;
}
.table-section-title {
  margin: 0;
}
@media (max-width: 1000px) {
  .server-metrics {
    grid-template-columns: 1fr;
  }
}
</style>
