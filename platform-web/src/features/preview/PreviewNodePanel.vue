<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElButton, ElDialog, ElTag } from 'element-plus'
import { RouterLink } from 'vue-router'
import { getMediaRuntime } from '../../api/media-runtime/api'
import type { MediaRuntimeCandidate } from '../../api/media-runtime/types'
import {
  getPreviewNodes,
  getDirectZlmInstances,
  createDirectZlmInstance,
  saveDirectZlmInstance,
  rotateDirectZlmSecret,
  deleteDirectZlmInstance,
  getOpsPreviewInstances,
  importOpsPreviewInstance,
  getOpsZlmInstances,
  importOpsZlmInstance,
  configurePreviewNode,
  previewNodeAction,
  registerPreviewNode,
  savePreviewNode,
  type PreviewCluster,
  type DirectZlmInstances,
  type DirectZlmInstance,
  type DirectZlmInput,
  type OpsPreviewInstances,
  type OpsZlmInstances,
  type PreviewNodeStatus,
  type PreviewNodeConnectionInput,
  previewMaintenance,
  type PreviewMaintenance,
} from '../../api/preview-node/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { ApiRequestError } from '../../lib/http/error'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import { formatDateTime } from '../../utils/dateTime'
import { labelOf } from '../../utils/display'
import { previewReason } from './model'
import {
  previewNodeLabels as names,
  previewMaintenanceLabels as maintenanceNames,
} from './nodePresentation'
import ZlmNetworkLoad from './ZlmNetworkLoad.vue'
import PreviewOpsSync from './PreviewOpsSync.vue'

const props = withDefaults(defineProps<{ management?: boolean }>(), { management: false })
const captureScope = usePageScope()
const snapshot = ref<PreviewCluster | null>(null),
  busy = ref(false),
  error = ref<unknown>(null)
const displayClock = ref(Date.now())
const snapshotRefreshedAt = ref(0)
let lastObservationAttemptAt = Date.now()
const localZlm = ref<MediaRuntimeCandidate | null>(null)
const opsInstances = ref<OpsPreviewInstances | null>(null)
const opsZlmInstances = ref<OpsZlmInstances | null>(null)
const opsBusy = ref(false)
const opsError = ref<unknown>(null)
const opsZlmBusy = ref(false)
const opsZlmError = ref<unknown>(null)
const directInstances = ref<DirectZlmInstances | null>(null)
const directBusy = ref(false)
const directError = ref<unknown>(null)
const directAdvice = computed(() => {
  const failure = directError.value
  if (!(failure instanceof ApiRequestError)) return undefined
  if (failure.status === 403) return '请核对超级管理员权限及当前 Platform 服务版本。'
  if (failure.code === 'NODE_CREDENTIAL_DIRECTORY_MISSING')
    return '平台无法保存 ZLM 密钥，请检查服务账号的私有存储权限。'
  if (failure.code === 'ZLM_UNAVAILABLE')
    return '请检查 ZLM 地址、端口及 Platform 的实际出口 IP 是否被允许访问。'
  if (failure.code === 'ZLM_REQUEST_FAILED' || failure.code === 'ZLM_UNAUTHORIZED')
    return '请核对 ZLM secret 及 ZLM 的 API 访问策略。'
  if (failure.code === 'NODE_MEDIA_CONFIGURATION_INVALID')
    return '请核对 ZLM 的 rtc.externIP 与媒体地址一致、rtc.port 和 rtsp.port 可用、rtsp.directProxy=0、空闲保留至少 20 秒，并限制 API 调试和访问 IP 范围。'
  return undefined
})
const directForm = ref<DirectZlmInput | null>(null)
const directEditing = ref<{
  instance: DirectZlmInstance
  priority: number
  budget: DirectZlmInstance['budget']
  installPath: string
} | null>(null)
const directCredential = ref<{ instance: DirectZlmInstance; secret: string } | null>(null)
const directRemoving = ref<DirectZlmInstance | null>(null)
const diagnostic = ref<PreviewNodeStatus | null>(null),
  editing = ref<PreviewNodeStatus | null>(null)
const connection = ref<PreviewNodeConnectionInput | null>(null)
const administrator = computed(() => !!sessionState.me?.isSuperAdmin)
const canManage = computed(() => props.management && administrator.value)
const configuredConnections = computed(() =>
  (snapshot.value?.registrationOptions ?? []).map((item) => ({
    ...item,
    registered: snapshot.value?.nodes.find((node) => node.nodeId === item.nodeId),
  })),
)
const maintaining = ref<PreviewNodeStatus | null>(null)
const synchronizing = ref<PreviewNodeStatus | null>(null)
const maintenance = ref<PreviewMaintenance | null>(null)
const defaultDirectBudget: DirectZlmInput['budget'] = {
  maxSources: 64,
  maxViewers: 64,
  maxTranscodes: 0,
  maxStarting: 4,
  ingressKbps: 655360,
  egressKbps: 655360,
  sourceUpperKbps: 8192,
  outputUpperKbps: 8192,
}
const fields: {
  key: keyof PreviewNodeStatus['budget']
  label: string
  min: number
  max: number
}[] = [
  { key: 'maxSources', label: '源流上限', min: 1, max: 64 },
  { key: 'maxViewers', label: '观看上限', min: 1, max: 64 },
  { key: 'maxTranscodes', label: '共享转码上限', min: 0, max: 64 },
  { key: 'maxStarting', label: '同时启动上限', min: 1, max: 4 },
  { key: 'ingressKbps', label: '入站预算（kbps）', min: 1, max: 100000000 },
  { key: 'egressKbps', label: '出口预算（kbps）', min: 1, max: 100000000 },
  { key: 'sourceUpperKbps', label: '每源码率上界（kbps）', min: 1, max: 1000000 },
  { key: 'outputUpperKbps', label: '每观看码率上界（kbps）', min: 1, max: 1000000 },
]
const directFields = fields.filter((field) => field.key !== 'maxTranscodes')
let controller: globalThis.AbortController | undefined
let runtimeController: globalThis.AbortController | undefined
let opsController: globalThis.AbortController | undefined
let opsZlmController: globalThis.AbortController | undefined
let directController: globalThis.AbortController | undefined
let observationController: globalThis.AbortController | undefined
let observationTimer: ReturnType<typeof globalThis.setInterval> | undefined
onBeforeUnmount(() => {
  controller?.abort()
  runtimeController?.abort()
  opsController?.abort()
  opsZlmController?.abort()
  directController?.abort()
  observationController?.abort()
  if (observationTimer) globalThis.clearInterval(observationTimer)
})
onMounted(() => {
  void run()
  void loadLocalZlm()
  observationTimer = globalThis.setInterval(() => {
    displayClock.value = Date.now()
    if (
      !busy.value &&
      snapshot.value?.nodes.some((node) => node.kind === 'ZLM_DIRECT') &&
      displayClock.value - lastObservationAttemptAt >= 15_000
    ) {
      lastObservationAttemptAt = displayClock.value
      void refreshObservation()
    }
  }, 5000)
})
watch(
  canManage,
  (enabled) => {
    if (enabled) {
      void loadOpsInstances()
      void loadOpsZlmInstances()
      void loadDirectInstances()
    } else {
      opsController?.abort()
      opsZlmController?.abort()
      opsController = undefined
      opsBusy.value = false
      opsError.value = null
      opsInstances.value = null
      opsZlmInstances.value = null
      opsZlmBusy.value = false
      opsZlmError.value = null
      directController?.abort()
      directController = undefined
      directBusy.value = false
      directError.value = null
      directInstances.value = null
      directForm.value = null
      directCredential.value = null
      synchronizing.value = null
    }
  },
  { immediate: true },
)

