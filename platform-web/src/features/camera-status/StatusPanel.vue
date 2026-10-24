<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import { Clock, Refresh } from '@element-plus/icons-vue'
import type { StatusLayer } from '../../api/camera-status/types'
import { layerKey } from '../../api/camera-status/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { effectiveUsage, streamLabel, usageLabel } from '../camera/form'
import { formatDateTime } from '../../utils/dateTime'
import { executionLabels, isTerminal, reasonLabel } from './model'
import { connectionLabels, upstreamLabels } from './health'
import type { ConnectionState } from './health'
import StatusFact from './StatusFact.vue'
import StatusHistory from './StatusHistory.vue'
import { useStatusChecks } from './useStatusChecks'

const props = defineProps<{
  camera: {
    cameraId: string
    name?: string
    profiles: {
      streamProfileId: string
      label: string
      usageHint: string
      width?: number | null
      height?: number | null
      classification?: { usageHint: string; origin: string }
    }[]
  } | null
  active: boolean
  initialProfileId?: string | null
  initialLayer?: StatusLayer
  showProfileSelector?: boolean
}>()
function profileOptionLabel(profile: NonNullable<typeof props.camera>['profiles'][number]): string {
  const base = streamLabel(profile)
  const role = effectiveUsage(profile)
  const hasKnownRole = role && role !== 'UNKNOWN' && role !== 'CUSTOM'
  const isDuplicateOrSameAsCamera =
    (props.camera?.name && base === props.camera.name) ||
    (props.camera?.profiles.filter((p) => streamLabel(p) === base).length ?? 0) > 1
  const title = isDuplicateOrSameAsCamera && hasKnownRole ? usageLabel(role) : base
  if (isDuplicateOrSameAsCamera && profile.width && profile.height) {
    return `${title} (${profile.width}×${profile.height})`
  }
  return title
}
const profileId = ref<string | null>(null)
const layer = ref<StatusLayer>(props.initialLayer ?? 'CONTROL')
const historyOpen = ref(false)
const cameraId = computed(() => props.camera?.cameraId ?? null)
const active = computed(() => props.active)
watch(
  [cameraId, () => props.initialProfileId],
  () => {
    profileId.value = props.initialProfileId ?? null
    layer.value = props.initialLayer ?? 'CONTROL'
  },
  { immediate: true, flush: 'sync' },
)
watch(
  () => props.active,
  (active) => {
    if (active) {
      if (props.initialProfileId) profileId.value = props.initialProfileId
      layer.value = props.initialLayer ?? 'CONTROL'
    }
  },
  { flush: 'sync' },
)
watch(
  () => props.camera?.profiles,
  (profiles) => {
    if (profileId.value && !profiles?.some((item) => item.streamProfileId === profileId.value))
      profileId.value = null
  },
  { flush: 'sync' },
)
const {
  selectedProfile,
  selected,
  check,
  pending,
  pendingHere,
  reading,
  posting,
  readPaused,
  error,
  refresh,
  submit,
} = useStatusChecks(cameraId, profileId, active, layer)
const availableLayers = computed<StatusLayer[]>(() => {
  const upstream = selectedProfile.value?.upstream
  return upstream && upstream.blockingReasonCode !== 'STATUS_LAYER_NOT_APPLICABLE'
    ? ['UPSTREAM']
    : ['CONTROL', 'MEDIA']
})
watch(availableLayers, (values) => {
  if (!values.includes(layer.value)) layer.value = values[0]!
})
watch(
  () => props.initialLayer,
  (value) => {
    if (value) layer.value = value
  },
)
function canCheck(target: StatusLayer) {
  if (!props.active || posting.value || !profileId.value) return false
  if (pending.value) return pendingHere.value && pending.value.input.layer === target
  const state = selectedProfile.value?.[layerKey(target)]
  const latest = layer.value === target ? check.value : state?.latestCheck
  return !!state?.canCheck && (!latest || isTerminal(latest))
}
async function inspect(target: StatusLayer) {
  if (!canCheck(target)) return
  layer.value = target
  await submit()
}
const targetError = computed(() =>
  pendingHere.value ? (pending.value?.error ?? error.value) : error.value,
)
const executionReason = computed(() => {
  if (!check.value?.reasonCode) return ''
  const fact = selected.value?.observation
  if (fact?.checkId === check.value.checkId && fact.reasonCode === check.value.reasonCode) return ''
  return reasonLabel(check.value.reasonCode)
})
const connection = computed(() => {
  const upstream = selectedProfile.value?.upstream
  const profile =
    upstream && upstream.blockingReasonCode !== 'STATUS_LAYER_NOT_APPLICABLE'
      ? upstream
      : selectedProfile.value?.control
  const platform = profile?.layer === 'UPSTREAM'
  let state: ConnectionState = 'unknown'
  if (profile?.latestCheck && !isTerminal(profile.latestCheck)) state = 'checking'
  else if (profile?.observation) {
    if (!profile.configurationCurrent || profile.freshness !== 'CURRENT') state = 'stale'
    else if (profile.observation.value === 'ACCESSIBLE') state = 'accessible'
    else if (profile.observation.value === 'FAILED') state = 'failed'
  }
  return {
    state,
    label: (platform ? upstreamLabels : connectionLabels)[state],
    title: platform ? '最近一次平台状态查询结果' : '最近一次相机连接检查结果',
  }
})
</script>

