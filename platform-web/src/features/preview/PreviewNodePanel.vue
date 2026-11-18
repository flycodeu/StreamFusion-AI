<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElButton, ElTag } from 'element-plus'
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
  defaultDirectBudget,
  nodeUsageMetrics as usageMetrics,
  previewNodeLabels as names,
} from './nodePresentation'
import ZlmNetworkLoad from './ZlmNetworkLoad.vue'
import PreviewOpsSync from './PreviewOpsSync.vue'
import PreviewNodeCatalogPopovers from './PreviewNodeCatalogPopovers.vue'
import PreviewNodeDialogs, {
  type DirectCredentialDraft,
  type DirectEditingDraft,
} from './PreviewNodeDialogs.vue'
import './preview-node.css'

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
const directEditing = ref<DirectEditingDraft | null>(null)
const directCredential = ref<DirectCredentialDraft | null>(null)
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

let controller: globalThis.AbortController | undefined
let runtimeController: globalThis.AbortController | undefined
let opsController: globalThis.AbortController | undefined
let opsZlmController: globalThis.AbortController | undefined
let directController: globalThis.AbortController | undefined
let observationController: globalThis.AbortController | undefined
let observationTimer: ReturnType<typeof globalThis.setInterval> | undefined

function clearManagementState() {
  opsController?.abort()
  opsZlmController?.abort()
  opsController = undefined
  opsZlmController = undefined
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
  directEditing.value = null
  directCredential.value = null
  directRemoving.value = null
  editing.value = null
  connection.value = null
  maintaining.value = null
  maintenance.value = null
  synchronizing.value = null
}

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
    if (enabled) loadManagementCatalogs()
    else clearManagementState()
  },
  { immediate: true },
)

watch(
  [() => sessionState.me?.user.id, () => sessionState.epoch],
  ([userId], [previousUserId]) => {
    controller?.abort()
    runtimeController?.abort()
    observationController?.abort()
    controller = undefined
    runtimeController = undefined
    observationController = undefined
    busy.value = false
    error.value = null
    snapshot.value = null
    snapshotRefreshedAt.value = 0
    localZlm.value = null
    diagnostic.value = null
    clearManagementState()
    if (!userId || userId === previousUserId) return
    const active = captureScope()
    globalThis.queueMicrotask(() => {
      if (!active()) return
      void run()
      void loadLocalZlm()
      if (canManage.value) loadManagementCatalogs()
    })
  },
  { flush: 'sync' },
)

function loadManagementCatalogs() {
  void loadOpsInstances()
  void loadOpsZlmInstances()
  void loadDirectInstances()
}

