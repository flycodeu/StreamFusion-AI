<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElButton,
  ElDrawer,
  ElForm,
  ElFormItem,
  ElInput,
  ElIcon,
  ElOption,
  ElPagination,
  ElSelect,
  ElRadioGroup,
  ElRadioButton,
  vLoading,
} from 'element-plus'
import {
  CircleCheck,
  Warning,
  Clock,
  QuestionFilled,
  SwitchButton,
  VideoCamera,
} from '@element-plus/icons-vue'
import { getCamera, getDeviceChannels } from '../../api/camera/api'
import type { Camera, CameraProfile } from '../../api/camera/types'
import { getCameraStatus } from '../../api/camera-status/api'
import type {
  ObservationFilter,
  StatusChannel,
  StatusProfile,
  StatusSearch,
  StatusLayer,
} from '../../api/camera-status/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { ApiRequestError } from '../../lib/http/error'
import TablePanel from '../../components/table/TablePanel.vue'
import { usageLabel } from '../../features/camera/form'
import type { ConnectionState } from '../../features/camera-status/health'
import StatusCard from '../../features/camera-status/StatusCard.vue'
import StatusPanel from '../../features/camera-status/StatusPanel.vue'
import PlanDialog from '../../features/camera-status/PlanDialog.vue'
import { useStatusPage } from '../../features/camera-status/useStatusPage'
import { addressLabel, addressText } from '../../features/camera/address'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'

defineOptions({ name: 'CameraStatus' })
const route = useRoute()
const name = ref(''),
  observation = ref<ObservationFilter | null>(null)
