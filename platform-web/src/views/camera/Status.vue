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
  vLoading,
} from 'element-plus'
import { CircleCheck, Warning, Clock, QuestionFilled, SwitchButton } from '@element-plus/icons-vue'
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

defineOptions({ name: 'CameraStatus' })
const route = useRoute()
const name = ref(''),
  observation = ref<ObservationFilter | null>(null)
const layer = ref<StatusLayer>('MEDIA')
const query = shallowRef<StatusSearch>({ page: 1, size: 20, layer: 'MEDIA' })
const drawerOpen = ref(false)
const plansOpen = ref(false)
const target = shallowRef<{ channel: StatusChannel; profileId: string } | null>(null)
watch(
  () => route?.query.name,
  (value) => {
    name.value = typeof value === 'string' ? value : ''
    observation.value = null
    search()
  },
  { immediate: true },
)
const { rows, result, total, loading, error, paused, refresh } = useStatusPage(
  query,
  computed(() => !drawerOpen.value && !plansOpen.value),
)
const summary = computed(() => result.value?.summary ?? null)
const stateLabels = computed(() => ({
  accessible: '检查通过',
  failed: '检查异常',
  checking: '检测中',
  stale: '结果过期',
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
const planGroups = computed(() => [
  ...new Set(
    rows.value
      .filter((row) => row.lifecycle === 'ENABLED')
      .flatMap((row) => (row.groupKey ? [row.groupKey] : [])),
  ),
])
const observationLabels = computed<Record<ObservationFilter, string>>(() => ({
  NEVER: '尚未检查',
  ACCESSIBLE: '最近检查通过',
  FAILED: '最近检查异常',
  UNSUPPORTED: '检测待适配',
}))
function profileName(profile: StatusProfile) {
  return (
    profile.name ||
    (profile.streamKind ? usageLabel(profile.streamKind) : `码流 ${profile.streamProfileId}`)
  )
}
const diagnosticTarget = computed(() =>
  target.value
    ? {
        cameraId: target.value.channel.cameraId,
        profiles: target.value.channel.profiles.map((profile) => ({
          streamProfileId: profile.streamProfileId,
          label: profileName(profile),
          usageHint: profile.streamKind ?? 'UNKNOWN',
        })),
      }
    : null,
)
const capacityHint = computed(() =>
  error.value instanceof ApiRequestError && error.value.code === 'CONFLICT'
    ? '请缩小设备/通道搜索范围；仍失败时提供请求标识给管理员核对码流数量与授权配置。'
    : '',
)
function search() {
  query.value = {
    page: 1,
    size: 20,
    layer: layer.value,
    name: name.value.trim() || undefined,
    observation: observation.value || undefined,
  }
}
function reset() {
  name.value = ''
  observation.value = null
  layer.value = 'MEDIA'
  search()
}
function changePage(page: number) {
  query.value = { ...query.value, page }
}
function showDiagnostic(channel: StatusChannel, profile: StatusProfile) {
  target.value = { channel, profileId: profile.streamProfileId }
  drawerOpen.value = true
}
</script>

<template>
  <div class="content-page camera-status-page">
    <section class="patrol-entry" aria-label="巡检任务入口">
      <strong>巡检任务</strong>
      <ElButton type="primary" size="large" @click="plansOpen = true">创建 / 管理任务</ElButton>
    </section>
    <ElForm class="search-form status-search" inline @submit.prevent="search">
      <ElFormItem label="设备 / 通道">
        <ElInput v-model="name" placeholder="搜索设备或通道名称" clearable maxlength="128" />
      </ElFormItem>
      <ElFormItem label="检查类型">
        <ElSelect v-model="layer" @change="observation = null">
          <ElOption value="MEDIA" label="视频收帧 / 平台状态" />
          <ElOption value="CONTROL" label="连接 / 平台状态" />
        </ElSelect>
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
        ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
        ><ElButton @click="reset">重置</ElButton></ElFormItem
      >
    </ElForm>
    <RequestError :error="error" />
    <p v-if="capacityHint" class="capacity-hint" role="alert">{{ capacityHint }}</p>
    <section class="status-overview" aria-label="当前页相机检查总览">
      <header class="overview-heading">
        <div>
          <h3>相机检查总览</h3>
          <span>当前页 {{ summary?.total ?? '—' }} 个通道</span>
        </div>
        <div class="overview-actions">
          <span>{{ result?.total ?? '—' }} 个通道</span>
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
    <TablePanel>
      <template #actions><span class="table-caption">相机概览</span></template>
      <template #tools><ElButton :loading="loading" @click="refresh">刷新结果</ElButton></template>
      <div v-loading="loading" class="camera-card-grid">
        <StatusCard
          v-for="channel in rows"
          :key="channel.cameraId"
          :channel="channel"
          @details="showDiagnostic"
        />
        <p v-if="!rows.length && !loading" class="empty-list">
          {{ error ? '状态未读取，请重试刷新' : result ? '暂无符合条件的通道' : '状态未读取' }}
        </p>
      </div>
      <p v-if="paused" class="read-paused" role="status">已暂停读取检查进度，请点击“刷新结果”。</p>
      <template #footer
        ><ElPagination
          :current-page="query.page"
          :page-size="query.size"
          :total="total"
          :disabled="loading || !result"
          layout="total, prev, pager, next"
          @current-change="changePage"
      /></template>
    </TablePanel>
    <PlanDialog
      v-if="plansOpen"
      :group-keys="planGroups"
      @close="plansOpen = false"
      @changed="refresh"
    />
    <ElDrawer
      v-model="drawerOpen"
      title="状态详情"
      size="min(560px, 100%)"
      :destroy-on-close="false"
    >
      <template v-if="target"
        ><div class="diagnostic-heading">
          <h3>{{ target.channel.name }}</h3>
          <p>
            {{ addressLabel(target.channel.accessAddress) }} ·
            {{ addressText(target.channel.accessAddress) }}
          </p>
          <p>{{ target.channel.deviceName || '未关联设备名称' }}</p>
        </div>
        <StatusPanel
          :camera="diagnosticTarget"
          :initial-profile-id="target.profileId"
          :initial-layer="query.layer"
          :active="drawerOpen"
      /></template>
    </ElDrawer>
  </div>
</template>

<style scoped>
.camera-status-page {
  min-width: 0;
}
.patrol-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 14px 16px;
  margin-bottom: 12px;
  border-left: 3px solid var(--el-color-primary);
  background: var(--el-bg-color);
  border-radius: 6px;
}
.patrol-entry strong {
  font-size: 16px;
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
.overview-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 12px;
}
.overview-actions > span,
.table-caption,
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
.diagnostic-heading h3 {
  margin: 0;
  font-size: 15px;
  overflow-wrap: anywhere;
}
.diagnostic-heading p {
  color: var(--text-secondary);
  overflow-wrap: anywhere;
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
  .overview-actions > span {
    display: none;
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
