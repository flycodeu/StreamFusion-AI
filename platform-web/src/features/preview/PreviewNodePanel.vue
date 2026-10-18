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
import { previewReason } from './model'
import ZlmNetworkLoad from './ZlmNetworkLoad.vue'

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
    return '请配置 Platform 受保护的预览节点凭据目录。'
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
const maintenance = ref<PreviewMaintenance | null>(null)
const maintenanceNames: Record<string, string> = {
  OPEN: '未封闭',
  SEALED: '已签发，等待 Ops 领取',
  CLAIMED: 'Ops 执行中',
  COMPLETE: 'Ops 已完成，等待解除',
}
const names: Record<string, string> = {
  LOCAL: '不接新预览',
  REMOTE: '参与调度',
  DRAINING: '排空中',
  READY: '就绪',
  MAINTENANCE: '维护封闭中',
  RECONCILING: '资源待核对',
  UNKNOWN: '观测已过期或未检查',
  DEGRADED: '媒体环境异常',
  ZLM_NOT_READY: '本机媒体未配置',
  NODE_HOST_OVERLOADED: '节点持续高负载，暂停新预览',
  NODE_BITRATE_EXCEEDED: '实测码率超出预算，暂停新预览',
}
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
  changes: Partial<Pick<DirectZlmInstance, 'mode' | 'priority' | 'budget'>>,
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