const layer = ref<StatusLayer>('CONTROL')
const query = shallowRef<StatusSearch>({ page: 1, size: 20, layer: 'CONTROL' })
const drawerOpen = ref(false)
const plansOpen = ref(false)
const target = shallowRef<{ channel: StatusChannel; profileId: string } | null>(null)
const selectedChannelId = ref<string | null>(null)
const selectedProfileId = ref<string | null>(null)
const channelOptions = ref<Camera[]>([])
const channelPage = ref(0)
const channelTotal = ref(0)
const channelLoading = ref(false)
const channelError = shallowRef<unknown>(null)
const detailLoading = ref(false)
const diagnosticTarget = shallowRef<{
  cameraId: string
  name: string
  profiles: CameraProfile[]
} | null>(null)
const deviceModel = ref<string | null>(null)
const deviceError = ref<unknown>(null)
const captureScope = usePageScope()
watch(
  () => sessionState.epoch,
  () => {
    drawerOpen.value = false
    target.value = null
    selectedChannelId.value = null
    selectedProfileId.value = null
  },
  { flush: 'sync' },
)
watch(
  () => (drawerOpen.value ? selectedChannelId.value : null),
  async (cameraId, _previous, onCleanup) => {
    deviceModel.value = null
    deviceError.value = null
    diagnosticTarget.value = null
    detailLoading.value = false
    if (!cameraId) return
    const inScope = captureScope()
    const controller = new globalThis.AbortController()
    onCleanup(() => controller.abort())
    detailLoading.value = true
    try {
      const [camera, status] = await Promise.all([
        getCamera(cameraId, controller.signal),
        getCameraStatus(cameraId, controller.signal),
      ])
      if (controller.signal.aborted || !inScope()) return
      const model = camera.deviceSummary?.model
      deviceModel.value = typeof model === 'string' && model.trim() ? model.trim() : null
      const byId = new Map(camera.profiles.map((profile) => [profile.streamProfileId, profile]))
      const profiles = status.profiles.flatMap((profile) => {
        const detail = byId.get(profile.streamProfileId)
        return detail?.enabled ? [detail] : []
      })
      diagnosticTarget.value = { cameraId, name: camera.name, profiles }
      selectedProfileId.value =
        cameraId === target.value?.channel.cameraId &&
        profiles.some((profile) => profile.streamProfileId === target.value?.profileId)
          ? target.value.profileId
          : (profiles[0]?.streamProfileId ?? null)
    } catch (cause) {
      if (!controller.signal.aborted && inScope()) deviceError.value = cause
    } finally {
      if (!controller.signal.aborted && inScope()) detailLoading.value = false
    }
  },
  { flush: 'sync' },
)
let channelRevision = 0
watch(
  () => (drawerOpen.value ? target.value?.channel.groupKey : null),
  (groupKey) => {
    ++channelRevision
    channelOptions.value = []
    channelPage.value = 0
    channelTotal.value = 0
    channelError.value = null
    channelLoading.value = false
    if (groupKey) void loadChannels()
  },
  { flush: 'sync' },
)
async function loadChannels() {
  const groupKey = target.value?.channel.groupKey
  if (!drawerOpen.value || !groupKey || channelLoading.value) return
  const revision = channelRevision
  const inScope = captureScope()
  channelLoading.value = true
  channelError.value = null
  try {
    const result = await getDeviceChannels(groupKey, { page: channelPage.value + 1, size: 50 })
    if (revision !== channelRevision || !drawerOpen.value || !inScope()) return
    const known = new Map(channelOptions.value.map((camera) => [camera.cameraId, camera]))
    for (const camera of result.items) known.set(camera.cameraId, camera)
    channelOptions.value = [...known.values()]
    channelPage.value = result.page
    channelTotal.value = result.total
  } catch (cause) {
    if (revision === channelRevision && inScope()) channelError.value = cause
  } finally {
    if (revision === channelRevision && inScope()) channelLoading.value = false
  }
}
watch(
  () => route?.query.name,
  (value) => {
    name.value = typeof value === 'string' ? value : ''
    observation.value = null
    search()
  },
  { immediate: true },
)
const { rows, result, summary, total, loading, error, paused, refresh } = useStatusPage(
  query,
  computed(() => !drawerOpen.value && !plansOpen.value),
)
const stateLabels = computed(() => ({
  accessible: '检查通过',
  failed: '检查异常',
  checking: '检测中',
  stale: '配置已变更',
  unknown: '尚未检查',
  disabled: '未启用',
}))
const stateIcons = {
  accessible: CircleCheck,
  failed: Warning,
  checking: Clock,
  stale: Clock,
  unknown: QuestionFilled,
  disabled: SwitchButton,
}
const stateOrder: ConnectionState[] = [
  'accessible',
  'failed',
  'checking',
  'stale',
  'unknown',
  'disabled',
]
const summaryLabel = computed(() =>
  summary.value
    ? stateOrder.map((state) => `${stateLabels.value[state]} ${summary.value![state]}`).join('，')
    : '状态尚未读取',
)
const observationLabels = computed<Record<ObservationFilter, string>>(() => ({
  NEVER: '尚未检查',
  ACCESSIBLE: '最近检查通过',
  FAILED: '最近检查异常',
  UNSUPPORTED: '检测待适配',
}))
const capacityHint = computed(() =>
  error.value instanceof ApiRequestError && error.value.code === 'CONFLICT'
    ? '请缩小相机搜索范围；仍失败时提供请求标识给管理员核对码流数量与授权配置。'
    : '',
)
function search() {
  query.value = {
    page: 1,
    size: query.value.size,
    layer: layer.value,
    name: name.value.trim() || undefined,
    observation: observation.value || undefined,
  }
}
function reset() {
  name.value = ''
  observation.value = null
  search()
}
function changeLayer(value: string | number | boolean | undefined) {
  if (value !== 'CONTROL' && value !== 'MEDIA') return
  layer.value = value
  observation.value = null
  search()
}
function changePage(page: number, size = query.value.size) {
  query.value = { ...query.value, page: size === query.value.size ? page : 1, size }
}
function showDiagnostic(channel: StatusChannel, profile: StatusProfile) {
  target.value = { channel, profileId: profile.streamProfileId }
  selectedChannelId.value = channel.cameraId
  selectedProfileId.value = profile.streamProfileId
  drawerOpen.value = true
}
</script>

