<script setup lang="ts">
import { ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElForm,
  ElFormItem,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import CameraDialogFrame from './CameraDialogFrame.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import GroupSelect from '../camera-groups/GroupSelect.vue'
import ConnectionForm from '../camera-access/ConnectionForm.vue'
import CandidateSelection from '../camera-access/CandidateSelection.vue'
import BulkImportPanel from '../camera-access/BulkImportPanel.vue'
import type { AccessJob } from '../../api/camera-access/types'
import type { AccessMode, ExistingCameraTarget } from '../camera-access/useAccessWizard'
import { useAccessWizard } from '../camera-access/useAccessWizard'
import { accessWarningLabel, methodLabels } from '../camera-access/form'
import { formatDateTime } from '../../utils/dateTime'

const props = withDefaults(
  defineProps<{
    embedded?: boolean
    mode?: AccessMode
    initialConnection?: { sourceId: string; sourceVersion: string; method?: string }
    targetCamera?: ExistingCameraTarget
    initialGroupId?: string | null
  }>(),
  { mode: 'device' },
)
const emit = defineEmits<{ close: []; saved: [cameraIds: string[]] }>()
const editingConnection = ref(false)
const bulkMode = ref(false)
const {
  pageNumber,
  accumulatedIds,
  changePage,
  options,
  connection,
  job,
  selections,
  groupId,
  impact,
  result,
  error,
  loading,
  busy,
  refreshing,
  paused,
  createFrozen,
  importFrozen,
  running,
  selectable,
  step,
  loadOptions,
  refreshJob,
  start,
  cancelAndReset,
  preview,
  confirmImport,
  reviseSelection,
  restartFromSource,
  detachJob,
} = useAccessWizard(
  () => (accumulatedIds.value.length ? emit('saved', accumulatedIds.value) : emit('close')),
  props.mode,
  props.initialConnection,
  props.targetCamera,
)
groupId.value = props.initialGroupId ?? null
function retryBulk(value: AccessJob) {
  if (!value.sourceId || !value.sourceVersion) return
  bulkMode.value = false
  void restartFromSource(value)
}
const statusLabels = {
  QUEUED: '等待读取',
  RUNNING: '正在读取设备',
  SUCCEEDED: '读取完成',
  PARTIAL: '读取到部分目录',
  FAILED: '读取失败',
  CANCELLED: '已取消',
  EXPIRED: '读取结果已过期',
}
</script>

<template>
  <CameraDialogFrame
    :embedded="embedded"
    :model-value="true"
    :title="
      mode === 'platform' ? '平台目录导入' : mode === 'rtsp' ? '按 RTSP 地址登记' : '读取设备资料'
    "
    width="min(960px, calc(100vw - 32px))"
    top="6vh"
    destroy-on-close
    :close-on-click-modal="false"
    :close-on-press-escape="false"
    :show-close="false"
    class="camera-access-dialog"
  >
    <BulkImportPanel
      v-if="bulkMode && job"
      :initial-job="job"
      :group-id="groupId"
      @job="job = $event"
      @back="bulkMode = false"
      @close="emit('saved', [])"
      @retry="retryBulk"
      @starting="detachJob"
    />
    <template v-else>
      <ol class="wizard-steps" aria-label="添加进度">
        <li :class="{ current: step === 0, complete: step > 0 }">
          1 {{ mode === 'platform' ? '连接平台' : mode === 'rtsp' ? '填写地址' : '连接设备' }}
        </li>
        <li :class="{ current: step === 1, complete: step > 1 }">2 选择通道</li>
        <li :class="{ current: step >= 2 }">3 确认导入</li>
      </ol>
      <RequestError :error="error" />
      <ElAlert
        v-if="targetCamera"
        type="info"
        :closable="false"
        :title="`为“${targetCamera.name}”选择一个对应通道。`"
      />
      <p v-if="loading" role="status">正在读取接入能力…</p>
      <template v-else-if="options && !options.ready">
        <ElAlert type="warning" :closable="false" title="接入配置未就绪，请联系系统管理员。" />
        <details class="operations-note">
          <summary>配置诊断</summary>
          <ul>
            <li v-for="item in options.diagnostics" :key="item">{{ item }}</li>
          </ul>
        </details>
      </template>
      <template v-else-if="step === 0 && options">
        <ElAlert
          v-if="createFrozen"
          type="warning"
          :closable="false"
          title="读取结果未确认，请重试。"
        />
        <ConnectionForm
          v-model="connection"
          :options="options"
          :disabled="busy || createFrozen"
          @editing="editingConnection = $event"
        />
      </template>
      <template v-else-if="result">
        <div class="import-result">
          <ElTag type="success">导入已完成</ElTag>
          <h3>新增 {{ result.createdCount }} 个相机，匹配已有 {{ result.existingCount }} 个</h3>
        </div>
        <ElTable :data="result.cameras" border row-key="cameraId"
          ><ElTableColumn label="相机 ID" prop="cameraId" min-width="200" /><ElTableColumn
            label="结果"
            min-width="140"
            ><template #default="{ row }">{{
              row.status === 'CREATED' ? '新建完成' : '匹配已有相机'
            }}</template></ElTableColumn
          ></ElTable
        >
      </template>
      <template v-else-if="impact">
        <div class="import-review">
          <h3>确认导入 {{ impact.cameraCount }} 个通道</h3>
          <dl>
            <dt>保存位置</dt>
            <dd>{{ impact.groupPath || '待归档' }}</dd>
            <dt>授权影响</dt>
            <dd>{{ impact.affectedUserCount }} 个账户</dd>
          </dl>
        </div>
        <ElAlert
          v-if="importFrozen"
          type="warning"
          :closable="false"
          title="导入结果未确认，请重试。"
        />
      </template>
      <template v-else-if="job">
        <div class="job-heading">
          <strong>{{
            options?.adapters?.find((adapter) => adapter.type === job?.method)?.label ||
            methodLabels[job.method] ||
            job.method
          }}</strong
          ><ElTag
            :type="
              job.status === 'FAILED' ? 'danger' : job.status === 'PARTIAL' ? 'warning' : 'info'
            "
            >{{ statusLabels[job.status] }}</ElTag
          ><span>有效至 {{ formatDateTime(job.expiresAt) }}</span>
        </div>
        <ElAlert
          v-if="job.diagnostic"
          type="error"
          :closable="false"
          :title="job.diagnostic.actionHint"
          :description="`诊断代码：${job.diagnostic.reasonCode} · 任务 ${job.jobId}`"
        />
        <p v-if="job.diagnostic" class="field-note">
          {{ job.diagnostic.occurredAt ? formatDateTime(job.diagnostic.occurredAt) : '' }}
          <span v-if="job.diagnostic.originTraceId"
            >请求标识：{{ job.diagnostic.originTraceId }}</span
          >
        </p>
        <ElAlert
          v-for="warning in job.warnings.filter(
            (code) => !['RTSP_CONFIGURATION_ONLY', 'PLATFORM_DIRECTORY_ONLY'].includes(code),
          )"
          :key="warning"
          type="warning"
          :closable="false"
          :title="accessWarningLabel(warning)"
        />
        <ElAlert
          v-if="job.status === 'PARTIAL'"
          type="warning"
          :closable="false"
          title="目录不完整，可导入当前已获取的通道。"
        />
        <p v-if="running" role="status">正在读取目录…</p>
        <p v-if="paused">自动刷新已暂停。</p>
        <ElDescriptions v-if="job.device" border :column="2" class="device-summary"
          ><ElDescriptionsItem label="设备名称">{{
            job.device.name || '未获取'
          }}</ElDescriptionsItem
          ><ElDescriptionsItem label="厂商">{{
            job.device.manufacturer || '未获取'
          }}</ElDescriptionsItem
          ><ElDescriptionsItem label="型号">{{ job.device.model || '未获取' }}</ElDescriptionsItem
          ><ElDescriptionsItem label="固件版本">{{
            job.device.firmware || '未获取'
          }}</ElDescriptionsItem
          ><ElDescriptionsItem label="序列号">{{
            job.device.serialNumber || '未获取'
          }}</ElDescriptionsItem></ElDescriptions
        >
        <template v-if="selectable"
          ><CandidateSelection
            v-model="selections"
            :candidates="job.candidates"
            :disabled="busy"
            :single="!!targetCamera"
          /><ElForm v-if="!targetCamera" label-width="110px" class="destination-form"
            ><ElFormItem label="保存到分组"
              ><GroupSelect v-model="groupId" clearable :disabled="busy" /><span class="field-note"
                >可留空。</span
              ></ElFormItem
            ></ElForm
          ></template
        >
      </template>
      <div
        v-if="mode === 'platform' && job?.page && !running && !impact"
        class="catalog-pagination"
      >
        <span
          >第 {{ job.page.pageNumber }} 页<span v-if="job.page.total != null">
            · 共 {{ job.page.total }} 个资源</span
          >；选择仅对当前页生效。</span
        >
        <ElButton
          :disabled="busy || createFrozen || importFrozen || pageNumber <= 1"
          @click="changePage(pageNumber - 1)"
          >上一页</ElButton
        >
        <ElButton
          :disabled="busy || createFrozen || importFrozen || !job.page.hasMore"
          @click="changePage(pageNumber + 1)"
          >下一页</ElButton
        >
        <p v-if="selections.length && !result">翻页后清空本页选择。</p>
      </div>
    </template>
    <template v-if="!editingConnection && !bulkMode" #footer>
      <ElButton v-if="createFrozen && step > 0" type="primary" :loading="busy" @click="start()"
        >重试本页读取请求</ElButton
      >
      <template v-if="result"
        ><ElButton
          type="primary"
          :disabled="busy || createFrozen"
          @click="
            emit(
              'saved',
              accumulatedIds.length
                ? accumulatedIds
                : result.cameras.map((camera) => camera.cameraId),
            )
          "
          >完成</ElButton
        ></template
      >
      <template v-else>
        <ElButton
          :disabled="busy || importFrozen || (createFrozen && step > 0)"
          @click="cancelAndReset(true)"
          >{{ running ? '取消读取并关闭' : '关闭' }}</ElButton
        >
        <ElButton v-if="!options || !options.ready" :loading="loading" @click="loadOptions"
          >重新检查</ElButton
        >
        <ElButton
          v-else-if="step === 0"
          type="primary"
          :loading="busy"
          :disabled="loading || !options.methods.includes(connection.method)"
          @click="start()"
          >{{
            createFrozen
              ? '重试原读取请求'
              : connection.method === 'RTSP'
                ? '解析视频地址'
                : mode === 'platform'
                  ? '查询平台目录'
                  : '读取设备'
          }}</ElButton
        >
        <template v-else-if="impact"
          ><ElButton :disabled="busy || importFrozen" @click="reviseSelection">返回选择</ElButton
          ><ElButton type="primary" :loading="busy" @click="confirmImport">{{
            importFrozen ? '重试原导入' : '确认导入'
          }}</ElButton></template
        >
        <template v-else
          ><ElButton :disabled="busy || createFrozen" @click="cancelAndReset()">重新连接</ElButton
          ><ElButton
            v-if="running || paused"
            :loading="refreshing"
            :disabled="busy"
            @click="refreshJob"
            >刷新状态</ElButton
          ><ElButton
            v-if="selectable"
            type="primary"
            :loading="busy"
            :disabled="createFrozen"
            @click="preview"
            >查看导入影响</ElButton
          ><ElButton
            v-if="mode === 'platform' && selectable && job?.page?.pageNumber === 1"
            type="primary"
            :disabled="busy || createFrozen"
            @click="bulkMode = true"
            >整批导入</ElButton
          ></template
        >
      </template>
    </template>
  </CameraDialogFrame>
</template>

<style scoped>
.catalog-pagination {
  margin: 18px 0;
  color: var(--el-text-color-secondary);
}
.wizard-steps {
  display: flex;
  gap: 24px;
  padding: 0 0 18px;
  margin: 0 0 24px;
  list-style: none;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.wizard-steps li {
  color: var(--el-text-color-placeholder);
}
.wizard-steps .current {
  color: var(--el-color-primary);
  font-weight: 600;
}
.wizard-steps .complete {
  color: var(--el-text-color-regular);
}
.operations-note {
  margin-top: 16px;
  color: var(--el-text-color-secondary);
}
.job-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}
.job-heading > span:last-child,
.field-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.device-summary,
.destination-form {
  margin: 20px 0;
}
.import-review,
.import-result {
  line-height: 1.7;
}
.import-review dl {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 10px;
  padding: 18px;
  background: var(--el-fill-color-light);
}
.import-review dd {
  margin: 0;
}
.import-result {
  margin-bottom: 20px;
}
</style>