async function loadDirectInstances() {
  if (!canManage.value) return
  if (directBusy.value) return
  const active = captureScope()
  directController?.abort()
  const nextController = new globalThis.AbortController()
  directController = nextController
  directBusy.value = true
  directError.value = null
  directInstances.value = null
  try {
    const value = await getDirectZlmInstances(nextController.signal)
    if (active() && canManage.value) directInstances.value = value
  } catch (cause) {
    if (active() && canManage.value) directError.value = cause
  } finally {
    if (directController === nextController) directBusy.value = false
  }
}

function openDirectForm() {
  directForm.value = {
    name: '',
    apiHost: '',
    apiPort: 80,
    mediaAddress: '',
    installPath: null,
    secret: '',
    priority: 100,
    budget: { ...defaultDirectBudget },
  }
}
function saveDirectForm() {
  if (!directForm.value) return
  const input = {
    ...directForm.value,
    mediaAddress: directForm.value.mediaAddress || directForm.value.apiHost,
    installPath: directForm.value.installPath?.trim() || null,
    budget: { ...directForm.value.budget },
  }
  directForm.value.secret = ''
  void run(async () => {
    await createDirectZlmInstance(input)
    directForm.value = null
  }).then(() => void loadDirectInstances())
}
function updateDirect(
  instance: DirectZlmInstance,
  changes: Partial<Pick<DirectZlmInstance, 'mode' | 'priority' | 'budget' | 'installPath'>>,
) {
  void run(async () => {
    await saveDirectZlmInstance(instance, changes)
    directEditing.value = null
  }).then(() => void loadDirectInstances())
}
function saveDirectCredential() {
  if (!directCredential.value?.secret) return
  const { instance, secret } = directCredential.value
  directCredential.value.secret = ''
  void run(async () => {
    await rotateDirectZlmSecret(instance, secret)
    directCredential.value = null
  }).then(() => void loadDirectInstances())
}
function removeDirect() {
  if (!directRemoving.value) return
  const instance = directRemoving.value
  void run(async () => {
    await deleteDirectZlmInstance(instance)
    directRemoving.value = null
  }).then(() => void loadDirectInstances())
}
function directIdle(instance: DirectZlmInstance) {
  return (
    instance.usage.sources === 0 && instance.usage.viewers === 0 && instance.usage.starting === 0
  )
}

function liveZlmLoad(node: PreviewNodeStatus | undefined) {
  if (displayClock.value - snapshotRefreshedAt.value > 20_000) return null
  return node?.zlmLoad ?? null
}

function directZlmLoad(nodeId: string) {
  return liveZlmLoad(snapshot.value?.nodes.find((node) => node.nodeId === nodeId))
}

async function refreshObservation() {
  if (observationController) return
  const active = captureScope()
  const nextController = new globalThis.AbortController()
  observationController = nextController
  try {
    const value = await getPreviewNodes(nextController.signal)
    if (active() && !busy.value && observationController === nextController) {
      snapshot.value = value
      snapshotRefreshedAt.value = Date.now()
      lastObservationAttemptAt = snapshotRefreshedAt.value
    }
  } catch {
    // The last reading expires in the UI if the read-only refresh fails.
  } finally {
    if (observationController === nextController) observationController = undefined
  }
}

function opsZlmStatus(item: OpsZlmInstances['items'][number]): 'linked' | 'occupied' | 'available' {
  const linked = snapshot.value?.nodes.some(
    (node) =>
      node.opsSource?.controllerId === item.controllerId &&
      node.opsSource?.targetId === item.targetId &&
      node.opsSource?.instanceId === item.instanceId,
  )
  if (linked) return 'linked'
  const occupied = (directInstances.value?.items ?? []).some(
    (node) => `http://${node.apiHost}:${node.apiPort}` === item.apiUrl,
  )
  return occupied ? 'occupied' : 'available'
}

async function loadOpsInstances() {
  if (opsBusy.value) return
  const active = captureScope()
  opsController?.abort()
  const nextController = new globalThis.AbortController()
  opsController = nextController
  opsBusy.value = true
  opsError.value = null
  opsInstances.value = null
  try {
    const value = await getOpsPreviewInstances(nextController.signal)
    if (active() && canManage.value) opsInstances.value = value
  } catch (cause) {
    if (active() && canManage.value) opsError.value = cause
  } finally {
    if (opsController === nextController) opsBusy.value = false
  }
}

async function loadOpsZlmInstances() {
  if (!canManage.value || opsZlmBusy.value) return
  const active = captureScope()
  opsZlmController?.abort()
  const nextController = new globalThis.AbortController()
  opsZlmController = nextController
  opsZlmBusy.value = true
  opsZlmError.value = null
  try {
    const value = await getOpsZlmInstances(nextController.signal)
    if (active() && canManage.value) opsZlmInstances.value = value
  } catch (cause) {
    if (active() && canManage.value) opsZlmError.value = cause
  } finally {
    if (opsZlmController === nextController) opsZlmBusy.value = false
  }
}

function importZlmFromOps(instanceId: string, releaseId: string) {
  void run(() => importOpsZlmInstance(instanceId, releaseId)).then(() => {
    void loadDirectInstances()
    void loadOpsZlmInstances()
  })
}

function importFromOps(instanceId: string, releaseId: string) {
  void run(() => importOpsPreviewInstance(instanceId, releaseId))
}