async function loadDirectInstances(replacePending = false) {
  if (!canManage.value) return
  if (directBusy.value && !replacePending) return
  const active = captureScope()
  directController?.abort()
  const nextController = new globalThis.AbortController()
  directController = nextController
  directBusy.value = true
  directError.value = null
  directInstances.value = null
  try {
    const value = await getDirectZlmInstances(nextController.signal)
    if (active() && !nextController.signal.aborted && canManage.value) directInstances.value = value
  } catch (cause) {
    if (active() && !nextController.signal.aborted && canManage.value) directError.value = cause
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

function saveDirectForm(submitted: DirectZlmInput) {
  if (busy.value || !directForm.value) return
  const draft = directForm.value
  const input = {
    ...submitted,
    mediaAddress: submitted.mediaAddress || submitted.apiHost,
    installPath: submitted.installPath?.trim() || null,
    budget: { ...submitted.budget },
  }
  void run(
    () => createDirectZlmInstance(input),
    () => {
      if (directForm.value === draft) directForm.value = null
      void loadDirectInstances(true)
    },
  )
}

function updateDirect(
  instance: DirectZlmInstance,
  changes: Partial<Pick<DirectZlmInstance, 'mode' | 'priority' | 'budget' | 'installPath'>>,
) {
  const draft = directEditing.value
  void run(
    () => saveDirectZlmInstance(instance, changes),
    () => {
      if (draft?.instance === instance && directEditing.value === draft) directEditing.value = null
      void loadDirectInstances(true)
    },
  )
}

function saveDirectEditingDraft(submitted: DirectEditingDraft) {
  updateDirect(submitted.instance, {
    priority: submitted.priority,
    budget: submitted.budget,
    installPath: submitted.installPath.trim(),
  })
}

function saveDirectCredential(submitted: DirectCredentialDraft) {
  if (busy.value || !directCredential.value || !submitted.secret) return
  const draft = directCredential.value
  void run(
    () => rotateDirectZlmSecret(submitted.instance, submitted.secret),
    () => {
      if (directCredential.value === draft) directCredential.value = null
      void loadDirectInstances(true)
    },
  )
}

function removeDirect() {
  if (!directRemoving.value) return
  const instance = directRemoving.value
  void run(
    () => deleteDirectZlmInstance(instance),
    () => {
      if (directRemoving.value === instance) directRemoving.value = null
      void loadDirectInstances(true)
    },
  )
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

async function loadOpsInstances() {
  if (!canManage.value || opsBusy.value) return
  const active = captureScope()
  opsController?.abort()
  const nextController = new globalThis.AbortController()
  opsController = nextController
  opsBusy.value = true
  opsError.value = null
  opsInstances.value = null
  try {
    const value = await getOpsPreviewInstances(nextController.signal)
    if (active() && !nextController.signal.aborted && canManage.value) opsInstances.value = value
  } catch (cause) {
    if (active() && !nextController.signal.aborted && canManage.value) opsError.value = cause
  } finally {
    if (opsController === nextController) opsBusy.value = false
  }
}

async function loadOpsZlmInstances(replacePending = false) {
  if (!canManage.value || (opsZlmBusy.value && !replacePending)) return
  const active = captureScope()
  opsZlmController?.abort()
  const nextController = new globalThis.AbortController()
  opsZlmController = nextController
  opsZlmBusy.value = true
  opsZlmError.value = null
  opsZlmInstances.value = null
  try {
    const value = await getOpsZlmInstances(nextController.signal)
    if (active() && !nextController.signal.aborted && canManage.value) opsZlmInstances.value = value
  } catch (cause) {
    if (active() && !nextController.signal.aborted && canManage.value) opsZlmError.value = cause
  } finally {
    if (opsZlmController === nextController) opsZlmBusy.value = false
  }
}

function importZlmFromOps(instanceId: string, releaseId: string) {
  void run(
    () => importOpsZlmInstance(instanceId, releaseId),
    () => {
      void loadDirectInstances(true)
      void loadOpsZlmInstances(true)
    },
  )
}

function importFromOps(instanceId: string, releaseId: string) {
  void run(() => importOpsPreviewInstance(instanceId, releaseId))
}

async function loadLocalZlm() {
  const active = captureScope()
  const nextController = new globalThis.AbortController()
  runtimeController = nextController
  try {
    const runtime = await getMediaRuntime(nextController.signal)
    if (active() && !nextController.signal.aborted)
      localZlm.value =
        runtime.candidates.find((candidate) => candidate.id === runtime.selected.zlmId) ?? null
  } catch {
    // Node status remains available when local software inspection fails.
  }
}

async function run(operation?: () => Promise<unknown>, afterApplied?: () => void) {
  if (busy.value) return
  observationController?.abort()
  observationController = undefined
  const active = captureScope()
  const nextController = new globalThis.AbortController()
  controller = nextController
  busy.value = true
  error.value = null
  try {
    if (operation) await operation()
    if (!active()) return
    afterApplied?.()
    const value = await getPreviewNodes(nextController.signal)
    if (active()) {
      snapshot.value = value
      snapshotRefreshedAt.value = Date.now()
      lastObservationAttemptAt = snapshotRefreshedAt.value
      if (maintaining.value)
        maintaining.value = value.nodes.find((n) => n.nodeId === maintaining.value?.nodeId) ?? null
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (controller === nextController) {
      controller = undefined
      busy.value = false
    }
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

function saveConnection(submitted: PreviewNodeConnectionInput) {
  if (!connection.value) return
  const draft = connection.value
  const value = { ...submitted }
  void run(
    () => configurePreviewNode(value),
    () => {
      if (connection.value === draft) connection.value = null
    },
  )
}

function mode(node: PreviewNodeStatus, value: string) {
  void run(() => savePreviewNode({ ...node, mode: value }))
}

function save(submitted: PreviewNodeStatus) {
  if (editing.value) {
    const marker = editing.value
    const value = { ...submitted, budget: { ...submitted.budget } }
    void run(
      () => savePreviewNode(value),
      () => {
        if (editing.value === marker) editing.value = null
      },
    )
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
      <div class="node-actions">
        <ElButton v-if="!snapshot" :loading="busy" @click="run()">重新加载</ElButton>
        <ElButton v-else :loading="busy" @click="run(() => previewNodeAction('check'))"
          >检查节点</ElButton
        >
        <ElButton
          v-if="snapshot && canManage"
          :disabled="busy"
          @click="run(() => previewNodeAction('reconcile'))"
          >重新核对资源</ElButton
        >
        <button
          v-if="snapshot && canManage"
          type="button"
          class="guide-trigger primary-trigger"
          popovertarget="preview-package-panel"
        >
          + 接入节点包
        </button>
        <RouterLink
          v-if="snapshot && !management && administrator"
          to="/monitor/PreviewServiceNodes"
          >节点配置与接入</RouterLink
        >
        <RouterLink v-if="snapshot && management" to="/monitor/Server">主机与媒体状态</RouterLink>
        <RouterLink v-if="snapshot && !management" to="/preview/center">前往预览</RouterLink>
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
              <div v-for="metric in usageMetrics" :key="metric.usage">
                <dt>{{ metric.label }}</dt>
                <dd>{{ node.usage[metric.usage] }} / {{ node.budget[metric.budget] }}</dd>
                <dd class="metric-track">
                  <progress
                    :value="node.usage[metric.usage]"
                    :max="Math.max(1, node.budget[metric.budget])"
                    :aria-label="`${metric.label}预算使用率`"
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

      <PreviewNodeCatalogPopovers
        v-if="canManage"
        :snapshot="snapshot"
        :ops-instances="opsInstances"
        :ops-busy="opsBusy"
        :ops-error="opsError"
        :ops-zlm-instances="opsZlmInstances"
        :ops-zlm-busy="opsZlmBusy"
        :ops-zlm-error="opsZlmError"
        :direct-instances="directInstances"
        :busy="busy"
        :configured-connections="configuredConnections"
        @refresh-ops-instances="loadOpsInstances()"
        @import-ops-instance="importFromOps"
        @register-node="(nodeId) => run(() => registerPreviewNode(nodeId))"
        @open-connection="openConnection()"
        @refresh-ops-zlm-instances="loadOpsZlmInstances()"
        @import-ops-zlm="importZlmFromOps"
      />
    </div>

    <PreviewOpsSync
      v-if="synchronizing && canManage"
      :key="synchronizing.nodeId"
      :node="synchronizing"
      @close="synchronizing = null"
      @applied="synchronizedOps"
    />

    <PreviewNodeDialogs
      :busy="busy"
      :error="error"
      :direct-form="directForm"
      :direct-editing="directEditing"
      :direct-credential="directCredential"
      :direct-removing="directRemoving"
      :connection="connection"
      :maintaining="maintaining"
      :maintenance="maintenance"
      :diagnostic="diagnostic"
      :editing="editing"
      @close-direct-form="directForm = null"
      @save-direct-form="saveDirectForm"
      @close-direct-editing="directEditing = null"
      @save-direct-editing="saveDirectEditingDraft"
      @close-direct-credential="directCredential = null"
      @save-direct-credential="saveDirectCredential"
      @close-direct-removing="directRemoving = null"
      @confirm-remove-direct="removeDirect"
      @close-connection="connection = null"
      @save-connection="saveConnection"
      @close-maintaining="maintaining = null"
      @maintain="maintain"
      @close-diagnostic="diagnostic = null"
      @close-editing="editing = null"
      @save-editing="save"
    />
  </section>
</template>