<template>
  <section
    v-if="camera"
    class="camera-status-panel"
    :class="{ 'has-summary': !!$slots.summary }"
    aria-label="相机状态检查"
  >
    <slot name="summary" :connection="connection" />
    <div class="status-actions">
      <ElSelect
        v-if="showProfileSelector !== false && camera.profiles.length > 1"
        v-model="profileId"
        placeholder="选择要检查的码流"
        aria-label="检查码流"
        :disabled="posting"
      >
        <ElOption
          v-for="profile in camera.profiles"
          :key="profile.streamProfileId"
          :label="profileOptionLabel(profile)"
          :value="profile.streamProfileId"
        />
      </ElSelect>
      <div class="check-buttons">
        <ElButton
          v-for="target in availableLayers"
          :key="target"
          :type="target === 'CONTROL' || target === 'UPSTREAM' ? 'primary' : 'default'"
          :loading="posting && pending?.input.layer === target"
          :disabled="!canCheck(target)"
          @click="inspect(target)"
        >
          {{
            pending?.uncertain && pending.input.layer === target
              ? '重试原检查'
              : target === 'UPSTREAM'
                ? '查询平台状态'
                : target === 'MEDIA'
                  ? '检查视频'
                  : '检查连接'
          }}
        </ElButton>
      </div>
      <ElButton
        class="status-refresh"
        link
        :icon="Refresh"
        :loading="reading"
        :disabled="!active || reading"
        @click="refresh"
        >刷新</ElButton
      >
    </div>
    <p v-if="pending?.uncertain" class="status-note" role="status">
      {{
        pendingHere
          ? '响应未确认，重试将使用原检查请求。'
          : `通道 ${pending.cameraId} 的码流 ${pending.input.streamProfileId} 有未确认请求，请返回原目标重试。`
      }}
    </p>
    <p v-else-if="selected && !selected.canCheck" class="status-note">
      {{ reasonLabel(selected.blockingReasonCode) || '当前不能执行手动检查' }}
    </p>
    <template v-if="profileId">
      <div class="layer-switcher" role="group" aria-label="检查结果层级">
        <button
          v-for="target in availableLayers"
          :key="target"
          type="button"
          class="layer-tab"
          :class="{ 'is-active': layer === target }"
          :aria-pressed="layer === target"
          @click="layer = target"
        >
          {{ target === 'UPSTREAM' ? '平台状态' : target === 'MEDIA' ? '视频收帧' : '连接检查' }}
        </button>
      </div>
      <StatusFact :profile="selected" />
      <div v-if="check" class="check-execution" aria-live="polite">
        <p v-if="executionReason" class="status-note">{{ executionReason }}</p>
        <details class="check-diagnostics">
          <summary>
            <span>检查信息</span>
            <span class="check-summary-state">{{ executionLabels[check.executionState] }}</span>
          </summary>
          <dl>
            <div>
              <dt>检查标识</dt>
              <dd>
                <code>{{ check.checkId }}</code>
              </dd>
            </div>
            <div>
              <dt>请求时间</dt>
              <dd>{{ formatDateTime(check.requestedAt) }}</dd>
            </div>
            <div v-if="check.reasonCode" class="check-field-wide">
              <dt>原因码</dt>
              <dd>
                <code>{{ check.reasonCode }}</code>
              </dd>
            </div>
            <div class="check-field-wide">
              <dt>请求标识</dt>
              <dd>
                <code>{{ check.traceId }}</code>
              </dd>
            </div>
          </dl>
        </details>
      </div>
    </template>
    <p v-if="readPaused" class="status-note" role="status">已暂停读取，请点击“刷新”查询进度。</p>
    <RequestError :error="targetError" />
    <div v-if="profileId" class="status-footer">
      <ElButton
        size="small"
        :icon="Clock"
        :aria-expanded="historyOpen"
        @click="historyOpen = !historyOpen"
        >{{ historyOpen ? '收起历史检测记录' : '查看历史检测记录' }}</ElButton
      >
    </div>
    <StatusHistory
      v-if="historyOpen && profileId"
      :camera-id="camera.cameraId"
      :profile-id="profileId"
      :layer="layer"
      :active="active"
    />
  </section>
