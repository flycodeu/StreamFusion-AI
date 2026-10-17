<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElButton, ElEmpty, ElMessage, ElOption, ElSelect, ElTag, vLoading } from 'element-plus'
import {
  detectMediaRuntime,
  getMediaRuntime,
  selectMediaRuntime,
} from '../../api/media-runtime/api'
import type {
  MediaRuntimeCandidate,
  MediaRuntimeSnapshot,
  RuntimeSelection,
} from '../../api/media-runtime/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'MediaRuntimePanel' })
const captureScope = usePageScope()
const snapshot = ref<MediaRuntimeSnapshot | null>(null),
  error = ref<unknown>(null)
const loading = ref(false),
  detecting = ref(false),
  saving = ref(false)
const selection = ref<RuntimeSelection>({ zlmId: null, ffmpegId: null })
const tools = [
  { kind: 'ZLM', title: 'ZLMediaKit', key: 'zlmId' },
  { kind: 'FFMPEG', title: 'FFmpeg', key: 'ffmpegId' },
] as const
const dirty = computed(
  () =>
    !!snapshot.value &&
    (selection.value.zlmId !== snapshot.value.selected.zlmId ||
      selection.value.ffmpegId !== snapshot.value.selected.ffmpegId),
)
const missingSelections = computed(() =>
  tools.filter((tool) => selection.value[tool.key] && !selected(tool.key)),
)
const statuses: Record<string, string> = {
  READY: '可用',
  API_AVAILABLE: 'API 可用',
  STOPPED: '未运行',
  UNAVAILABLE: '不可用',
  CONFIG_MISSING: '配置缺失',
  PROBE_FAILED: '检测失败',
}
let refreshTimer: ReturnType<typeof globalThis.setInterval> | undefined
let controller: globalThis.AbortController | undefined
function candidates(kind: string) {
  return snapshot.value?.candidates.filter((row) => row.kind === kind) ?? []
}
function selected(key: keyof RuntimeSelection) {
  return snapshot.value?.candidates.find((row) => row.id === selection.value[key])
}
function normalizeSelection(value: RuntimeSelection): RuntimeSelection {
  return { zlmId: value.zlmId || null, ffmpegId: value.ffmpegId || null }
}
function versionName(row: MediaRuntimeCandidate) {
  return row.softwareVersion || '版本未识别'
}
async function load(detect = false, background = false) {
  if (loading.value || detecting.value || saving.value || (background && dirty.value)) return
  const active = captureScope(),
    preserveSelection = dirty.value,
    selectionAtStart = { ...selection.value }
  controller = new globalThis.AbortController()
  if (detect) detecting.value = true
  else loading.value = true
  if (!background) error.value = null
  try {
    const result = await (detect
      ? detectMediaRuntime(controller.signal)
      : getMediaRuntime(controller.signal))
    if (!active()) return
    const editedWhileReading =
      selection.value.zlmId !== selectionAtStart.zlmId ||
      selection.value.ffmpegId !== selectionAtStart.ffmpegId
    snapshot.value = result
    if (!preserveSelection && !editedWhileReading) selection.value = { ...result.selected }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) {
      loading.value = false
      detecting.value = false
    }
  }
}
async function save() {
  if (!snapshot.value || saving.value || !dirty.value) return
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    if (missingSelections.value.length) return
    const result = await selectMediaRuntime(
      snapshot.value.version,
      normalizeSelection(selection.value),
    )
    if (active()) {
      snapshot.value = result
      selection.value = { ...result.selected }
      ElMessage.success('媒体运行环境已保存')
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) saving.value = false
  }
}
onMounted(() => {
  void load()
  refreshTimer = globalThis.setInterval(() => {
    void load(false, true)
  }, 30000)
})
onBeforeUnmount(() => {
  if (refreshTimer) globalThis.clearInterval(refreshTimer)
  controller?.abort()
})
</script>