function opsZlmRegistered(apiUrl: string) {
  return (directInstances.value?.items ?? []).some(
    (item) => `http://${item.apiHost}:${item.apiPort}` === apiUrl,
  )
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
      <div>
        <h2>预览节点</h2>
        <span>{{ snapshot?.nodes.length ?? '—' }} 台已登记</span>
      </div>
    </header>
    <RequestError
      :error="error"
      advice="核对节点原因和预算；连接异常时检查证书、凭据与节点日志，资源未确认关闭前保留占用。"
    />
    <div v-if="snapshot" class="node-content">
      <div class="node-actions">
        <ElButton :loading="busy" @click="run(() => previewNodeAction('check'))">检查节点</ElButton>
        <ElButton
          v-if="canManage"
          :disabled="busy"
          @click="run(() => previewNodeAction('reconcile'))"
          >重新核对资源</ElButton
        >
        <RouterLink v-if="!management" to="/preview/center">前往预览</RouterLink>
      </div>
      <section v-if="canManage" class="node-guide direct-guide" aria-label="直连 ZLM">
        <div class="connection-heading">
          <h3>直连 ZLM</h3>
          <div class="node-actions">
            <ElButton
              :disabled="busy || directBusy || !directInstances?.registrationAvailable"
              @click="openDirectForm()"
              >接入 ZLM</ElButton
            >
            <ElButton :loading="directBusy" @click="loadDirectInstances()">刷新</ElButton>
          </div>
        </div>
        <RequestError :error="directError" :advice="directAdvice" />
        <details class="ops-zlm-catalog">
          <summary>
            从 Ops 引入 ZLM<span v-if="opsZlmInstances?.items.length">
              · {{ opsZlmInstances.items.length }} 台可查看</span
            >
          </summary>
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
            尚未连接 Ops 实例目录，可手工接入已安装的 ZLM。
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
                  <ElTag :type="opsZlmRegistered(item.apiUrl) ? 'info' : 'warning'" effect="plain">
                    {{ opsZlmRegistered(item.apiUrl) ? '已接入' : '可引入' }}
                  </ElTag>
                </div>
                <div class="connection-meta">
                  <span>{{ item.targetName }} · {{ item.targetAddress }}</span>
                  <span>API：{{ item.apiUrl }}</span>
                </div>
              </div>
              <ElButton
                :disabled="
                  busy ||
                  opsZlmBusy ||
                  !directInstances?.registrationAvailable ||
                  opsZlmRegistered(item.apiUrl)
                "
                @click="importZlmFromOps(item.instanceId, item.releaseId)"
                >引入平台</ElButton
              >
            </article>
          </div>
        </details>
        <p v-if="directBusy && !directInstances" class="connection-empty">正在读取 ZLM 实例…</p>
        <p
          v-else-if="directInstances && !directInstances.registrationAvailable"
          class="connection-empty"
        >
          请先配置 Platform 受保护的预览节点凭据目录，再接入 ZLM。
        </p>
        <p v-if="directInstances && !directInstances.items.length" class="connection-empty">
          尚未接入 ZLM 实例。
        </p>
        <div v-if="directInstances?.items.length" class="connection-list">
          <article
            v-for="item in directInstances.items"
            :key="item.nodeId"
            class="connection-row direct-row"
          >
            <div class="connection-detail">
              <div class="connection-name">
                <strong>{{ item.name }}</strong>
                <ElTag effect="plain">{{ names[item.mode] || item.mode }}</ElTag>
              </div>
              <div class="connection-meta">
                <span>ZLM HTTP：{{ item.apiHost }}:{{ item.apiPort }}</span>
                <span>媒体地址：{{ item.mediaAddress }}</span>
                <span>ZLM 版本：{{ item.zlmVersion || '未检测' }}</span>
                <span>实际状态 {{ names[item.state] || item.state }}</span>
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
                  }
                "
                >预算</ElButton
              >
              <ElButton
                :disabled="
                  busy ||
                  !directInstances.registrationAvailable ||
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
      <details v-if="canManage" class="node-guide node-package-guide" aria-label="接入预览节点">
        <summary>接入节点包</summary>
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
          尚未连接 Ops 实例目录。
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
                snapshot.nodes.some((node) => node.nodeId === item.nodeId && node.mode === 'REMOTE')
              "
              @click="importFromOps(item.instanceId, item.releaseId)"
              >{{
                snapshot.nodes.some((node) => node.nodeId === item.nodeId) ? '更新登记' : '引入节点'
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
                :disabled="busy || item.registered?.mode === 'REMOTE'"
                @click="run(() => registerPreviewNode(item.nodeId))"
                >{{ item.registered ? '重新导入' : '导入节点' }}</ElButton
              >
            </div>
          </div>
        </details>
        <details class="connection-extra">
          <summary>手工登记节点包</summary>
          <p v-if="!snapshot.directRegistrationAvailable" class="connection-empty">
            须先配置 Platform 受保护的机器凭据目录。
          </p>
          <ElButton
            :disabled="busy || !snapshot.directRegistrationAvailable"
            @click="openConnection()"
            >手工登记</ElButton
          >
        </details>
      </details>
      <div class="node-list">
        <article
          v-for="node in management
            ? snapshot.nodes.filter((item) => item.kind !== 'ZLM_DIRECT')
            : snapshot.nodes"
          :key="node.nodeId"
          class="node-card"
        >
          <div class="node-heading">
            <div>
              <h3>{{ node.name }}</h3>
              <small>{{ node.nodeId }} · 优先级 {{ node.priority }}</small>
            </div>
            <ElTag effect="plain">{{ names[node.mode] }}</ElTag>
          </div>
          <p class="node-state">
            实际状态：{{ names[node.state] || node.state
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
          <div class="node-operational">
            <dl class="node-metrics">
              <div>
                <dt>源流</dt>
                <dd>{{ node.usage.sources }} / {{ node.budget.maxSources }}</dd>
              </div>
              <div>
                <dt>观看</dt>
                <dd>{{ node.usage.viewers }} / {{ node.budget.maxViewers }}</dd>
              </div>
              <div>
                <dt>转码</dt>
                <dd>{{ node.usage.transcodes }} / {{ node.budget.maxTranscodes }}</dd>
              </div>
              <div>
                <dt>启动中</dt>
                <dd>{{ node.usage.starting }} / {{ node.budget.maxStarting }}</dd>
              </div>
            </dl>
            <div class="node-bandwidth">
              <span>预留带宽</span>
              <span
                >入站 {{ (node.usage.ingressKbps / 1000).toFixed(1) }} /
                {{ node.budget.ingressKbps / 1000 }} Mbps</span
              >
              <span
                >出口 {{ (node.usage.egressKbps / 1000).toFixed(1) }} /
                {{ node.budget.egressKbps / 1000 }} Mbps</span
              >
            </div>
            <ZlmNetworkLoad v-if="node.kind === 'ZLM_DIRECT'" :load="liveZlmLoad(node)" />
            <p v-if="node.reasonCode" class="node-reason">
              {{ previewReason(node.reasonCode) }} <code>{{ node.reasonCode }}</code>
            </p>
            <div class="node-actions">
              <ElButton @click="diagnostic = node">诊断 JSON</ElButton>
              <template v-if="canManage">
                <ElButton :disabled="busy" @click="edit(node)">预算与优先级</ElButton>
                <ElButton
                  v-if="node.kind !== 'ZLM_DIRECT' && node.nodeId !== 'local'"
                  :disabled="busy"
                  @click="openMaintenance(node)"
                  >维护许可</ElButton
                >
                <ElButton
                  v-if="node.mode !== 'REMOTE'"
                  :disabled="busy"
                  @click="mode(node, 'REMOTE')"
                  >参与调度</ElButton
                >
                <ElButton v-else :disabled="busy" @click="mode(node, 'DRAINING')"
                  >停止接新预览</ElButton
                >
              </template>
            </div>
            <details
              v-if="canManage && node.kind !== 'ZLM_DIRECT' && node.nodeId !== 'local'"
              class="node-advanced"
            >
              <summary>高级连接配置</summary>
              <ElButton :disabled="busy || node.mode === 'REMOTE'" @click="openConnection(node)"
                >手工修改连接</ElButton
              >
            </details>
          </div>
        </article>
      </div>
    </div>
    <ElDialog
      :model-value="!!directForm"
      title="接入 ZLM"
      width="min(620px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="directForm = null"
    >
      <form v-if="directForm" class="budget-form" @submit.prevent="saveDirectForm">
        <p class="connection-form-intro">填写 ZLM 的内网 HTTP API；接入后默认不参与调度。</p>
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
        <ElButton native-type="submit" :loading="busy">检查并接入</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directEditing"
      title="ZLM 预算设置"
      width="min(620px, calc(100vw - 32px))"
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
          })
        "
      >
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
        <ElButton native-type="submit" :loading="busy">保存</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directCredential"
      title="更新 ZLM secret"
      width="min(520px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="directCredential = null"
    >
      <form v-if="directCredential" class="budget-form" @submit.prevent="saveDirectCredential">
        <p class="connection-form-intro">
          {{ directCredential.instance.name }}：排空并确认资源归零后才能更新。
        </p>
        <label
          >新 secret<input
            v-model="directCredential.secret"
            type="password"
            autocomplete="new-password"
            required
        /></label>
        <ElButton native-type="submit" :loading="busy">更新密钥</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!directRemoving"
      title="移除 ZLM 实例"
      width="min(520px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="directRemoving = null"
    >
      <p v-if="directRemoving" class="maintenance-body">
        确认移除 {{ directRemoving.name }}？仅排空且无资源归属时允许移除。
      </p>
      <ElButton :loading="busy" @click="removeDirect()">确认移除</ElButton>
    </ElDialog>
    <ElDialog
      :model-value="!!connection"
      :title="connection?.version == null ? '新增远程预览节点' : '修改节点连接'"
      width="min(680px, calc(100vw - 32px))"
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
        <label
          >证书 SHA-256 指纹<input
            v-model.trim="connection.certificateSha256"
            minlength="64"
            maxlength="64"
            required
        /></label>
        <p>凭据文件须预先存放在 Platform 受保护目录，文件名为“节点 ID.token”。</p>
        <ElButton native-type="submit" :loading="busy">检查并登记</ElButton>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!maintaining"
      title="预览节点维护"
      width="min(680px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="maintaining = null"
    >
      <div v-if="maintaining" class="maintenance-body">
        <p>{{ maintaining.name }} · {{ names[maintaining.mode] }}</p>
        <ol>
          <li>停止接新预览，等待已有观看关闭及资源释放。</li>
          <li>获取维护许可，在 Ops 对应预览实例的升级或回退计划中填写许可编号。</li>
          <li>Ops 作业完成后检查节点；配置发生变化时重新导入登记，解除维护，再明确参与调度。</li>
        </ol>
        <RequestError
          :error="error"
          advice="未决 Ops 作业须先核对或恢复。许可过期不会自动解除维护。"
        />
        <p v-if="maintenance">
          维护状态：{{ maintenanceNames[maintenance.state] || maintenance.state }}
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
        <div class="node-actions">
          <ElButton :disabled="busy" @click="maintain()">刷新维护状态</ElButton>
          <ElButton
            :disabled="busy || maintaining.mode !== 'DRAINING' || maintenance?.state === 'CLAIMED'"
            @click="maintain('PREPARE')"
            >获取维护许可</ElButton
          >
          <ElButton
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
      title="预览节点诊断"
      width="min(760px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="diagnostic = null"
    >
      <pre class="node-json">{{ JSON.stringify(diagnostic, null, 2) }}</pre>
    </ElDialog>
    <ElDialog
      :model-value="!!editing"
      title="节点预算与优先级"
      width="min(620px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
      @close="editing = null"
    >
      <form v-if="editing" class="budget-form" @submit.prevent="save">
        <label
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
        <ElButton native-type="submit" :loading="busy">保存</ElButton>
      </form>
    </ElDialog>
  </section>
</template>

<style scoped>
.preview-node-panel {
  padding: 20px 24px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: var(--el-bg-color);
}
.node-title,
.node-title > div {
  display: flex;
  align-items: baseline;
  gap: 12px;
}
.node-title {
  justify-content: space-between;
  flex-wrap: wrap;
}
.node-title h2 {
  margin: 0;
  font-size: 17px;
}
.node-title span {
  color: var(--text-secondary);
  font-size: 13px;
}
.node-content {
  margin-top: 20px;
}
.node-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 520px), 1fr));
  gap: 16px;
  margin-top: 16px;
}
.node-card {
  container-name: node-card;
  container-type: inline-size;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 390px);
  column-gap: 24px;
  align-content: start;
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
  padding: 18px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
}
.node-heading {
  grid-column: 1 / -1;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
h3 {
  font-size: 16px;
  margin: 0 0 4px;
}
small,
.node-state,
.node-bandwidth,
.node-guide {
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.8;
}
.node-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin: 0 0 14px;
}
.node-metrics > div {
  min-width: 0;
}
.node-identity {
  margin: 0;
  display: grid;
  gap: 6px;
  align-content: start;
}
.node-state {
  grid-column: 1 / -1;
  margin: 10px 0 16px;
}
.node-operational {
  min-width: 0;
  padding-left: 24px;
  border-left: 1px solid var(--border-subtle);
}
.node-identity > div {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 10px;
  align-items: center;
}
.node-identity dd {
  overflow-wrap: break-word;
  margin: 0;
}
.node-ports {
  display: flex;
  flex-wrap: wrap;
  column-gap: 12px;
  row-gap: 2px;
}
.node-ports > span {
  white-space: nowrap;
}
.node-bandwidth {
  display: grid;
  gap: 2px;
  margin: 0 0 16px;
}
.node-bandwidth > span:first-child {
  color: var(--el-text-color-primary);
  font-weight: 600;
}
dt {
  color: var(--text-secondary);
  font-size: 12px;
}
dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
}
.node-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.node-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.node-actions a {
  font-size: 14px;
  color: var(--el-color-primary);
}
.node-guide {
  margin-top: 20px;
  border-top: 1px solid var(--border-subtle);
  padding-top: 16px;
}
.node-guide h3 {
  color: var(--el-text-color-primary);
}
.ops-zlm-catalog {
  margin: 12px 0 16px;
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
}
.ops-zlm-catalog > summary,
.node-package-guide > summary {
  cursor: pointer;
  color: var(--el-text-color-primary);
  font-size: 14px;
  font-weight: 600;
}
.ops-zlm-catalog > .connection-heading,
.node-package-guide > .connection-heading {
  margin-top: 12px;
}
.ops-zlm-catalog > summary span {
  color: var(--text-secondary);
  font-weight: 400;
}
.ops-zlm-catalog h4 {
  margin: 0;
  color: var(--el-text-color-primary);
  font-size: 13px;
}
.connection-empty,
.connection-hint {
  margin: 4px 0 0;
}
.connection-name code {
  overflow-wrap: anywhere;
}
.connection-heading,
.connection-name {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.connection-heading {
  justify-content: space-between;
}
.connection-heading h3 {
  margin: 0;
  color: var(--el-text-color-primary);
  font-size: 14px;
}
.connection-list {
  display: grid;
  gap: 8px;
  margin-top: 10px;
}
.connection-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
}
.connection-detail {
  min-width: 0;
}
.direct-row {
  align-items: center;
}
.direct-actions {
  justify-content: flex-end;
  max-width: 360px;
}
.node-package-guide {
  padding-bottom: 2px;
}
.connection-meta {
  display: flex;
  flex-wrap: wrap;
  column-gap: 14px;
  overflow-wrap: anywhere;
  font-size: 12px;
  line-height: 1.5;
}
.connection-meta > span {
  min-width: 0;
}
.connection-extra {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid var(--border-subtle);
}
.connection-extra summary {
  width: fit-content;
  cursor: pointer;
  color: var(--el-text-color-primary);
  font-size: 13px;
}
.connection-extra > .el-button {
  margin-top: 10px;
}
.node-advanced {
  margin-top: 10px;
  color: var(--text-secondary);
  font-size: 12px;
}
.node-advanced summary {
  width: fit-content;
  cursor: pointer;
}
.node-advanced > .el-button {
  margin-top: 8px;
}
.connection-name strong {
  color: var(--el-text-color-primary);
  font-size: 14px;
}
.connection-name code {
  font-size: 12px;
}
.node-reason {
  font-size: 13px;
  color: var(--el-color-warning-dark-2);
  overflow-wrap: anywhere;
}
.node-json {
  margin: 0;
  max-height: 60vh;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
  line-height: 1.7;
}
.budget-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.maintenance-body {
  font-size: 14px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.maintenance-body ol {
  padding-left: 22px;
}
.maintenance-body label {
  display: grid;
  gap: 6px;
}
.maintenance-body input {
  width: 100%;
  box-sizing: border-box;
  padding: 8px;
  color: var(--el-text-color-primary);
  background: var(--el-bg-color);
  border: 1px solid var(--border-subtle);
}
.budget-form label {
  display: grid;
  gap: 6px;
  font-size: 13px;
}
.budget-form input {
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
  padding: 8px;
  border: 1px solid var(--border-subtle);
  border-radius: 4px;
  background: var(--el-bg-color);
  color: var(--el-text-color-primary);
}
.budget-form p {
  grid-column: 1 / -1;
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-secondary);
}
.budget-form > .el-button {
  grid-column: 1 / -1;
  justify-self: start;
  min-width: 140px;
}
.budget-form .connection-form-intro {
  margin: 0;
}
.form-advanced {
  grid-column: 1 / -1;
  padding-top: 8px;
  border-top: 1px solid var(--border-subtle);
}
.form-advanced summary {
  width: fit-content;
  cursor: pointer;
  font-size: 13px;
}
.form-advanced-fields {
  margin-top: 14px;
}
@media (max-width: 560px) {
  .preview-node-panel {
    padding: 16px;
  }
  .node-identity > div {
    grid-template-columns: 84px minmax(0, 1fr);
  }
  .budget-form {
    grid-template-columns: 1fr;
  }
  .direct-actions {
    justify-content: flex-start;
  }
}
@media (max-width: 800px) {
  .connection-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .direct-actions {
    justify-content: flex-start;
    max-width: none;
  }
}
@container node-card (max-width: 1024px) {
  .node-identity,
  .node-operational {
    grid-column: 1 / -1;
  }
  .node-operational {
    margin-top: 16px;
    padding-top: 16px;
    padding-left: 0;
    border-top: 1px solid var(--border-subtle);
    border-left: 0;
  }
}
</style>
