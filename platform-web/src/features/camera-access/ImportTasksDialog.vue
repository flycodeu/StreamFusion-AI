<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElButton, ElDialog, ElPagination, ElTable, ElTableColumn, ElTag } from 'element-plus'
import { getImportJobs } from '../../api/camera-access/api'
import type { AccessJob } from '../../api/camera-access/types'
import { usePageScope } from '../../composables/usePageScope'
import RequestError from '../../components/feedback/RequestError.vue'
import BulkImportPanel from './BulkImportPanel.vue'
import CreateDialog from '../camera/CreateDialog.vue'
import { formatDateTime } from '../../utils/dateTime'

const emit = defineEmits<{ close: [] }>()
const captureScope = usePageScope(),
  rows = ref<AccessJob[]>([]),
  page = ref(1),
  total = ref(0),
  loading = ref(false),
  error = ref<unknown>(null),
  selected = ref<AccessJob | null>(null),
  retrying = ref<AccessJob | null>(null)
let sequence = 0
const asJob = (value: unknown) => value as AccessJob
const labels: Record<AccessJob['status'], string> = {
  QUEUED: '等待导入',
  RUNNING: '正在导入',
  SUCCEEDED: '已完成',
  PARTIAL: '部分完成',
  FAILED: '已中断',
  CANCELLED: '已取消',
  EXPIRED: '已到期',
}
async function load() {
  const active = captureScope(),
    current = ++sequence
  loading.value = true
  error.value = null
  try {
    const result = await getImportJobs({ page: page.value, size: 20 })
    if (active() && current === sequence) {
      rows.value = result.items
      total.value = result.total
    }
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function retry(job: AccessJob) {
  selected.value = null
  retrying.value = job
}
function back() {
  selected.value = null
  retrying.value = null
  void load()
}
onMounted(load)
</script>
<template>
  <ElDialog
    :model-value="true"
    :title="retrying ? '重试平台导入' : selected ? '导入进度' : '导入任务'"
    width="min(960px, calc(100vw - 32px))"
    :close-on-click-modal="!retrying"
    :close-on-press-escape="!retrying"
    :show-close="!retrying"
    @update:model-value="emit('close')"
  >
    <CreateDialog
      v-if="retrying?.sourceId && retrying.sourceVersion"
      embedded
      mode="platform"
      :initial-connection="{
        sourceId: retrying.sourceId,
        sourceVersion: retrying.sourceVersion,
        method: retrying.method,
      }"
      :initial-group-id="retrying.bulk?.groupId"
      @close="back"
      @saved="back"
    />
    <BulkImportPanel
      v-else-if="selected"
      :key="selected.jobId"
      :initial-job="selected"
      close-label="返回任务列表"
      @close="back"
      @back="back"
      @retry="retry"
    />
    <template v-else>
      <RequestError :error="error" />
      <ElTable :data="rows" row-key="jobId" border empty-text="本次登录暂无导入任务">
        <ElTableColumn prop="jobId" label="任务" min-width="200" /><ElTableColumn
          label="状态"
          width="120"
          ><template #default="{ row }"
            ><ElTag>{{ labels[row.status as AccessJob['status']] }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn label="处理 / 总数" min-width="140"
          ><template #default="{ row }"
            >{{ row.bulk?.processedCount ?? 0 }} / {{ row.bulk?.total ?? '未知' }}</template
          ></ElTableColumn
        >
        <ElTableColumn label="新增 / 已有 / 失败" min-width="160"
          ><template #default="{ row }"
            >{{ row.bulk?.createdCount ?? 0 }} / {{ row.bulk?.existingCount ?? 0 }} /
            {{ row.bulk?.failedCount ?? 0 }}</template
          ></ElTableColumn
        >
        <ElTableColumn label="执行期限" min-width="170"
          ><template #default="{ row }">{{
            formatDateTime(row.expiresAt)
          }}</template></ElTableColumn
        >
        <ElTableColumn label="操作" width="85"
          ><template #default="{ row }"
            ><ElButton link type="primary" @click="selected = asJob(row)">查看</ElButton></template
          ></ElTableColumn
        >
      </ElTable>
      <ElPagination
        v-model:current-page="page"
        :page-size="20"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="load"
      />
    </template>
    <template v-if="!selected && !retrying" #footer
      ><ElButton :loading="loading" @click="load">刷新</ElButton
      ><ElButton @click="emit('close')">关闭</ElButton></template
    >
  </ElDialog>
</template>
