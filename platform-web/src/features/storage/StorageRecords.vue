<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { ElButton, ElTable, ElTableColumn, ElTag, ElDialog, ElPagination } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import * as api from '../../api/storage/api'
import type { StorageTask } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import JsonInspector from '../../components/feedback/JsonInspector.vue'
import StorageHistoryPolicy from './StorageHistoryPolicy.vue'
import { label, time } from './display'

const props = defineProps<{ instanceId: string; active: boolean }>()
const emit = defineEmits<{ updated: [] }>()
const loading = ref(false)
const retentionOpen = ref(false)
const rows = ref<StorageTask[]>([]),
  page = ref(1),
  total = ref(0),
  error = ref<unknown>(null),
  selected = ref<StorageTask | null>(null)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined,
  sequence = 0
const live = new Set(['QUEUED', 'RUNNING', 'RECONCILING'])
const observedLive = new Set([...live, 'IN_PROGRESS'])
let polls = 0
async function load() {
  globalThis.clearTimeout(timer)
  if (!props.active) return
  const current = ++sequence
  loading.value = true
  try {
    const result = await api.listTasks(props.instanceId, page.value, 10)
    if (sequence !== current || !props.active) return
    const lastPage = Math.max(1, Math.ceil(result.total / 10))
    if (page.value > lastPage) {
      page.value = lastPage
      await load()
      return
    }
    const wasRunning = rows.value.some((row) => observedLive.has(row.status))
    rows.value = result.items
    total.value = result.total
    error.value = null
    if (wasRunning && !rows.value.some((row) => observedLive.has(row.status))) emit('updated')
    if (rows.value.some((row) => observedLive.has(row.status)) && ++polls < 60)
      timer = globalThis.setTimeout(() => void load(), 2000)
  } catch (e) {
    if (sequence === current) error.value = e
  } finally {
    if (sequence === current) loading.value = false
  }
}
watch(
  () => [props.active, props.instanceId],
  () => {
    sequence++
    globalThis.clearTimeout(timer)
    polls = 0
    page.value = 1
    rows.value = []
    selected.value = null
    if (props.active) void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  sequence++
  globalThis.clearTimeout(timer)
})
async function action(row: StorageTask, resume = false) {
  try {
    if (resume) await api.resumeTask(row.taskId, row.version)
    else await api.cancelTask(row.taskId, row.version)
    await load()
  } catch (e) {
    error.value = e
  }
}
function refresh() {
  polls = 0
  void load()
}
const asRow = (row: unknown): StorageTask => row as StorageTask
</script>

<template>
  <section class="storage-records" aria-label="操作记录">
    <header class="record-tools">
      <h3>操作记录</h3>
      <div class="record-actions">
        <ElButton @click="retentionOpen = true">清理策略</ElButton>
        <ElButton :icon="Refresh" :loading="loading" @click="refresh">刷新记录</ElButton>
      </div>
    </header>
    <RequestError v-if="error" :error="error" />
    <ElTable :data="rows" row-key="taskId" max-height="320">
      <ElTableColumn label="任务" min-width="130"
        ><template #default="{ row }">{{ label(row.type) }}</template></ElTableColumn
      >
      <ElTableColumn label="状态" width="130"
        ><template #default="{ row }"
          ><ElTag>{{ label(row.status) }}</ElTag></template
        ></ElTableColumn
      >
      <ElTableColumn label="创建时间" min-width="180"
        ><template #default="{ row }">{{ time(row.acceptedAt) }}</template></ElTableColumn
      >
      <ElTableColumn label="操作" min-width="190"
        ><template #default="{ row }"
          ><ElButton text @click="selected = asRow(row)">查看 JSON</ElButton
          ><ElButton
            v-if="live.has(row.status)"
            text
            :disabled="row.cancelRequested"
            @click="action(asRow(row))"
            >{{ row.cancelRequested ? '取消已请求' : '取消' }}</ElButton
          ><ElButton v-if="row.status === 'PAUSED_LIMIT'" text @click="action(asRow(row), true)"
            >继续统计</ElButton
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <ElPagination
      v-model:current-page="page"
      :page-size="10"
      :total="total"
      :pager-count="5"
      layout="total, prev, pager, next"
      @current-change="load"
    />
    <ElDialog
      :model-value="!!selected"
      class="management-dialog"
      title="任务详情"
      width="min(780px, 94vw)"
      align-center
      destroy-on-close
      @update:model-value="selected = null"
    >
      <JsonInspector :value="selected" />
    </ElDialog>
    <StorageHistoryPolicy v-model="retentionOpen" :instance-id="instanceId" />
  </section>
</template>

<style scoped>
.storage-records {
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid #e2e8f0;
  min-width: 0;
}
.record-tools {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 14px;
}
.record-tools h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: #0f172a;
}
.el-pagination {
  justify-content: flex-end;
  flex-wrap: wrap;
  row-gap: 10px;
  margin-top: 12px;
}
.record-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.record-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.storage-records :deep(.el-table__cell) {
  padding: 11px 0;
  font-variant-numeric: tabular-nums;
}
.storage-records :deep(.el-tag) {
  background: #e0f2fe;
  color: #0284c7;
  border: 0;
  border-radius: 6px;
  padding: 2px 8px;
  font-weight: 500;
}
</style>