async function loadLocalZlm() {
  const active = captureScope()
  runtimeController = new globalThis.AbortController()
  try {
    const runtime = await getMediaRuntime(runtimeController.signal)
    if (active())
      localZlm.value =
        runtime.candidates.find((candidate) => candidate.id === runtime.selected.zlmId) ?? null
  } catch {
    // Node status remains available when local software inspection fails.
  }
}
async function run(operation?: () => Promise<unknown>) {
  if (busy.value) return
  observationController?.abort()
  observationController = undefined
  const active = captureScope()
  controller = new globalThis.AbortController()
  busy.value = true
  error.value = null
  try {
    if (operation) await operation()
    const value = await getPreviewNodes(controller.signal)
    if (active()) {
      snapshot.value = value
      snapshotRefreshedAt.value = Date.now()
      lastObservationAttemptAt = snapshotRefreshedAt.value
      if (maintaining.value)
        maintaining.value = value.nodes.find((n) => n.nodeId === maintaining.value?.nodeId) ?? null
      editing.value = null
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) busy.value = false
  }
}
function edit(node: PreviewNodeStatus) {
  editing.value = { ...node, budget: { ...node.budget } }
}
function openConnection(node?: PreviewNodeStatus) {
  connection.value = {
    nodeId: node?.nodeId ?? '',
    name: node?.name ?? '',
    controlUri: node?.controlUri ?? 'https://',
    certificateSha256: node?.certificateSha256 ?? '',
    mediaAddress: node?.mediaAddress ?? '',
    version: node?.version ?? null,
  }
}
function saveConnection() {
  if (!connection.value) return
  const value = { ...connection.value }
  void run(async () => {
    await configurePreviewNode(value)
    connection.value = null
  })
}
function mode(node: PreviewNodeStatus, value: string) {
  void run(() => savePreviewNode({ ...node, mode: value }))
}
function save() {
  if (editing.value) {
    const value = editing.value
    void run(() => savePreviewNode(value))
  }
}
function openMaintenance(node: PreviewNodeStatus) {
  maintaining.value = node
  maintenance.value = null
  void maintain()
}
function synchronizedOps() {
  synchronizing.value = null
  void run()
  void loadDirectInstances()
}
function maintain(action?: 'PREPARE' | 'RELEASE') {
  const node = maintaining.value
  if (!node) return
  const active = captureScope()
  void run(async () => {
    const value = await previewMaintenance(
      node,
      action,
      action === 'PREPARE'
        ? globalThis.crypto.randomUUID()
        : (maintenance.value?.permitId ?? undefined),
    )
    if (active() && maintaining.value?.nodeId === node.nodeId) maintenance.value = value
  })
}
</script>

