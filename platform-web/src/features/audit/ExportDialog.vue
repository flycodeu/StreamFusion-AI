<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  ElButton,
  ElDialog,
  ElSelect,
  ElOption,
  ElTable,
  ElTableColumn,
  ElTag,
  ElMessageBox,
} from 'element-plus'
import {
  getExports,
  createExport,
  cancelExport,
  removeExport,
  downloadExport,
  type ExportJob,
} from '../../api/audit/maintenance'
import { getAuditPage, type AuditQuery } from '../../api/audit/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import { formatBytes } from '../../utils/formatBytes'
import { labelOf } from '../../utils/display'
import { exportStates, exportReasons } from './maintenancePresentation'
import { actionNames, moduleNames, resultNames } from './labels'
const props = defineProps<{ filter: AuditQuery }>()
defineEmits<{ close: [] }>()
const format = ref('CSV'),
  rows = ref<ExportJob[]>([]),
  loading = ref(false),
  estimated = ref<number | null>(null),
  error = ref<unknown>(null)
const scope = usePageScope()
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
let requestKey: string | undefined
let alive = true
let revision = 0
watch(
  () => [format.value, JSON.stringify(props.filter)],
  () => {
    requestKey = undefined
  },
)
const asJob = (value: unknown) => value as ExportJob
function filterSummary(filter: Record<string, unknown>) {
  const text = (key: string) => (typeof filter[key] === 'string' ? (filter[key] as string) : '')
  const start = text('startTime'),
    end = text('endTime')
  const time = (value: string) =>
    formatDateTime(/(?:Z|[+-]\d{2}:\d{2})$/.test(value) ? value : `${value}+08:00`)
  return [
    labelOf(moduleNames, text('module'), '全部模块'),
    labelOf(actionNames, text('action'), ''),
    labelOf(resultNames, text('result'), ''),
    text('user') || text('userPattern')
      ? `操作人 ${
          text('user') ||
          text('userPattern')
            .slice(1, -1)
            .replace(/!([!%_])/g, '$1')
        }`
      : '',
    text('traceId') ? `请求 ${text('traceId')}` : '',
    start || end ? `${start ? time(start) : '不限'} 至 ${end ? time(end) : '不限'}` : '全部时间',
  ]
    .filter(Boolean)
    .join(' · ')
}
async function estimate() {
  const active = scope()
  estimated.value = null
  try {
    const value = await getAuditPage({ ...props.filter, page: 1, size: 1 })
    if (active()) estimated.value = value.total
  } catch (e) {
    if (active()) error.value = e
  }
}
async function load() {
  const active = scope(),
    current = ++revision
  try {
    const value = await getExports()
    if (active() && current === revision) rows.value = value
  } catch (e) {
    if (active() && current === revision) error.value = e
  }
}
async function refresh() {
  error.value = null
  await Promise.all([load(), estimate()])
}
async function act(action: 'create' | 'cancel' | 'remove' | 'download', row?: ExportJob) {
  if (loading.value) return
  ++revision
  const active = scope()
  loading.value = true
  error.value = null
  try {
    if (action === 'create') {
      requestKey ??= crypto.randomUUID()
      await createExport(format.value, props.filter, requestKey)
      requestKey = undefined
    } else if (row) {
      if (action === 'cancel') await cancelExport(row.id)
      else if (action === 'download') await downloadExport(row.id)
      else {
        await ElMessageBox.confirm('删除此导出文件？原始操作日志仍保留。', '删除导出文件', {
          type: 'warning',
          confirmButtonClass: 'el-button--danger',
          confirmButtonText: '确认删除',
          cancelButtonText: '取消',
        })
        if (!active()) return
        await removeExport(row.id)
      }
    }
    if (active()) await load()
  } catch (e) {
    if (active() && e !== 'cancel' && e !== 'close') error.value = e
  } finally {
    if (active()) loading.value = false
  }
}
onMounted(async () => {
  await refresh()
  if (!alive) return
  const poll = async () => {
    if (!alive) return
    if (!loading.value && rows.value.some((r) => ['QUEUED', 'RUNNING'].includes(r.state)))
      await load()
    if (alive) timer = globalThis.setTimeout(poll, 3000)
  }
  timer = globalThis.setTimeout(poll, 3000)
})
onBeforeUnmount(() => {
  alive = false
  globalThis.clearTimeout(timer)
})
</script>
<template>
  <ElDialog
    :model-value="true"
    title="操作日志导出"
    width="1060px"
    class="management-dialog"
    align-center
    @close="$emit('close')"
  >
    <div class="export-scope">
      {{ filterSummary({ ...filter }) }} ·
      {{ estimated === null ? '正在核对数量' : `预计 ${estimated.toLocaleString()} 条` }}
    </div>
    <div class="export-toolbar">
      <ElSelect v-model="format" aria-label="导出格式"
        ><ElOption label="CSV（表格）" value="CSV" /><ElOption
          label="JSONL（结构化）"
          value="JSONL" /></ElSelect
      ><ElButton
        type="primary"
        :loading="loading"
        :disabled="estimated === null || estimated > 100000"
        @click="act('create')"
        >导出当前筛选</ElButton
      ><ElButton :disabled="loading" @click="refresh">刷新记录</ElButton>
    </div>
    <RequestError :error="error" />
    <p v-if="estimated !== null && estimated > 100000" class="export-reason">
      超过10万条，请缩小筛选范围。
    </p>
    <ElTable :data="rows" row-key="id" border stripe :max-height="380">
      <ElTableColumn type="expand" width="40"
        ><template #default="{ row }">
          <dl class="export-details">
            <dt>任务编号</dt>
            <dd>{{ row.id }}</dd>
            <dt>创建账号</dt>
            <dd>{{ row.actorId }}</dd>
            <dt>完成时间</dt>
            <dd>{{ formatDateTime(row.finishedAt) }}</dd>
          </dl>
        </template></ElTableColumn
      >
      <ElTableColumn label="创建时间" width="170"
        ><template #default="{ row }"
          ><div>{{ formatDateTime(row.createdAt) }}</div>
        </template></ElTableColumn
      >
      <ElTableColumn label="状态" width="95"
        ><template #default="{ row }"
          ><ElTag
            :type="
              row.state === 'FAILED' ? 'danger' : row.state === 'COMPLETED' ? 'success' : 'info'
            "
            >{{ labelOf(exportStates, row.state) }}</ElTag
          ></template
        ></ElTableColumn
      >
      <ElTableColumn label="格式" prop="format" width="70" />
      <ElTableColumn label="筛选范围" min-width="150"
        ><template #default="{ row }"
          ><div>{{ filterSummary(row.filter) }}</div>
          <div v-if="row.reasonCode" class="export-reason">
            {{ labelOf(exportReasons, row.reasonCode) }}
          </div></template
        ></ElTableColumn
      >
      <ElTableColumn label="数量 / 大小" width="125"
        ><template #default="{ row }"
          >{{ row.rowCount }} 条 ·
          {{ formatBytes(row.fileBytes, { unit: 'KiB', precision: 1 }) }}</template
        ></ElTableColumn
      >
      <ElTableColumn label="文件有效期" width="170"
        ><template #default="{ row }">{{ formatDateTime(row.expiresAt) }}</template></ElTableColumn
      >
      <ElTableColumn label="操作" width="145" fixed="right"
        ><template #default="{ row }"
          ><ElButton
            v-if="row.downloadable"
            link
            type="primary"
            :disabled="loading"
            @click="act('download', asJob(row))"
            >下载</ElButton
          ><ElButton
            v-if="['QUEUED', 'RUNNING'].includes(row.state)"
            link
            :disabled="loading"
            @click="act('cancel', asJob(row))"
            >取消</ElButton
          ><ElButton
            v-else-if="row.state !== 'EXPIRED'"
            link
            type="danger"
            :disabled="loading"
            @click="act('remove', asJob(row))"
            >删除文件</ElButton
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <template #footer><ElButton @click="$emit('close')">关闭</ElButton></template>
  </ElDialog>
</template>
<style scoped>
.export-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 16px;
}
.export-scope {
  margin-bottom: 12px;
  padding: 10px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #334155;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.export-details {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 10px;
  padding: 0 20px;
}
.export-details dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.export-toolbar :deep(.el-select) {
  width: 180px;
}
.export-toolbar :deep(.el-button + .el-button) {
  margin-left: 0;
}
.export-reason {
  color: var(--el-color-danger);
  margin-top: 5px;
}
</style>
