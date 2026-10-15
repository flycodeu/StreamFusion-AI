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
import { actionNames, moduleNames } from './labels'
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
watch(
  () => [format.value, JSON.stringify(props.filter)],
  () => {
    requestKey = undefined
  },
)
const states: Record<string, string> = {
  QUEUED: '等待导出',
  RUNNING: '正在导出',
  COMPLETED: '已完成',
  FAILED: '失败',
  CANCELLED: '已取消',
  EXPIRED: '文件已过期',
}
const reasons: Record<string, string> = {
  WORKER_LOST: '服务中断，请重新导出',
  EXPORT_TIMEOUT: '导出超时，请缩小范围',
  EXPORT_ROW_LIMIT: '超过10万条，请缩小范围',
  EXPORT_ROW_TOO_LARGE: '单条日志过大，请缩小范围或联系管理员',
  EXPORT_PERMISSION_REVOKED: '导出权限已失效',
  EXPORT_IO_ERROR: '文件生成失败，请检查存储空间',
  WORKER_STOPPED: '服务已停止',
  EXPORT_CANCELLED: '任务已取消',
}
const asJob = (value: unknown) => value as ExportJob
function filterSummary(filter: Record<string, unknown>) {
  const text = (key: string) => (typeof filter[key] === 'string' ? (filter[key] as string) : '')
  const start = text('startTime'),
    end = text('endTime')
  const time = (value: string) =>
    formatDateTime(/(?:Z|[+-]\d{2}:\d{2})$/.test(value) ? value : `${value}+08:00`)
  return [
    moduleNames[text('module')] || text('module') || '全部模块',
    actionNames[text('action')] || text('action'),
    ({ SUCCESS: '成功', FAILURE: '失败', DENIED: '拒绝' } as Record<string, string>)[
      text('result')
    ],
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
  const active = scope()
  try {
    const value = await getExports()
    if (active()) rows.value = value
  } catch (e) {
    if (active()) error.value = e
  }
}
async function refresh() {
  error.value = null
  await Promise.all([load(), estimate()])
}
async function act(action: 'create' | 'cancel' | 'remove' | 'download', row?: ExportJob) {
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
    if (rows.value.some((r) => ['QUEUED', 'RUNNING'].includes(r.state))) await load()
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
    <ElTable :data="rows" row-key="id" stripe>
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
            >{{ states[row.state] || row.state }}</ElTag
          ></template
        ></ElTableColumn
      >
      <ElTableColumn label="格式" prop="format" width="70" />
      <ElTableColumn label="筛选范围" min-width="150"
        ><template #default="{ row }"
          ><div>{{ filterSummary(row.filter) }}</div>
          <div v-if="row.reasonCode" class="export-reason">
            {{ reasons[row.reasonCode] || row.reasonCode }}
          </div></template
        ></ElTableColumn
      >
      <ElTableColumn label="数量 / 大小" width="125"
        ><template #default="{ row }"
          >{{ row.rowCount }} 条 · {{ (row.fileBytes / 1024).toFixed(1) }} KB</template
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
  color: var(--el-text-color-secondary);
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