<template>
  <section class="preview-node-panel" :aria-label="management ? '预览节点管理' : '预览节点总览'">
    <header class="node-title">
      <div class="node-title-label">
        <h2>预览节点</h2>
        <span class="node-count">{{ snapshot?.nodes.length ?? '—' }} 台已登记</span>
      </div>
      <div v-if="snapshot" class="node-actions">
        <ElButton :loading="busy" @click="run(() => previewNodeAction('check'))">检查节点</ElButton>
        <ElButton
          v-if="canManage"
          :disabled="busy"
          @click="run(() => previewNodeAction('reconcile'))"
          >重新核对资源</ElButton
        >
        <button
          v-if="canManage"
          type="button"
          class="guide-trigger primary-trigger"
          popovertarget="preview-package-panel"
        >
          + 接入节点包
        </button>
        <RouterLink v-if="!management" to="/preview/center">前往预览</RouterLink>
      </div>
    </header>
    <RequestError
      :error="error"
      advice="核对节点原因和预算；连接异常时检查证书、凭据与节点日志，资源未确认关闭前保留占用。"
    />
    <div v-if="snapshot" class="node-content">
      <div class="node-list">
        <article
          v-for="node in management
            ? snapshot.nodes.filter((item) => item.kind !== 'ZLM_DIRECT')
            : snapshot.nodes"
          :key="node.nodeId"
          class="node-card"
          :style="{ order: node.nodeId === 'local' ? -1 : 0 }"
        >
          <div class="node-heading">
            <div>
              <h3>{{ node.name }}</h3>
              <small>{{ node.nodeId }} · 优先级 {{ node.priority }}</small>
            </div>
            <ElTag round :type="node.mode === 'REMOTE' ? 'primary' : 'info'">{{
              labelOf(names, node.mode)
            }}</ElTag>
          </div>
          <div class="node-status-bar" :class="{ ready: node.state === 'READY' }">
            <p class="node-state">
              <span class="state-dot" aria-hidden="true"></span> 实际状态：{{
                labelOf(names, node.state)
              }}<span v-if="node.observedAt"> · {{ formatDateTime(node.observedAt) }}</span>
            </p>
            <dl class="node-identity">
              <div>
                <dt>
                  {{
                    node.kind === 'ZLM_DIRECT'
                      ? 'ZLM API 地址'
                      : node.nodeId === 'local'
                        ? 'ZLM 地址'
                        : '节点控制地址'
                  }}
                </dt>
                <dd>
                  {{
                    node.kind === 'ZLM_DIRECT'
                      ? node.zlmApiAddress || '未登记'
                      : node.nodeId === 'local'
                        ? localZlm?.apiBase || '本机'
                        : node.controlUri || '未登记'
                  }}
                </dd>
              </div>
              <div v-if="node.nodeId !== 'local'">
                <dt>媒体地址</dt>
                <dd>{{ node.mediaAddress || '未登记' }}</dd>
              </div>
              <div v-if="node.opsSource">
                <dt>Ops 已同步版本</dt>
                <dd>
                  <code>{{ node.opsSource.releaseId }}</code> ·
                  {{ formatDateTime(node.opsSource.syncedAt) }}
                </dd>
              </div>
              <div v-if="node.kind !== 'ZLM_DIRECT'">
                <dt>{{ node.nodeId === 'local' ? 'ZLM 版本' : '节点程序版本' }}</dt>
                <dd>
                  {{
                    node.nodeId === 'local'
                      ? localZlm?.softwareVersion || '未检测'
                      : node.observation?.version || '未上报'
                  }}
                </dd>
              </div>
              <div v-if="node.kind === 'MANAGED' || (node.nodeId !== 'local' && !node.kind)">
                <dt>ZLM 版本</dt>
                <dd>节点未上报</dd>
              </div>
              <div v-if="node.nodeId === 'local' && localZlm?.ports.length">
                <dt>监听端口</dt>
                <dd class="node-ports">
                  <span v-for="port in localZlm.ports" :key="`${port.protocol}-${port.port}`">
                    {{ port.protocol }} {{ port.port }}
                  </span>
                </dd>
              </div>
            </dl>
          </div>
          <div class="node-operational">
            <dl class="node-metrics">
              <div>
                <dt>源流</dt>
                <dd>{{ node.usage.sources }} / {{ node.budget.maxSources }}</dd>
                <dd class="metric-track">
                  <progress
                    :value="node.usage.sources"
                    :max="Math.max(1, node.budget.maxSources)"
                    aria-label="源流预算使用率"
                  ></progress>
                </dd>
              </div>
              <div>
                <dt>观看</dt>
                <dd>{{ node.usage.viewers }} / {{ node.budget.maxViewers }}</dd>
                <dd class="metric-track">
                  <progress
                    :value="node.usage.viewers"
                    :max="Math.max(1, node.budget.maxViewers)"
                    aria-label="观看预算使用率"
                  ></progress>
                </dd>
              </div>
              <div>
                <dt>转码</dt>
                <dd>{{ node.usage.transcodes }} / {{ node.budget.maxTranscodes }}</dd>
                <dd class="metric-track">
                  <progress
                    :value="node.usage.transcodes"
                    :max="Math.max(1, node.budget.maxTranscodes)"
                    aria-label="转码预算使用率"
                  ></progress>
                </dd>
              </div>
              <div>
                <dt>启动中</dt>
                <dd>{{ node.usage.starting }} / {{ node.budget.maxStarting }}</dd>
                <dd class="metric-track">
                  <progress
                    :value="node.usage.starting"
                    :max="Math.max(1, node.budget.maxStarting)"
                    aria-label="启动中预算使用率"
                  ></progress>
                </dd>
              </div>
            </dl>
            <div class="node-bandwidth">
              <div>
                <div class="bandwidth-label">
                  <span>预留入站</span
                  ><strong
                    >{{ (node.usage.ingressKbps / 1000).toFixed(1) }} /
                    {{ node.budget.ingressKbps / 1000 }} Mbps</strong
                  >
                </div>
                <progress
                  :value="node.usage.ingressKbps"
                  :max="Math.max(1, node.budget.ingressKbps)"
                  aria-label="预留入站带宽使用率"
                ></progress>
              </div>
              <div>
                <div class="bandwidth-label">
                  <span>预留出口</span
                  ><strong
                    >{{ (node.usage.egressKbps / 1000).toFixed(1) }} /
                    {{ node.budget.egressKbps / 1000 }} Mbps</strong
                  >
                </div>
                <progress
                  :value="node.usage.egressKbps"
                  :max="Math.max(1, node.budget.egressKbps)"
                  aria-label="预留出口带宽使用率"
                ></progress>
              </div>
            </div>
            <ZlmNetworkLoad v-if="node.kind === 'ZLM_DIRECT'" :load="liveZlmLoad(node)" />
            <p v-if="node.reasonCode" class="node-reason">
              {{ previewReason(node.reasonCode) }} <code>{{ node.reasonCode }}</code>
            </p>
            <div class="node-actions node-footer">
              <ElButton text @click="diagnostic = node">诊断 JSON</ElButton>
              <template v-if="canManage">
                <ElButton text :disabled="busy" @click="edit(node)">预算与优先级</ElButton>
                <ElButton
                  v-if="node.opsSource"
                  :disabled="busy || node.mode !== 'DRAINING'"
                  @click="synchronizing = node"
                  >同步 Ops 配置</ElButton
                >
                <ElButton
                  v-if="node.kind !== 'ZLM_DIRECT' && node.nodeId !== 'local'"
                  :disabled="busy"
                  @click="openMaintenance(node)"
                  >维护许可</ElButton
                >
                <ElButton
                  v-if="node.mode !== 'REMOTE'"
                  class="node-mode-action"
                  :disabled="busy"
                  @click="mode(node, 'REMOTE')"
                  >参与调度</ElButton
                >
                <ElButton
                  v-else
                  text
                  class="node-mode-action drain-action"
                  :disabled="busy"
                  @click="mode(node, 'DRAINING')"
                  >停止接新预览</ElButton
                >
              </template>
            </div>
            <details v-if="canManage && node.nodeId !== 'local'" class="node-advanced">
              <summary>高级连接配置</summary>
              <ElButton
                v-if="node.kind !== 'ZLM_DIRECT' && !node.opsSource"
                :disabled="busy || node.mode === 'REMOTE'"
                @click="openConnection(node)"
                >手工修改连接</ElButton
              >
              <ElButton
                v-if="!node.opsSource"
                :disabled="busy || node.mode !== 'DRAINING'"
                @click="synchronizing = node"
                >关联现有 Ops 实例</ElButton
              >
              <p v-else class="connection-empty">连接由 Ops 来源维护；修改配置请使用同步入口。</p>
            </details>
          </div>
        </article>
      </div>
      <section v-if="canManage" class="node-guide direct-guide" aria-label="直连 ZLM">
        <div class="connection-heading">
          <div>
            <h3>直连 ZLM</h3>
            <p class="connection-hint">手工接入与 Ops 引入互不依赖；接入后需手动参与调度</p>
          </div>
          <div class="node-actions">
            <button type="button" class="guide-trigger" popovertarget="preview-ops-zlm-panel">
              从 Ops 引入 ZLM
            </button>
            <ElButton
              :disabled="busy || directBusy || !directInstances?.registrationAvailable"
              type="primary"
              @click="openDirectForm()"
              >+ 接入 ZLM</ElButton
            >
            <ElButton text :loading="directBusy" @click="loadDirectInstances()">刷新</ElButton>
          </div>
        </div>
        <RequestError :error="directError" :advice="directAdvice" />
        <p v-if="directBusy && !directInstances" class="connection-empty">正在读取 ZLM 实例…</p>
        <p
          v-else-if="directInstances && !directInstances.registrationAvailable"
          class="connection-empty"
        >
          平台密钥存储不可用，请检查服务账号权限。
        </p>
        <div v-if="directInstances && !directInstances.items.length" class="direct-empty">
          <svg
            aria-hidden="true"
            viewBox="0 0 40 40"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <rect x="8" y="7" width="24" height="11" rx="3" />
            <rect x="8" y="22" width="24" height="11" rx="3" />
            <path d="M13 12.5h2m-2 15h2m5-15h7m-7 15h7" />
          </svg>
          <strong>尚未接入 ZLM 实例</strong><span>手工接入或从 Ops 引入后，即可在此管理</span>
        </div>
        <div v-if="directInstances?.items.length" class="connection-list">
          <article
            v-for="item in directInstances.items"
            :key="item.nodeId"
            class="connection-row direct-row"
          >
            <div class="connection-detail">
              <div class="connection-name">
                <strong>{{ item.name }}</strong>
                <ElTag effect="plain">{{ labelOf(names, item.mode) }}</ElTag>
              </div>
              <div class="connection-meta">
                <span>服务地址：{{ item.apiHost }}</span>
                <span>ZLM HTTP：{{ item.apiHost }}:{{ item.apiPort }}</span>
                <span>媒体地址：{{ item.mediaAddress }}</span>
                <span>安装位置：{{ item.installPath || '未登记' }}</span>
                <span>ZLM 版本：{{ item.zlmVersion || '未检测' }}</span>
                <span>实际状态 {{ labelOf(names, item.state) }}</span>
              </div>
              <div class="connection-meta">
                <span
                  >RTC 端口：{{
                    item.portsObservedAt && item.rtcPort != null ? item.rtcPort : '未核对'
                  }}</span
                >
                <span
                  >RTSP 端口：{{
                    item.portsObservedAt && item.rtspPort != null ? item.rtspPort : '未核对'
                  }}</span
                >
                <span
                  >端口最近核对：{{
                    item.portsObservedAt ? formatDateTime(item.portsObservedAt) : '未核对'
                  }}</span
                >
              </div>
              <div class="connection-meta">
                <span>源流 {{ item.usage.sources }} / {{ item.budget.maxSources }}</span>
                <span>观看 {{ item.usage.viewers }} / {{ item.budget.maxViewers }}</span>
                <span>转码 {{ item.usage.transcodes }} / {{ item.budget.maxTranscodes }}</span>
              </div>
              <ZlmNetworkLoad :load="directZlmLoad(item.nodeId)" />
              <p v-if="item.reasonCode" class="node-reason">
                {{ previewReason(item.reasonCode) }} <code>{{ item.reasonCode }}</code>
              </p>
            </div>
            <div class="node-actions direct-actions">
              <ElButton
                v-if="item.mode !== 'REMOTE'"
                :disabled="busy || !directIdle(item)"
                @click="updateDirect(item, { mode: 'REMOTE' })"
                >参与调度</ElButton
              >
              <ElButton v-else :disabled="busy" @click="updateDirect(item, { mode: 'DRAINING' })"
                >排空</ElButton
              >
              <ElButton
                :disabled="busy || item.mode === 'REMOTE'"
                @click="
                  directEditing = {
                    instance: item,
                    priority: item.priority,
                    budget: { ...item.budget },
                    installPath: item.installPath || '',
                  }
                "
                >预算</ElButton
              >
              <ElButton
                :disabled="
                  busy ||
                  !directInstances.registrationAvailable ||
                  !!snapshot?.nodes.find((node) => node.nodeId === item.nodeId)?.opsSource ||
                  item.mode === 'REMOTE' ||
                  !directIdle(item)
                "
                @click="directCredential = { instance: item, secret: '' }"
                >密钥</ElButton
              >
              <ElButton
                :disabled="busy || item.mode === 'REMOTE' || !directIdle(item)"
                @click="directRemoving = item"
                >移除</ElButton
              >
            </div>
          </article>
        </div>
      </section>
      <section
        v-if="canManage"
        id="preview-package-panel"
        popover="auto"
        class="guide-popover node-package-guide"
        aria-label="接入预览节点"
      >
        <header class="popover-heading">
          <div>
            <h3>接入节点包</h3>
            <p>从 Ops 引入或手工登记远程节点</p>
          </div>
          <button
            type="button"
            class="guide-trigger"
            popovertarget="preview-package-panel"
            popovertargetaction="hide"
          >
            关闭
          </button>
        </header>
        <div class="connection-heading">
          <h3>Ops 已安装</h3>
          <div class="node-actions">
            <ElButton :loading="opsBusy" @click="loadOpsInstances()">刷新实例</ElButton>
          </div>
        </div>
        <RequestError
          :error="opsError"
          advice="检查 Platform 与 Ops 的连接及 Ops 中预览实例的部署状态。"
        />
        <p v-if="opsBusy && !opsInstances" class="connection-empty">正在读取 Ops 实例…</p>
        <p v-else-if="opsInstances?.reasonCode" class="connection-empty">
          {{ previewReason(opsInstances.reasonCode) }}
          <code>{{ opsInstances.reasonCode }}</code>
        </p>
        <p v-else-if="opsInstances && !opsInstances.available" class="connection-empty">
          Ops 目录未配置（可选）。
        </p>
        <p v-else-if="opsInstances && !opsInstances.items.length" class="connection-empty">
          Ops 暂无可引入的预览实例。
        </p>
        <div v-if="opsInstances?.items.length" class="connection-list">
          <div v-for="item in opsInstances.items" :key="item.instanceId" class="connection-row">
            <div class="connection-detail">
              <div class="connection-name">
                <strong>{{ item.name }}</strong>
                <ElTag
                  :type="
                    snapshot.nodes.some((node) => node.nodeId === item.nodeId) ? 'info' : 'warning'
                  "
                  effect="plain"
                  >{{
                    snapshot.nodes.some((node) => node.nodeId === item.nodeId) ? '已登记' : '可引入'
                  }}</ElTag
                >
              </div>
              <div class="connection-meta">
                <span>服务器：{{ item.targetName }}</span>
                <span v-if="item.targetAddress">地址：{{ item.targetAddress }}</span>
              </div>
              <div class="connection-meta">
                <span>安装位置：{{ item.installRoot }}</span>
                <span>发布版本：{{ item.releaseId }}</span>
                <span
                  >源流 {{ item.maxSources }} · 观看 {{ item.maxViewers }} · 转码
                  {{ item.maxTranscodes }}</span
                >
              </div>
            </div>
            <ElButton
              :disabled="
                busy ||
                opsBusy ||
                !item.controllerId ||
                !item.targetId ||
                snapshot.nodes.some((node) => node.nodeId === item.nodeId)
              "
              @click="importFromOps(item.instanceId, item.releaseId)"
              >{{
                snapshot.nodes.some((node) => node.nodeId === item.nodeId)
                  ? '请从节点卡同步'
                  : '引入节点'
              }}</ElButton
            >
          </div>
        </div>
        <details v-if="configuredConnections.length" class="connection-extra">
          <summary>登记文件中的节点（{{ configuredConnections.length }}）</summary>
          <div class="connection-list">
            <div v-for="item in configuredConnections" :key="item.nodeId" class="connection-row">
              <div class="connection-name">
                <strong>{{ item.name }}</strong>
                <code>{{ item.nodeId }}</code>
                <ElTag :type="item.registered ? 'info' : 'warning'" effect="plain">{{
                  item.registered ? '已登记' : '待导入'
                }}</ElTag>
              </div>
              <ElButton
                :disabled="
                  busy || item.registered?.mode === 'REMOTE' || !!item.registered?.opsSource
                "
                @click="run(() => registerPreviewNode(item.nodeId))"
                >{{ item.registered ? '重新导入' : '导入节点' }}</ElButton
              >
            </div>
          </div>
        </details>
        <details class="connection-extra">
          <summary>手工登记节点包</summary>
          <p v-if="!snapshot.directRegistrationAvailable" class="connection-empty">
            节点包需要预置机器令牌；可先从 Ops 引入，或由管理员配置节点包凭据目录。
          </p>
          <ElButton
            :disabled="busy || !snapshot.directRegistrationAvailable"
            @click="openConnection()"
            >手工登记</ElButton
          >
        </details>
      </section>
      <section
        id="preview-ops-zlm-panel"
        popover="auto"
        class="guide-popover ops-zlm-catalog"
        aria-label="从 Ops 引入 ZLM"
      >
        <header class="popover-heading">
          <div>
            <h3>从 Ops 引入 ZLM</h3>
            <p>选择已发布实例接入预览调度</p>
          </div>
          <button
            type="button"
            class="guide-trigger"
            popovertarget="preview-ops-zlm-panel"
            popovertargetaction="hide"
          >
            关闭
          </button>
        </header>
        <div class="connection-heading">
          <h4>已发布实例</h4>
          <ElButton :loading="opsZlmBusy" @click="loadOpsZlmInstances()">刷新</ElButton>
        </div>
        <RequestError
          :error="opsZlmError"
          advice="检查 Platform 与 Ops 的连接，以及 Ops 中 ZLM 实例的发布状态。"
        />
        <p v-if="opsZlmBusy && !opsZlmInstances" class="connection-empty">
          正在读取 Ops 的 ZLM 实例…
        </p>
        <p v-else-if="opsZlmInstances?.reasonCode" class="connection-empty">
          {{ previewReason(opsZlmInstances.reasonCode) }}
          <code>{{ opsZlmInstances.reasonCode }}</code>
        </p>
        <p v-else-if="opsZlmInstances && !opsZlmInstances.available" class="connection-empty">
          Ops 目录未配置；可继续手工接入。
        </p>
        <p v-else-if="opsZlmInstances && !opsZlmInstances.items.length" class="connection-empty">
          Ops 暂无已发布的 ZLM 实例。
        </p>
        <div v-if="opsZlmInstances?.items.length" class="connection-list">
          <article
            v-for="item in opsZlmInstances.items"
            :key="item.instanceId"
            class="connection-row"
          >
            <div class="connection-detail">
              <div class="connection-name">
                <strong>{{ item.name }}</strong>
                <ElTag
                  :type="opsZlmStatus(item) === 'available' ? 'warning' : 'info'"
                  effect="plain"
                >
                  {{
                    opsZlmStatus(item) === 'linked'
                      ? '已引入平台'
                      : opsZlmStatus(item) === 'occupied'
                        ? '地址已登记'
                        : '未引入平台'
                  }}
                </ElTag>
              </div>
              <div class="connection-meta">
                <span>服务器：{{ item.targetName }}</span>
                <span v-if="item.targetAddress">服务器地址：{{ item.targetAddress }}</span>
                <span>媒体地址：{{ item.mediaAddress }}</span>
              </div>
              <div class="connection-meta">
                <span>ZLM HTTP API：{{ item.apiUrl }}</span>
                <span>HTTP 端口：{{ item.apiPort }}</span>
                <span>RTC 端口：{{ item.rtcPort }}</span>
                <span>Ops 目标根目录：{{ item.installRoot }}</span>
                <span v-if="item.installPath">ZLM 发布目录：{{ item.installPath }}</span>
                <span>发布版本：{{ item.releaseId }}</span>
              </div>
            </div>
            <ElButton
              :disabled="
                busy ||
                opsZlmBusy ||
                !item.controllerId ||
                !item.targetId ||
                !directInstances?.registrationAvailable ||
                opsZlmStatus(item) !== 'available'
              "
              @click="importZlmFromOps(item.instanceId, item.releaseId)"
              >引入平台</ElButton
            >
          </article>
        </div>
      </section>
    </div>
    <PreviewOpsSync
      v-if="synchronizing && canManage"
      :key="synchronizing.nodeId"
      :node="synchronizing"
      @close="synchronizing = null"
      @applied="synchronizedOps"
    />
    <ElDialog
      :model-value="!!directForm"
      class="management-dialog"
      title="接入 ZLM"
      width="min(620px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="directForm = null"
    >
      <form v-if="directForm" class="budget-form" @submit.prevent="saveDirectForm">
        <p class="connection-form-intro">填写内网 HTTP API；安装位置仅用于登记。</p>
        <label>实例名称<input v-model.trim="directForm.name" maxlength="80" required /></label>
        <label
          >内网 IP<input v-model.trim="directForm.apiHost" placeholder="10.0.0.10" required
        /></label>
        <label
          >HTTP 端口<input
            v-model.number="directForm.apiPort"
            type="number"
            min="1"
            max="65535"
            required
        /></label>
        <label
          >安装位置（选填）<input
            v-model.trim="directForm.installPath"
            maxlength="512"
            placeholder="例如 /opt/zlm"
          />
        </label>
        <label
          >ZLM secret<input
            v-model="directForm.secret"
            type="password"
            autocomplete="new-password"
            required
        /></label>
        <details class="form-advanced">
          <summary>高级设置：媒体地址与预算</summary>
          <div class="budget-form form-advanced-fields">
            <label
              >浏览器可达媒体地址<input
                v-model.trim="directForm.mediaAddress"
                :placeholder="directForm.apiHost || '默认同内网 IP'"
            /></label>
            <label
              >优先级<input
                v-model.number="directForm.priority"
                type="number"
                min="0"
                max="1000"
                required
            /></label>
            <label v-for="field in directFields" :key="field.key">
              {{ field.label
              }}<input
                v-model.number="directForm.budget[field.key]"
                type="number"
                :min="field.min"
                :max="field.max"
                required
              />
            </label>
          </div>
        </details>
        <ElButton native-type="submit" type="primary" :loading="busy">检查并接入</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directEditing"
      class="management-dialog"
      title="ZLM 配置与预算"
      width="min(620px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="directEditing = null"
    >
      <form
        v-if="directEditing"
        class="budget-form"
        @submit.prevent="
          updateDirect(directEditing.instance, {
            priority: directEditing.priority,
            budget: directEditing.budget,
            installPath: directEditing.installPath.trim(),
          })
        "
      >
        <label
          >安装位置（选填）<input
            v-model.trim="directEditing.installPath"
            maxlength="512"
            placeholder="例如 /opt/zlm"
          />
        </label>
        <label
          >优先级<input
            v-model.number="directEditing.priority"
            type="number"
            min="0"
            max="1000"
            required
        /></label>
        <label v-for="field in directFields" :key="field.key">
          {{ field.label
          }}<input
            v-model.number="directEditing.budget[field.key]"
            type="number"
            :min="field.min"
            :max="field.max"
            required
          />
        </label>
        <p>直连 ZLM 首期不支持转码，转码上限固定为 0。降低预算只影响新申请。</p>
        <ElButton native-type="submit" type="primary" :loading="busy">保存</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directCredential"
      class="management-dialog"
      title="更新 ZLM secret"
      width="min(520px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="directCredential = null"
    >
      <form v-if="directCredential" class="budget-form" @submit.prevent="saveDirectCredential">
        <p class="connection-form-intro">
          {{ directCredential.instance.name }}：排空并确认资源归零后才能更新。
        </p>
        <label class="field-full"
          >新 secret<input
            v-model="directCredential.secret"
            type="password"
            autocomplete="new-password"
            required
        /></label>
        <ElButton native-type="submit" type="primary" :loading="busy">更新密钥</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directRemoving"
      class="management-dialog"
      title="移除 ZLM 实例"
      width="min(480px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="directRemoving = null"
    >
      <div v-if="directRemoving" class="remove-confirm">
        <p class="maintenance-body">
          确认移除 <strong>{{ directRemoving.name }}</strong
          >？仅排空且无资源归属时允许移除。
        </p>
        <div class="node-actions">
          <ElButton type="danger" :loading="busy" @click="removeDirect()">确认移除</ElButton>
        </div>
      </div>
    </ElDialog>
    <ElDialog
      :model-value="!!connection"
      class="management-dialog"
      :title="connection?.version == null ? '新增远程预览节点' : '修改节点连接'"
      width="min(680px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="connection = null"
    >
      <form v-if="connection" class="budget-form" @submit.prevent="saveConnection">
        <p class="connection-form-intro">
          节点控制地址不是 ZLM HTTP API；可用内网自签名 HTTPS 控制口，无需公网证书。
        </p>
        <label
          >节点 ID<input
            v-model.trim="connection.nodeId"
            :readonly="connection.version != null"
            maxlength="64"
            required
        /></label>
        <label>名称<input v-model.trim="connection.name" maxlength="80" required /></label>
        <label
          >节点控制地址（高级）<input
            v-model.trim="connection.controlUri"
            type="url"
            placeholder="https://10.0.0.10:9443"
            required
        /></label>
        <label
          >浏览器可达媒体地址<input
            v-model.trim="connection.mediaAddress"
            placeholder="10.0.0.10"
            required
        /></label>
        <label class="field-full"
          >证书 SHA-256 指纹<input
            v-model.trim="connection.certificateSha256"
            minlength="64"
            maxlength="64"
            required
        /></label>
        <p>凭据文件须预先存放在 Platform 受保护目录，文件名为“节点 ID.token”。</p>
        <ElButton native-type="submit" type="primary" :loading="busy">检查并登记</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!maintaining"
      class="management-dialog"
      title="预览节点维护"
      width="min(680px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="maintaining = null"
    >
      <div v-if="maintaining" class="maintenance-body">
        <p class="maintenance-target">
          <strong>{{ maintaining.name }}</strong> · {{ labelOf(names, maintaining.mode) }}
        </p>
        <ol>
          <li>停止接新预览，等待已有观看关闭及资源释放。</li>
          <li>获取维护许可，在 Ops 对应预览实例的升级或回退计划中填写许可编号。</li>
          <li>Ops 作业完成后检查节点；配置发生变化时同步 Ops 配置，解除维护，再明确参与调度。</li>
        </ol>
        <RequestError
          :error="error"
          advice="未决 Ops 作业须先核对或恢复。许可过期不会自动解除维护。"
        />
        <p v-if="maintenance" class="maintenance-state">
          维护状态：<strong>{{ labelOf(maintenanceNames, maintenance.state) }}</strong>
        </p>
        <template v-if="maintenance?.permitId">
          <label
            >维护许可编号<input :value="maintenance.permitId" readonly aria-label="维护许可编号"
          /></label>
          <p>领取期限：{{ formatDateTime(new Date(maintenance.expiresAt).toISOString()) }}</p>
          <p v-if="maintenance.jobId">
            Ops 作业：<code>{{ maintenance.jobId }}</code>
          </p>
        </template>
        <div class="node-actions maintenance-actions">
          <ElButton :disabled="busy" @click="maintain()">刷新维护状态</ElButton>
          <ElButton
            type="primary"
            plain
            :disabled="busy || maintaining.mode !== 'DRAINING' || maintenance?.state === 'CLAIMED'"
            @click="maintain('PREPARE')"
            >获取维护许可</ElButton
          >
          <ElButton
            type="primary"
            :disabled="
              busy ||
              maintaining.mode !== 'DRAINING' ||
              !maintenance?.permitId ||
              maintenance.state === 'CLAIMED'
            "
            @click="maintain('RELEASE')"
            >解除维护</ElButton
          >
        </div>
      </div>
    </ElDialog>
    <ElDialog
      :model-value="!!diagnostic"
      class="management-dialog"
      title="预览节点诊断"
      width="min(740px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="diagnostic = null"
    >
      <pre class="node-json">{{ JSON.stringify(diagnostic, null, 2) }}</pre>
    </ElDialog>
    <ElDialog
      :model-value="!!editing"
      class="management-dialog"
      title="节点预算与优先级"
      width="min(620px, calc(100vw - 32px))"
      align-center
      append-to-body
      destroy-on-close
      @close="editing = null"
    >
      <form v-if="editing" class="budget-form" @submit.prevent="save">
        <label class="field-full"
          >优先级<input
            v-model.number="editing.priority"
            type="number"
            min="0"
            max="10000"
            required
        /></label>
        <label v-for="field in fields" :key="field.key"
          >{{ field.label
          }}<input
            v-model.number="editing.budget[field.key]"
            type="number"
            :min="field.min"
            :max="field.max"
            required
        /></label>
        <p>降低预算只影响新申请。修改每路码率上界须先排空；远程预算不得超过节点已应用配置。</p>
        <ElButton native-type="submit" type="primary" :loading="busy">保存</ElButton>
      </form>
    </ElDialog>
  </section>