</template>

<style scoped>
.camera-status-panel {
  margin-top: var(--camera-card-section-gap, 20px);
  padding-top: var(--camera-card-section-gap, 20px);
  border-top: 1px solid var(--el-border-color-lighter);
  font-size: var(--camera-card-text, 14px);
  line-height: 1.6;
}
.camera-status-panel.has-summary {
  margin-top: 0;
  padding-top: 0;
  border-top: 0;
}
.status-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}
.status-actions .el-select {
  flex: 1 1 200px;
  min-width: 175px;
  max-width: 260px;
}
.status-actions .el-button {
  flex-shrink: 0;
  margin: 0;
}
.check-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.status-actions .status-refresh {
  margin-left: auto;
  color: #64748b;
}
.layer-switcher {
  display: flex;
  gap: 24px;
  margin-bottom: 16px;
  border-bottom: 1px solid #e2e8f0;
}
.layer-tab {
  margin: 0 0 -1px;
  padding: 0 2px 10px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: #64748b;
  font-size: 13px;
  cursor: pointer;
}
.layer-tab:hover,
.layer-tab.is-active {
  color: #2563eb;
}
.layer-tab.is-active {
  border-bottom-color: #2563eb;
  font-weight: 500;
}
.layer-tab:focus-visible,
.check-diagnostics > summary:focus-visible {
  outline: 2px solid #2563eb;
  outline-offset: 3px;
}
.status-note {
  margin: 8px 0;
  color: var(--text-secondary);
  font-size: var(--camera-card-meta, 13px);
  overflow-wrap: anywhere;
}
.check-execution {
  margin-top: 16px;
}
.check-diagnostics {
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font-size: 13px;
}
.check-diagnostics > summary {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  cursor: pointer;
  font-weight: 500;
  list-style: none;
}
.check-diagnostics > summary::-webkit-details-marker {
  display: none;
}
.check-diagnostics > summary::after {
  content: '';
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  margin-right: 3px;
  border-right: 1px solid #94a3b8;
  border-bottom: 1px solid #94a3b8;
  transform: rotate(45deg);
}
.check-diagnostics[open] > summary::after {
  transform: rotate(225deg);
}
.check-summary-state {
  margin-left: auto;
  color: #64748b;
  font-size: 12px;
  font-weight: 400;
}
.check-diagnostics dl {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 20px;
  padding: 0 14px 14px;
  margin: 0;
}
.check-diagnostics dl > div {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  min-width: 0;
}
.check-diagnostics .check-field-wide {
  grid-column: 1 / -1;
}
.check-diagnostics dt {
  color: #64748b;
}
.check-diagnostics dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
  user-select: text;
}
.check-diagnostics code {
  color: #64748b;
  font:
    12px/1.6 Consolas,
    monospace;
}
.status-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.status-footer .el-button {
  height: 28px;
  border-color: #dbe2ea;
  border-radius: 6px;
  background: #fff;
  color: #334155;
}
.status-footer .el-button:hover {
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #0f172a;
}
@media (max-width: 420px) {
  .check-diagnostics dl {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