<template>
  <div class="content-page camera-status-page">
    <ElForm class="search-form status-search" inline @submit.prevent="search">
      <ElFormItem label="检查类型">
        <ElRadioGroup :model-value="layer" aria-label="检查类型" @update:model-value="changeLayer">
          <ElRadioButton value="CONTROL">相机连接</ElRadioButton>
          <ElRadioButton value="MEDIA">视频检查</ElRadioButton>
        </ElRadioGroup>
      </ElFormItem>
      <ElFormItem label="相机">
        <ElInput v-model="name" placeholder="搜索相机名称 / IP" clearable maxlength="128" />
      </ElFormItem>
      <ElFormItem label="最近检查结果">
        <ElSelect v-model="observation" clearable placeholder="全部结果（含历史）">
          <ElOption
            v-for="(label, value) in observationLabels"
            :key="value"
            :value="value"
            :label="label"
          />
        </ElSelect>
      </ElFormItem>
      <ElFormItem
        ><ElButton type="primary" native-type="submit" :loading="loading && !result">查询</ElButton
        ><ElButton @click="reset">重置</ElButton></ElFormItem
      >
    </ElForm>
    <RequestError :error="error" />
    <p v-if="capacityHint" class="capacity-hint" role="alert">{{ capacityHint }}</p>
    <section class="status-overview" aria-label="当前页相机检查总览">
      <header class="overview-heading">
        <div>
          <h3>{{ layer === 'CONTROL' ? '连接检查' : '视频检查' }}</h3>
          <span>当前页 {{ summary?.total ?? '—' }} 台相机</span>
        </div>
      </header>
      <div class="status-metrics">
        <div
          v-for="state in stateOrder"
          :key="state"
          class="status-metric"
          :class="`state-${state}`"
        >
          <ElIcon class="metric-icon" aria-hidden="true"
            ><component :is="stateIcons[state]"
          /></ElIcon>
          <div>
            <strong>{{ summary?.[state] ?? '—' }}</strong
            ><span>{{ stateLabels[state] }}</span>
          </div>
        </div>
      </div>
      <div class="status-distribution" role="img" :aria-label="summaryLabel">
        <template v-if="summary?.total">
          <span
            v-for="state in stateOrder"
            :key="state"
            :class="`state-${state}`"
            :style="{ width: `${(summary[state] / summary.total) * 100}%` }"
          />
        </template>
      </div>
    </section>
    <TablePanel :initial-loading="loading && !result">
      <template #actions
        ><ElButton type="primary" @click="plansOpen = true">巡检任务</ElButton></template
      >
      <template #tools
        ><ElButton :loading="loading && !result" @click="refresh">刷新结果</ElButton></template
      >
      <div v-loading="loading && !result" class="camera-card-grid" :aria-busy="loading">
        <StatusCard
          v-for="channel in rows"
          :key="channel.groupKey || channel.cameraId"
          :channel="channel"
          :layer="query.layer"
          @details="showDiagnostic"
        />
        <p v-if="!rows.length && !loading" class="empty-list">
          {{ error ? '状态未读取，请重试刷新' : result ? '暂无符合条件的相机' : '状态未读取' }}
        </p>
      </div>
      <p v-if="paused" class="read-paused" role="status">已暂停读取检查进度，请点击“刷新结果”。</p>
      <template #footer
        ><ElPagination
          :current-page="query.page"
          :page-size="query.size"
          :total="total"
          :disabled="!result"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          @update:current-page="changePage($event)"
          @update:page-size="changePage(1, $event)"
      /></template>
    </TablePanel>
    <PlanDialog v-if="plansOpen" @close="plansOpen = false" @changed="refresh" />
    <ElDrawer
      v-model="drawerOpen"
      title="状态详情"
      size="min(760px, 100vw)"
      :destroy-on-close="false"
    >
      <template v-if="target">
        <RequestError :error="deviceError" advice="请重新打开状态详情以读取相机档案。" />
        <div v-loading="detailLoading">
          <StatusPanel
            :camera="diagnosticTarget"
            :initial-profile-id="selectedProfileId"
            :initial-layer="query.layer"
            :show-profile-selector="false"
            :active="drawerOpen"
          >
            <template #summary="{ connection }">
              <section class="status-device-summary" aria-label="设备档案摘要">
                <div class="device-summary-heading">
                  <ElIcon class="device-summary-icon" aria-hidden="true"><VideoCamera /></ElIcon>
                  <h3 :title="target.channel.deviceName || target.channel.name">
                    {{ target.channel.deviceName || target.channel.name }}
                  </h3>
                  <span
                    class="device-connection"
                    :class="`state-${target.channel.lifecycle === 'ENABLED' ? connection.state : 'disabled'}`"
                    :title="connection.title"
                  >
                    <i aria-hidden="true" />{{
                      target.channel.lifecycle === 'DISABLED'
                        ? '已停用'
                        : target.channel.lifecycle === 'PENDING_ASSIGNMENT'
                          ? '待归档'
                          : connection.label
                    }}
                  </span>
                </div>
                <div class="device-summary-meta">
                  <span
                    :title="`${addressLabel(target.channel.accessAddress)}：${addressText(target.channel.accessAddress)}`"
                    >{{ addressLabel(target.channel.accessAddress) }}：{{
                      addressText(target.channel.accessAddress)
                    }}</span
                  >
                  <span :title="`型号：${deviceModel || '未获取'}`"
                    >型号：{{ deviceModel || '未获取' }}</span
                  >
                </div>
              </section>
              <details
                v-if="channelTotal > 1 || (diagnosticTarget?.profiles.length ?? 0) > 1"
                class="detection-target-options"
              >
                <summary>检测设置</summary>
                <div class="detection-target-controls">
                  <ElSelect
                    v-if="channelTotal > 1"
                    v-model="selectedChannelId"
                    filterable
                    aria-label="检测通道"
                    placeholder="选择检测通道"
                    :loading="channelLoading"
                  >
                    <ElOption
                      v-for="camera in channelOptions"
                      :key="camera.cameraId"
                      :value="camera.cameraId"
                      :label="camera.name"
                      :disabled="camera.lifecycle !== 'ENABLED'"
                    />
                  </ElSelect>
                  <ElSelect
                    v-if="(diagnosticTarget?.profiles.length ?? 0) > 1"
                    v-model="selectedProfileId"
                    aria-label="检测码流"
                    placeholder="选择检测码流"
                  >
                    <ElOption
                      v-for="profile in diagnosticTarget?.profiles"
                      :key="profile.streamProfileId"
                      :value="profile.streamProfileId"
                      :label="profile.label || usageLabel(profile.usageHint)"
                    />
                  </ElSelect>
                  <ElButton
                    v-if="channelOptions.length < channelTotal"
                    :loading="channelLoading"
                    @click="loadChannels"
                    >加载更多通道</ElButton
                  >
                </div>
                <p>默认检测一路低负载码流；结果只代表所选通道最近一次检查。</p>
              </details>
              <RequestError :error="channelError" advice="可重新打开状态详情以加载通道选项。" />
            </template>
          </StatusPanel>
        </div>
      </template>
    </ElDrawer>
  </div>