</template>

<style scoped>
.preview-node-panel {
  display: grid;
  gap: 16px;
  min-width: 0;
  background: transparent;
}
.node-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 14px;
  padding: 16px 22px;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
}
.node-title-label {
  display: flex;
  align-items: center;
  gap: 12px;
}
.node-title h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  color: #0f172a;
  letter-spacing: -0.01em;
}
.node-count {
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #f8fafc;
  color: #475569;
  padding: 3px 10px;
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.node-content {
  display: grid;
  gap: 16px;
  min-width: 0;
}
.node-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 440px), 1fr));
  gap: 16px;
}
.node-card {
  container-name: node-card;
  container-type: inline-size;
  min-width: 0;
  padding: 20px 22px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #ffffff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}
.node-card:only-child {
  grid-column: 1 / -1;
}
.node-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 6px 18px -6px rgba(15, 23, 42, 0.08);
}
.node-heading,
.node-heading > div {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.node-heading {
  justify-content: space-between;
}
h3 {
  margin: 0;
  font-size: 16px;
  color: #0f172a;
  font-weight: 700;
}
.node-heading small {
  font:
    500 12px ui-monospace,
    Consolas,
    monospace;
  color: #475569;
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  padding: 2px 8px;
  border-radius: 6px;
  overflow-wrap: anywhere;
}
.node-status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin: 14px 0;
  padding: 10px 14px;
  border: 1px solid #fed7aa;
  background: #fff7ed;
  border-radius: 8px;
  color: #9a3412;
  font-size: 12.5px;
  line-height: 1.6;
}
.node-status-bar.ready {
  border-color: #bbf7d0;
  background: #f0fdf4;
  color: #166534;
}
.node-state {
  margin: 0;
  font-weight: 600;
}
.state-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  margin: 0 6px 1px 0;
  background: #ea580c;
}
.ready .state-dot {
  background: #16a34a;
}
.node-identity {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin: 0;
  font-size: 12.5px;
}
.node-identity > div {
  display: flex;
  gap: 5px;
  min-width: 0;
}
.node-identity dt {
  opacity: 0.8;
}
.node-identity dt::after {
  content: ':';
}
.node-identity dd {
  font-weight: 500;
  overflow-wrap: anywhere;
}
dt,
dd {
  margin: 0;
}
.node-ports {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.node-metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin: 0 0 14px;
}
.node-metrics > div {
  min-width: 0;
  padding: 12px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}
