<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElButton,
  ElDialog,
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
import { streamLabel } from '../../features/camera/streamLabels'
import type { ConnectionState } from '../../features/camera-status/health'
import {
  summaryLabels as stateLabels,
  observationLabels,
} from '../../features/camera-status/presentation'
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
    ? stateOrder.map((state) => `${stateLabels[state]} ${summary.value![state]}`).join('，')
    : '状态尚未读取',
)
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
function profileOptionLabel(profile: CameraProfile): string {
  const title = streamLabel(profile, diagnosticTarget.value?.profiles, diagnosticTarget.value?.name)
  if (profile.width && profile.height) {
    return `${title} (${profile.width}×${profile.height})`
  }
  return title
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
      <div class="overview-top">
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
            :class="[`state-${state}`, { 'is-active': (summary?.[state] ?? 0) > 0 }]"
          >
            <span class="metric-label">
              <ElIcon class="metric-icon" aria-hidden="true"
                ><component :is="stateIcons[state]"
              /></ElIcon>
              <span>{{ stateLabels[state] }}</span>
            </span>
            <strong>{{ summary?.[state] ?? '—' }}</strong>
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
    <ElDialog
      v-model="drawerOpen"
      title="状态详情"
      width="680px"
      align-center
      append-to-body
      class="management-dialog status-detail-dialog"
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
                  <div class="device-summary-icon-wrap" aria-hidden="true">
                    <ElIcon class="device-summary-icon"><VideoCamera /></ElIcon>
                  </div>
                  <div class="device-summary-title">
                    <h3 :title="target.channel.deviceName || target.channel.name">
                      {{ target.channel.deviceName || target.channel.name }}
                    </h3>
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
                  </div>
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
                <div
                  v-if="channelTotal > 1 || (diagnosticTarget?.profiles.length ?? 0) > 1"
                  class="detection-target-options"
                >
                  <span class="detection-target-label">检测目标</span>
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
                        :label="profileOptionLabel(profile)"
                      />
                    </ElSelect>
                    <ElButton
                      v-if="channelOptions.length < channelTotal"
                      :loading="channelLoading"
                      @click="loadChannels"
                      >加载更多</ElButton
                    >
                  </div>
                </div>
              </section>
              <RequestError :error="channelError" advice="可重新打开状态详情以加载通道选项。" />
            </template>
          </StatusPanel>
        </div>
      </template>
    </ElDialog>
  </div>
</template>

<style>
.el-dialog.management-dialog.status-detail-dialog {
  width: min(680px, calc(100vw - 32px));
  max-height: calc(100dvh - 64px);
  margin: auto;
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.14);
}
.status-detail-dialog > .el-dialog__header {
  padding: 16px 48px 16px 22px;
}
.status-detail-dialog .el-dialog__title {
  font-size: 16px;
  font-weight: 600;
  color: #0f172a;
}
.status-detail-dialog > .el-dialog__body {
  padding: 18px 22px 20px;
}
</style>

<style scoped>
.camera-status-page {
  min-width: 0;
}
.status-overview {
  margin-bottom: 14px;
  padding: 12px 16px;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--el-bg-color);
}
.overview-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 20px;
}
.overview-heading {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}
.overview-heading > div:first-child {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.overview-heading h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}
.overview-heading > div:first-child > span {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  color: #475569;
  font-size: 12px;
  font-weight: 500;
}
.status-metrics {
  display: grid;
  grid-template-columns: repeat(6, minmax(112px, 1fr));
  gap: 8px;
  flex: 1 1 700px;
}
.status-metric {
  --state-soft-bg: #f8fafc;
  --state-soft-border: #e2e8f0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
  padding: 7px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  transition:
    background-color 0.15s ease,
    border-color 0.15s ease;
}
.status-metric.is-active {
  background: var(--state-soft-bg);
  border-color: var(--state-soft-border);
}
.metric-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: #475569;
  font-size: 12px;
  white-space: nowrap;
}
.metric-icon {
  flex-shrink: 0;
  color: var(--state-color);
  font-size: 15px;
}
.status-metric strong {
  font-size: 15px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1;
  color: #0f172a;
}
.status-metric.is-active strong {
  color: var(--state-color);
}
.state-accessible {
  --state-color: #059669;
  --state-soft-bg: #ecfdf5;
  --state-soft-border: #a7f3d0;
}
.state-failed {
  --state-color: #dc2626;
  --state-soft-bg: #fef2f2;
  --state-soft-border: #fecaca;
}
.state-checking {
  --state-color: #2563eb;
  --state-soft-bg: #eff6ff;
  --state-soft-border: #bfdbfe;
}
.state-stale {
  --state-color: #d97706;
  --state-soft-bg: #fffbeb;
  --state-soft-border: #fde68a;
}
.state-unknown,
.state-disabled {
  --state-color: #64748b;
  --state-soft-bg: #f1f5f9;
  --state-soft-border: #cbd5e1;
}
.status-distribution {
  display: flex;
  height: 5px;
  margin-top: 10px;
  border-radius: 999px;
  overflow: hidden;
  background: #f1f5f9;
}
.status-distribution > span {
  background: var(--state-color);
}
.status-search {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px 20px;
  padding: 14px 18px;
}
.status-search :deep(.el-form-item) {
  min-width: 0;
  margin: 0;
}
.status-search :deep(.el-input) {
  width: 220px;
}
.status-search :deep(.el-select) {
  width: 180px;
}
.capacity-hint {
  margin: 8px 0 16px;
  color: var(--text-secondary);
  font-size: 13px;
}
.camera-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 275px), 1fr));
  gap: 14px;
  align-items: stretch;
  min-height: 100px;
}
.status-device-summary {
  padding: 16px;
  margin-bottom: 16px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}
.device-summary-heading {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.device-summary-icon-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
  flex-shrink: 0;
}
.device-summary-icon {
  font-size: 20px;
}
.device-summary-title {
  flex: 1;
  min-width: 0;
}
.device-summary-title h3 {
  margin: 0;
  overflow: hidden;
  color: #0f172a;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.4;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.device-connection {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 99px;
  background: #fff;
  color: #64748b;
  font-size: 12px;
  font-weight: 600;
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
  flex-wrap: wrap;
  gap: 6px 12px;
  margin-top: 6px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
  font-variant-numeric: tabular-nums;
}
.device-summary-meta > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.device-summary-meta > span + span {
  padding-left: 12px;
  border-left: 1px solid #cbd5e1;
}
.detection-target-options {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px 14px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid #e2e8f0;
  color: #334155;
  font-size: 13px;
}
.detection-target-label {
  color: #475569;
  font-weight: 600;
  flex-shrink: 0;
}
.detection-target-controls {
  display: flex;
  flex-wrap: wrap;
  flex: 1;
  gap: 10px;
  min-width: 0;
}
.detection-target-controls :deep(.el-select) {
  flex: 1 1 180px;
  min-width: 0;
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
@media (max-width: 960px) {
  .status-metrics {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
@media (max-width: 520px) {
  .overview-heading {
    align-items: flex-start;
  }
  .status-overview {
    padding: 12px;
  }
  .status-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
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