<template>
  <section class="runtime-settings" aria-label="媒体运行环境">
    <header class="settings-heading">
      <div>
        <h2>媒体运行环境</h2>
        <span>{{ snapshot?.os || '—' }} · {{ formatDateTime(snapshot?.checkedAt) }}</span>
      </div>
      <div class="settings-actions">
        <ElButton :loading="detecting" :disabled="saving || loading" @click="load(true)"
          >重新检测</ElButton
        ><ElButton
          type="primary"
          :loading="saving"
          :disabled="!dirty || detecting || loading || missingSelections.length > 0"
          @click="save"
          >保存选择</ElButton
        >
      </div>
    </header>
    <RequestError :error="error" />
    <p v-if="missingSelections.length" class="runtime-diagnostic" role="status">
      {{ missingSelections.map((tool) => tool.title).join('、') }}
      的已选版本未被发现，请重新选择或清空后保存。
    </p>
    <div v-loading="loading && !snapshot" class="runtime-tools">
      <section v-for="tool in tools" :key="tool.kind" class="runtime-tool">
        <header>
          <h3>{{ tool.title }}</h3>
          <ElTag
            v-if="selected(tool.key)"
            :type="selected(tool.key)?.status === 'READY' ? 'success' : 'warning'"
            effect="plain"
            >{{ statuses[selected(tool.key)?.status || ''] || '未检测' }}</ElTag
          ><ElTag v-else type="info" effect="plain">{{
            selection[tool.key] ? '选择已失效' : '未选择'
          }}</ElTag>
        </header>
        <label :for="`${tool.kind}-version`">使用版本</label>
        <ElSelect
          :id="`${tool.kind}-version`"
          v-model="selection[tool.key]"
          clearable
          :disabled="saving || detecting"
          :placeholder="candidates(tool.kind).length ? '选择运行环境' : '未检测到运行环境'"
          class="runtime-select"
          popper-class="media-runtime-options"
          :fit-input-width="true"
        >
          <ElOption
            v-for="row in candidates(tool.kind)"
            :key="row.id"
            :value="row.id"
            :label="versionName(row)"
            ><div class="version-option">
              <strong>{{ versionName(row) }}</strong
              ><span :title="row.executablePath">{{ row.executablePath }}</span>
            </div></ElOption
          >
        </ElSelect>
        <template v-if="selected(tool.key)">
          <dl class="runtime-details">
            <div>
              <dt>程序位置</dt>
              <dd>{{ selected(tool.key)?.executablePath }}</dd>
            </div>
            <div v-if="tool.kind === 'ZLM'">
              <dt>配置文件</dt>
              <dd>{{ selected(tool.key)?.configPath || '未检测到' }}</dd>
            </div>
            <div v-if="tool.kind === 'ZLM'">
              <dt>服务地址</dt>
              <dd>{{ selected(tool.key)?.apiBase || '未配置' }}</dd>
            </div>
            <div>
              <dt>{{ tool.kind === 'FFMPEG' ? '调用状态' : '进程状态' }}</dt>
              <dd>
                {{
                  selected(tool.key)?.processRunning
                    ? '运行中'
                    : tool.kind === 'FFMPEG'
                      ? selected(tool.key)?.status === 'READY'
                        ? '按需运行（当前空闲）'
                        : '暂无进程'
                      : '未运行'
                }}<span v-if="selected(tool.key)?.pids.length" class="detail-secondary">{{
                  selected(tool.key)?.pids.join('、')
                }}</span>
              </dd>
            </div>
            <div v-if="selected(tool.key)?.ports.length">
              <dt>监听端口</dt>
              <dd class="runtime-ports">
                <span
                  v-for="port in selected(tool.key)?.ports"
                  :key="`${port.protocol}-${port.port}`"
                  :class="{ reachable: port.reachable }"
                  >{{ port.protocol }} {{ port.port }}</span
                >
              </dd>
            </div>
          </dl>
          <p v-if="selected(tool.key)?.diagnostic" class="runtime-diagnostic">
            {{ selected(tool.key)?.diagnostic?.message }}
          </p>
        </template>
        <ElEmpty
          v-else
          :image-size="70"
          :description="
            selection[tool.key] ? '已选版本未被发现，请重新选择或清空' : '请选择检测到的运行环境'
          "
        />
        <details v-if="candidates(tool.kind).length" class="detected-versions">
          <summary>已检测到 {{ candidates(tool.kind).length }} 个版本</summary>
          <ul>
            <li v-for="row in candidates(tool.kind)" :key="row.id">
              <strong>{{ versionName(row) }}</strong
              ><span>{{ row.executablePath }}</span
              ><ElTag
                size="small"
                :type="row.status === 'READY' ? 'success' : 'info'"
                effect="plain"
                >{{ statuses[row.status] || row.status }}</ElTag
              >
            </li>
          </ul>
        </details>
      </section>
    </div>
    <div v-if="snapshot?.scanDiagnostics.length" class="scan-diagnostics">
      <p v-for="(item, index) in snapshot.scanDiagnostics" :key="index">{{ item.message }}</p>
    </div>
  </section>