.node-metrics dt {
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
  margin-bottom: 6px;
}
.node-metrics dd {
  color: #0f172a;
  font:
    700 16px ui-monospace,
    Consolas,
    monospace;
}
.node-metrics .metric-track {
  margin-top: 10px;
}
progress {
  display: block;
  width: 100%;
  height: 5px;
  border: 0;
  border-radius: 999px;
  overflow: hidden;
  appearance: none;
  background: #e2e8f0;
  color: #2563eb;
}
progress::-webkit-progress-bar {
  background: #e2e8f0;
  border-radius: 999px;
}
progress::-webkit-progress-value {
  background: #2563eb;
  border-radius: 999px;
}
progress::-moz-progress-bar {
  background: #2563eb;
  border-radius: 999px;
}
.node-bandwidth {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 14px;
}
.node-bandwidth > div {
  padding: 10px 14px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}
.bandwidth-label {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
  font-size: 12px;
  font-weight: 500;
  color: #64748b;
}
.bandwidth-label strong {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: #0f172a;
}
.node-bandwidth progress {
  height: 5px;
}
.node-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.node-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.node-actions a {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-color-primary);
  text-decoration: none;
}
.node-actions a:hover {
  text-decoration: underline;
}
.node-footer {
  border-top: 1px solid #f1f5f9;
  padding-top: 12px;
  margin-top: 4px;
}
.node-footer :deep(.el-button.node-mode-action) {
  margin-left: auto;
}
.drain-action {
  color: #b45309;
}
.drain-action:hover {
  color: #dc2626;
  background: #fff1f2;
}
.direct-guide {
  padding: 20px 22px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #ffffff;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
}
.connection-heading,
.connection-name {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}
.connection-heading {
  justify-content: space-between;
}
.connection-heading h4 {
  margin: 0;
  font-size: 14px;
  color: #0f172a;
}
.connection-hint {
  color: #64748b;
  font-size: 12.5px;
  margin: 4px 0 0;
  line-height: 1.5;
}
.connection-empty {
  color: #64748b;
  font-size: 13px;
  line-height: 1.6;
}
.direct-empty {
  min-height: 132px;
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px dashed #cbd5e1;
  border-radius: 10px;
  background: #f8fafc;
  color: #64748b;
  font-size: 12.5px;
}
.direct-empty svg {
  width: 32px;
  height: 32px;
  color: #94a3b8;
}
.direct-empty strong {
  font-size: 14px;
  font-weight: 600;
  color: #334155;
}
.guide-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 32px;
  padding: 0 14px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  background: #ffffff;
  color: #334155;
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    border-color 120ms ease,
    background-color 120ms ease,
    color 120ms ease;
}
.guide-trigger:hover {
  border-color: #93c5fd;
  color: #2563eb;
  background: #eff6ff;
}
.guide-trigger:focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 2px;
}
.primary-trigger {
  color: #ffffff;
  background: var(--el-color-primary);
  border-color: var(--el-color-primary);
}
.primary-trigger:hover {
  color: #ffffff;
  background: var(--el-color-primary-light-3);
  border-color: var(--el-color-primary-light-3);
}
.guide-popover {
  box-sizing: border-box;
  width: min(860px, calc(100vw - 32px));
  max-height: 82vh;
  overflow: auto;
  padding: 22px 24px;
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  background: #ffffff;
  color: #334155;
  box-shadow:
    0 20px 48px -12px rgba(15, 23, 42, 0.22),
    0 4px 12px -2px rgba(15, 23, 42, 0.08);
}
.guide-popover::backdrop {
  background: rgba(15, 23, 42, 0.35);
  backdrop-filter: blur(2px);
}
.popover-heading {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  padding-bottom: 14px;
  margin-bottom: 16px;
  border-bottom: 1px solid #e2e8f0;
}
.popover-heading p {
  margin: 4px 0 0;
  color: #64748b;
  font-size: 12.5px;
}
.connection-list {
  display: grid;
  gap: 10px;
  margin-top: 14px;
}
.connection-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 16px;
  align-items: center;
  padding: 14px 16px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}
