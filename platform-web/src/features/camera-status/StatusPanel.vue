<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import { Clock, Refresh } from '@element-plus/icons-vue'
import type { StatusLayer } from '../../api/camera-status/types'
import { layerKey } from '../../api/camera-status/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { streamLabel } from '../camera/streamLabels'
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
  const title = streamLabel(profile, props.camera?.profiles, props.camera?.name)
  if (profile.width && profile.height) {
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
    : selectedProfile.value?.control.blockingReasonCode === 'STATUS_LAYER_NOT_APPLICABLE'
      ? ['MEDIA']
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
    <div class="status-panel-header">
      <div v-if="profileId" class="layer-switcher" role="group" aria-label="检查结果层级">
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
  border-top: 1px solid var(--sf-border-light);
  font-size: var(--camera-card-text, var(--sf-fs-body));
  line-height: 1.6;
}
.camera-status-panel.has-summary {
  margin-top: 0;
  padding-top: 0;
  border-top: 0;
}
.status-panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 12px;
  margin-bottom: 12px;
}
.layer-switcher {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px;
  border-radius: var(--sf-radius-md);
  background: var(--sf-surface-muted);
  border: 1px solid var(--sf-border);
}
.layer-tab {
  margin: 0;
  padding: 5px 14px;
  border: 0;
  border-radius: var(--sf-radius-sm);
  background: transparent;
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-sub);
  font-weight: 500;
  line-height: 1.4;
  cursor: pointer;
  transition:
    background-color 0.15s ease,
    color 0.15s ease,
    box-shadow 0.15s ease;
}
.layer-tab:hover {
  color: var(--sf-text-primary);
}
.layer-tab.is-active {
  background: var(--sf-surface);
  color: var(--sf-primary);
  font-weight: 600;
  box-shadow: var(--sf-shadow-xs);
}
.layer-tab:focus-visible,
.check-diagnostics > summary:focus-visible {
  outline: 2px solid var(--sf-primary);
  outline-offset: 2px;
}
.status-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}
.status-actions .el-select {
  flex: 1 1 170px;
  min-width: 155px;
  max-width: 220px;
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
  color: var(--sf-text-secondary);
  padding: 4px 6px;
}
.status-note {
  margin: 8px 0 12px;
  padding: 8px 12px;
  border-radius: var(--sf-radius-sm);
  background: var(--sf-surface-subtle);
  border: 1px solid var(--sf-border);
  color: var(--sf-text-secondary);
  font-size: var(--camera-card-meta, var(--sf-fs-sub));
  overflow-wrap: anywhere;
}
.check-execution {
  margin-top: 12px;
}
.check-diagnostics {
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-md);
  background: var(--sf-surface-subtle);
  color: var(--sf-text-regular);
  font-size: var(--sf-fs-sub);
}
.check-diagnostics > summary {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
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
  border-right: 1px solid var(--sf-text-muted);
  border-bottom: 1px solid var(--sf-text-muted);
  transform: rotate(45deg);
}
.check-diagnostics[open] > summary::after {
  transform: rotate(225deg);
}
.check-summary-state {
  margin-left: auto;
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-caption);
  font-weight: 400;
}
.check-diagnostics dl {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 16px;
  padding: 0 14px 12px;
  margin: 0;
}
.check-diagnostics dl > div {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  min-width: 0;
}
.check-diagnostics .check-field-wide {
  grid-column: 1 / -1;
}
.check-diagnostics dt {
  color: var(--sf-text-secondary);
}
.check-diagnostics dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
  user-select: text;
}
.check-diagnostics code {
  color: var(--sf-text-regular);
  font-family: var(--sf-font-mono);
  font-size: var(--sf-fs-caption);
  line-height: 1.6;
}
.status-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.status-footer .el-button {
  height: 30px;
  border-color: var(--sf-border);
  border-radius: var(--sf-radius-sm);
  background: var(--sf-surface);
  color: var(--sf-text-regular);
}
.status-footer .el-button:hover {
  border-color: var(--sf-border-strong);
  background: var(--sf-surface-subtle);
  color: var(--sf-text-primary);
}
@media (max-width: 420px) {
  .check-diagnostics dl {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