</template>

<style scoped>
.runtime-settings {
  display: grid;
  gap: 20px;
}
.settings-heading,
.settings-actions,
.runtime-tool > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.settings-heading h2 {
  margin: 0 0 8px;
  font-size: 20px;
}
.settings-heading span {
  color: var(--text-secondary);
  font-size: 13px;
}
.runtime-tools {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 22px;
}
.runtime-tool {
  min-width: 0;
  padding: clamp(20px, 2vw, 30px);
  background: var(--el-bg-color);
  border: 1px solid var(--border-subtle);
  border-radius: 8px;
}
.runtime-tool h3 {
  margin: 0;
  font-size: 19px;
  font-weight: 600;
}
.runtime-tool > label {
  display: block;
  margin: 26px 0 9px;
  font-size: 14px;
  color: var(--text-secondary);
}
.runtime-select {
  width: 100%;
}
.runtime-details {
  margin: 24px 0 0;
  display: grid;
  gap: 18px;
}
.runtime-details > div {
  display: grid;
  grid-template-columns: 90px 1fr;
  gap: 16px;
  line-height: 1.7;
}
dt {
  color: var(--text-secondary);
  font-size: 13px;
}
dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 14px;
}
.detail-secondary {
  margin-left: 12px;
  color: var(--text-secondary);
}
.runtime-ports {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}
.runtime-ports span {
  background: #f1f3f5;
  border-radius: 3px;
  padding: 1px 7px;
  color: #7d838b;
  font-size: 12px;
}
.runtime-ports .reachable {
  color: #20876b;
  background: #edf8f3;
}
.runtime-diagnostic {
  background: #fff8eb;
  color: #906a2d;
  padding: 12px;
  border-radius: 4px;
  font-size: 13px;
  line-height: 1.6;
}
.detected-versions {
  border-top: 1px solid var(--border-subtle);
  margin-top: 28px;
  padding-top: 18px;
  font-size: 13px;
}
.detected-versions summary {
  cursor: pointer;
  color: var(--text-secondary);
}
.detected-versions ul {
  margin: 12px 0 0;
  padding: 0;
  list-style: none;
}
.detected-versions li {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 6px;
  padding: 12px 0;
}
.detected-versions li > span {
  grid-row: 2;
  overflow-wrap: anywhere;
  color: var(--text-secondary);
}
.detected-versions li :deep(.el-tag) {
  grid-column: 2;
  grid-row: 1 / 3;
  align-self: center;
}
.version-option {
  display: grid;
  gap: 3px;
  line-height: 1.5;
  padding: 3px 0;
  min-width: 0;
}
.version-option strong {
  white-space: normal;
  overflow-wrap: anywhere;
}
.version-option span {
  color: var(--text-secondary);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.scan-diagnostics {
  color: var(--text-secondary);
  font-size: 13px;
}
@media (max-width: 900px) {
  .runtime-tools {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 560px) {
  .settings-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .settings-actions {
    align-self: flex-end;
  }
  .runtime-details > div {
    grid-template-columns: 72px 1fr;
    gap: 10px;
  }
}
</style>

<style>
.media-runtime-options {
  max-width: calc(100vw - 24px);
}
.media-runtime-options .el-select-dropdown__item {
  height: auto;
  min-height: 60px;
  padding: 8px 32px 8px 16px;
  line-height: 1.5;
  white-space: normal;
}
</style>