.connection-detail {
  min-width: 0;
}
.connection-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin-top: 8px;
  overflow-wrap: anywhere;
  font-size: 12px;
  line-height: 1.5;
  color: #475569;
}
.connection-meta > span {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  background: #ffffff;
  font-variant-numeric: tabular-nums;
}
.connection-name code {
  overflow-wrap: anywhere;
  font-size: 12px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #e2e8f0;
  color: #334155;
}
.connection-name strong {
  font-size: 14px;
  color: #0f172a;
}
.direct-actions {
  justify-content: flex-end;
  max-width: 320px;
}
.connection-extra,
.node-advanced {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #e2e8f0;
  font-size: 13px;
}
.connection-extra summary,
.node-advanced summary {
  cursor: pointer;
  width: fit-content;
  color: #475569;
  font-weight: 500;
}
.connection-extra > .el-button,
.node-advanced > .el-button {
  margin-top: 10px;
}
.node-reason {
  margin: 10px 0 0;
  padding: 8px 12px;
  border: 1px solid #fde68a;
  border-radius: 8px;
  background: #fffbeb;
  font-size: 12.5px;
  color: #92400e;
  overflow-wrap: anywhere;
}
.node-json {
  margin: 0;
  padding: 14px 16px;
  max-height: 58vh;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #0f172a;
  font:
    12.5px/1.65 ui-monospace,
    Consolas,
    monospace;
}
.budget-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px 16px;
}
.budget-form label.field-full {
  grid-column: 1 / -1;
}
.remove-confirm {
  display: grid;
  gap: 16px;
}
.remove-confirm .node-actions {
  justify-content: flex-end;
}
.maintenance-body {
  margin: 0;
  font-size: 13.5px;
  line-height: 1.7;
  color: #334155;
  overflow-wrap: anywhere;
}
.maintenance-target {
  margin: 0 0 10px;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
.maintenance-state {
  margin: 10px 0;
  padding: 8px 12px;
  border-radius: 8px;
  background: #eff6ff;
  color: #1e40af;
}
.maintenance-actions {
  margin-top: 14px;
  justify-content: flex-end;
}
.maintenance-body ol {
  margin: 0 0 12px;
  padding-left: 20px;
  color: #475569;
}
.maintenance-body label {
  display: grid;
  gap: 6px;
  margin-top: 10px;
  font-size: 12.5px;
  font-weight: 500;
}
.maintenance-body input,
.budget-form input {
  min-width: 0;
  width: 100%;
  height: 36px;
  box-sizing: border-box;
  padding: 0 12px;
  border: 1px solid #cbd5e1;
  border-radius: 8px;
  background: #ffffff;
  color: #0f172a;
  font-size: 13px;
  transition:
    border-color 120ms ease,
    box-shadow 120ms ease;
}
.maintenance-body input:focus,
.budget-form input:focus {
  border-color: #2563eb;
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12);
  outline: none;
}
.budget-form label {
  display: grid;
  gap: 6px;
  font-size: 12.5px;
  font-weight: 500;
  color: #334155;
}
.budget-form p {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: #64748b;
}
.budget-form > .el-button {
  grid-column: 1 / -1;
  justify-self: end;
  min-width: 120px;
}
.budget-form .connection-form-intro {
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
}
.form-advanced {
  grid-column: 1 / -1;
  padding-top: 10px;
  border-top: 1px solid #e2e8f0;
}
.form-advanced summary {
  width: fit-content;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  color: #2563eb;
}
.form-advanced-fields {
  margin-top: 12px;
}

@media (max-width: 800px) {
  .node-title,
  .direct-guide {
    padding: 16px;
  }
  .connection-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .direct-actions {
    justify-content: flex-start;
    max-width: none;
  }
}
@media (max-width: 560px) {
  .node-title-label {
    width: 100%;
  }
  .node-title > .node-actions {
    gap: 8px;
  }
  .node-title :deep(.el-button),
  .guide-trigger {
    font-size: 12px;
    padding-left: 10px;
    padding-right: 10px;
  }
  .node-card {
    padding: 16px;
  }
  .budget-form {
    grid-template-columns: 1fr;
  }
  .guide-popover {
    padding: 18px;
  }
}
@container node-card (max-width: 360px) {
  .node-metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .node-bandwidth {
    grid-template-columns: 1fr;
    gap: 10px;
  }
  .node-footer :deep(.el-button.node-mode-action) {
    margin-left: 0;
  }
}
</style>