</template>

<style scoped>
.camera-status-page {
  min-width: 0;
}
.overview-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.overview-heading > div:first-child {
  min-width: 0;
}
.overview-heading > div:first-child > span {
  display: block;
  margin-top: 6px;
  color: var(--text-secondary);
  font-size: 12px;
}
.capacity-hint {
  color: var(--text-secondary);
  font-size: 13px;
}
.status-overview {
  margin-bottom: 12px;
  padding: 14px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  background: var(--el-bg-color);
}
.overview-heading h3 {
  margin: 0;
  font-size: 14px;
}
.status-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 16px;
  margin: 14px 0;
}
.status-metric {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.metric-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  padding: 6px;
  border-radius: 50%;
  background: var(--el-fill-color-light);
  color: var(--state-color);
  font-size: 24px;
}
.status-metric > div {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}
.status-metric strong {
  font-size: 22px;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}
.status-metric span {
  font-size: 12px;
  color: var(--text-secondary);
}
.state-accessible {
  --state-color: var(--el-color-success);
}
.state-failed {
  --state-color: var(--el-color-danger);
}
.state-checking {
  --state-color: var(--el-color-primary);
}
.state-stale {
  --state-color: var(--el-color-warning);
}
.state-unknown,
.state-disabled {
  --state-color: var(--el-text-color-placeholder);
}
.status-distribution {
  display: flex;
  height: 8px;
  border-radius: 4px;
  overflow: hidden;
  background: var(--el-fill-color-light);
}
.status-distribution > span {
  background: var(--state-color);
}
.status-search :deep(.el-input) {
  width: 100%;
}
.status-search :deep(.el-select) {
  width: 100%;
}
.status-search {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 16px;
  padding: 16px;
}
.status-search :deep(.el-form-item) {
  min-width: 0;
  margin: 0;
}
.status-search :deep(.el-form-item:not(:last-child)) {
  flex: 1 1 260px;
}
.status-search :deep(.el-form-item__content) {
  min-width: 0;
}
.capacity-hint {
  margin: 8px 0 16px;
}
.camera-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr));
  gap: 12px;
  align-items: stretch;
  min-height: 100px;
}
.status-device-summary {
  padding: 14px 16px;
  margin-bottom: 20px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
.detection-target-options {
  margin: 0 0 16px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #64748b;
  font-size: 13px;
}
.detection-target-options summary {
  cursor: pointer;
  color: #334155;
  font-weight: 500;
}
.detection-target-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 12px;
}
.detection-target-controls :deep(.el-select) {
  flex: 1 1 180px;
  min-width: 0;
}
.detection-target-options p {
  margin: 10px 0 0;
  color: #94a3b8;
  font-size: 12px;
}
.device-summary-heading {
  display: flex;
  align-items: center;
  gap: 10px;
}
.device-summary-icon {
  flex-shrink: 0;
  color: #64748b;
  font-size: 18px;
}
.device-summary-heading h3 {
  flex: 1;
  min-width: 0;
  margin: 0;
  overflow: hidden;
  color: #0f172a;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.device-connection {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 99px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
}
.device-connection.state-accessible {
  border-color: #a7f3d0;
  background: #ecfdf5;
  color: #059669;
}
.device-connection.state-failed {
  border-color: #fecaca;
  background: #fef2f2;
  color: #b91c1c;
}
.device-connection i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.device-summary-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 10px;
  color: #64748b;
  font:
    13px/1.6 Consolas,
    monospace;
}
.device-summary-meta > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.device-summary-meta > span + span {
  flex: 1;
  padding-left: 12px;
  border-left: 1px solid #cbd5e1;
}
.empty-list {
  grid-column: 1 / -1;
  margin: 0;
  padding: 28px 16px;
  text-align: center;
  color: var(--text-secondary);
  font-size: 14px;
}
.read-paused {
  margin: 16px 0 0;
  color: var(--text-secondary);
  font-size: 13px;
}
@media (max-width: 520px) {
  .overview-heading {
    align-items: flex-start;
  }
  .status-overview {
    padding: 12px;
  }
  .status-metrics {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px 6px;
  }
  .status-metric {
    gap: 6px;
  }
  .metric-icon {
    width: 20px;
    height: 20px;
    padding: 0;
    font-size: 18px;
    background: none;
  }
  .status-metric strong {
    font-size: 20px;
  }
  .status-search {
    padding: 12px;
    gap: 10px;
  }
  .status-search :deep(.el-form-item) {
    width: 100%;
    margin-right: 0;
    flex: 1 1 100%;
  }
  .status-search :deep(.el-form-item:not(:last-child)) {
    flex-basis: 100%;
  }
  .status-search :deep(.el-form-item__content),
  .status-search :deep(.el-input),
  .status-search :deep(.el-select) {
    width: 100%;
  }
}
</style>
